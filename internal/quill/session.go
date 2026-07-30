//go:build windows

package quill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Recording is a live two-track capture session. StartRecording /
// Stop / FinishSession are the seams shared by the CLI and the tray app.
type Recording struct {
	Dir          string
	StartedAt    time.Time
	MicDevice    string
	SystemDevice string

	cancel  context.CancelFunc
	results chan trackResult
}

// StartRecording creates a session folder under the recordings root and
// starts both tracks. If either track fails to start, the other is torn
// down and the folder removed — never half a session silently.
func StartRecording() (*Recording, error) {
	root := recordingsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	// os.Mkdir (not MkdirAll) atomically claims the folder name, so two
	// recordings started in the same minute can never share a session.
	startedAt := time.Now()
	dir := filepath.Join(root, startedAt.Format("2006.01.02-1504"))
	for n := 2; ; n++ {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return nil, err
		}
		dir = filepath.Join(root, fmt.Sprintf("%s-%d", startedAt.Format("2006.01.02-1504"), n))
	}

	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan trackResult, 2)
	micStarted := make(chan string, 1)
	sysStarted := make(chan string, 1)
	go captureTrack(ctx, trackSystem, filepath.Join(dir, "system.flac"), sysStarted, results)
	go captureTrack(ctx, trackMic, filepath.Join(dir, "mic.flac"), micStarted, results)

	sysDev, micDev := <-sysStarted, <-micStarted
	if sysDev == "" || micDev == "" {
		cancel()
		res1, res2 := <-results, <-results
		os.RemoveAll(dir)
		for _, r := range []trackResult{res1, res2} {
			if r.err != nil {
				return nil, r.err
			}
		}
		return nil, fmt.Errorf("capture failed to start")
	}
	return &Recording{
		Dir:          dir,
		StartedAt:    startedAt,
		MicDevice:    micDev,
		SystemDevice: sysDev,
		cancel:       cancel,
		results:      results,
	}, nil
}

// Elapsed reports how long the session has been recording.
func (r *Recording) Elapsed() time.Duration {
	return time.Since(r.StartedAt)
}

// Stop ends both tracks and writes meta.json. Track problems (a device that
// died mid-session) are warnings on stderr, not errors — the audio that was
// captured is still saved.
func (r *Recording) Stop() error {
	r.cancel()
	byKind := map[trackKind]trackResult{}
	for i := 0; i < 2; i++ {
		res := <-r.results
		byKind[res.kind] = res
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", res.err)
		}
	}
	return writeMeta(r.Dir, r.StartedAt, time.Now(), byKind)
}

// FinishSession is the post-recording tail shared by every UI: transcribe
// when wanted and possible, then fire the on_stop hook either way.
func FinishSession(dir string, transcribe bool) error {
	defer runOnStopHook(dir)
	if !transcribe || !transcriptionEnabled() {
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

// runRecord is the CLI recording flow: capture until Enter/Ctrl+C (or the
// duration elapses), then finish the session.
func runRecord(duration time.Duration, noTranscribe bool) error {
	rec, err := StartRecording()
	if err != nil {
		return err
	}

	fmt.Printf("recording → %s\n", rec.Dir)
	fmt.Printf("  mic:    %s\n", rec.MicDevice)
	fmt.Printf("  system: %s\n", rec.SystemDevice)

	waitForStop(duration)
	fmt.Println("\nstopping…")
	if err := rec.Stop(); err != nil {
		return err
	}
	fmt.Printf("saved %s (%ds)\n", rec.Dir, int(rec.Elapsed().Seconds()))
	return FinishSession(rec.Dir, !noTranscribe)
}
