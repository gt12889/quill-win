//go:build windows

package quill

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

// runRecord captures mic + system audio into a new session folder until
// Enter/Ctrl+C (or duration elapses), writes meta.json, then transcribes
// unless told not to.
func runRecord(duration time.Duration, noTranscribe bool) error {
	root := recordingsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	startedAt := time.Now()
	dir := filepath.Join(root, startedAt.Format("2006.01.02-1504"))
	for n := 2; ; n++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		dir = filepath.Join(root, fmt.Sprintf("%s-%d", startedAt.Format("2006.01.02-1504"), n))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := make(chan trackResult, 2)
	micStarted := make(chan string, 1)
	sysStarted := make(chan string, 1)
	go captureTrack(ctx, trackSystem, filepath.Join(dir, "system.flac"), sysStarted, results)
	go captureTrack(ctx, trackMic, filepath.Join(dir, "mic.flac"), micStarted, results)

	sysDev, micDev := <-sysStarted, <-micStarted
	if sysDev == "" || micDev == "" {
		// One track failed to start; never run half a session silently.
		cancel()
		res1, res2 := <-results, <-results
		os.RemoveAll(dir)
		for _, r := range []trackResult{res1, res2} {
			if r.err != nil {
				return r.err
			}
		}
		return fmt.Errorf("capture failed to start")
	}

	fmt.Printf("recording → %s\n", dir)
	fmt.Printf("  mic:    %s\n", micDev)
	fmt.Printf("  system: %s\n", sysDev)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	enter := make(chan struct{})
	go func() {
		// EOF (no interactive stdin) must not count as Enter, or a
		// recording launched from a script stops instantly.
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err == nil {
			close(enter)
		}
	}()

	var timeout <-chan time.Time
	if duration > 0 {
		timeout = time.After(duration)
		fmt.Printf("stopping automatically after %s\n", duration)
	} else {
		fmt.Println("press Enter or Ctrl+C to stop")
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
loop:
	for {
		select {
		case <-interrupt:
			break loop
		case <-enter:
			break loop
		case <-timeout:
			break loop
		case <-tick.C:
			elapsed := time.Since(startedAt).Round(time.Second)
			fmt.Printf("\r  %s ", elapsed)
		}
	}
	fmt.Println("\nstopping…")
	cancel()

	byKind := map[trackKind]trackResult{}
	for i := 0; i < 2; i++ {
		r := <-results
		byKind[r.kind] = r
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", r.err)
		}
	}
	ended := time.Now()

	if err := writeMeta(dir, startedAt, ended, byKind); err != nil {
		return err
	}
	fmt.Printf("saved %s (%ds)\n", dir, int(ended.Sub(startedAt).Seconds()))

	defer runOnStopHook(dir)
	if noTranscribe || !transcriptionEnabled() {
		return nil
	}
	if findWhisperCLI() == "" || findModel() == "" {
		fmt.Println("transcription engine not installed — run `quill setup`, then `quill transcribe` to catch up")
		return nil
	}
	return transcribeSession(dir)
}

// writeMeta mirrors the original quill's meta.json: timestamps, track files,
// and per-track start offsets so transcript timestamps share one clock.
func writeMeta(dir string, started, ended time.Time, tracks map[trackKind]trackResult) error {
	micStart, sysStart := started, started
	if t := tracks[trackMic].clockStart; !t.IsZero() {
		micStart = t
	}
	if t := tracks[trackSystem].clockStart; !t.IsZero() {
		sysStart = t
	}
	earliest := micStart
	if sysStart.Before(earliest) {
		earliest = sysStart
	}
	meta := map[string]any{
		"started":          started.Format(time.RFC3339),
		"ended":            ended.Format(time.RFC3339),
		"duration_seconds": int(ended.Sub(started).Seconds()),
		"files":            map[string]string{"mic": "mic.flac", "system": "system.flac"},
		"start_offset_ms": map[string]int{
			"mic":    int(micStart.Sub(earliest).Milliseconds()),
			"system": int(sysStart.Sub(earliest).Milliseconds()),
		},
		"devices": map[string]string{
			"mic":    tracks[trackMic].device,
			"system": tracks[trackSystem].device,
		},
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o644)
}
