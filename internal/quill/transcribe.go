//go:build windows

package quill

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// segment is one timed utterance in the merged transcript.
type segment struct {
	Speaker string `json:"speaker"`
	StartMS int    `json:"start_ms"`
	EndMS   int    `json:"end_ms"`
	Text    string `json:"text"`
}

// whisperOutput matches whisper-cli's -oj JSON layout (the parts we use).
type whisperOutput struct {
	Transcription []struct {
		Offsets struct {
			From int `json:"from"`
			To   int `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// runTranscribe handles `quill transcribe [dir]`: one session, or every
// pending one (meta.json without transcript.json) under the recordings root —
// the filesystem is the queue, as in the original.
func runTranscribe(arg string) error {
	if arg != "" {
		return transcribeSession(arg)
	}
	pending := PendingSessions()
	if len(pending) == 0 {
		fmt.Println("nothing to transcribe")
		return nil
	}
	fmt.Printf("%d pending session(s)\n", len(pending))
	for _, dir := range pending {
		if err := transcribeSession(dir); err != nil {
			// Failures log and never block later jobs.
			fmt.Fprintf(os.Stderr, "%s: %v\n", dir, err)
		}
	}
	return nil
}

// PendingSessions lists finished-but-untranscribed session dirs, oldest
// first — the filesystem is the queue, so a crash or quit mid-transcription
// is picked up by the next `quill transcribe` or tray launch.
func PendingSessions() []string {
	entries, err := os.ReadDir(recordingsRoot())
	if err != nil {
		return nil
	}
	var pending []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(recordingsRoot(), e.Name())
		if fileExists(filepath.Join(dir, "meta.json")) && !fileExists(filepath.Join(dir, "transcript.json")) {
			pending = append(pending, dir)
		}
	}
	sort.Strings(pending)
	return pending
}

func transcribeSession(dir string) error {
	cli := findWhisperCLI()
	model := findModel()
	if cli == "" || model == "" {
		return fmt.Errorf("transcription engine not installed — run `quill setup`")
	}

	var meta struct {
		StartOffsetMS map[string]int `json:"start_offset_ms"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "meta.json")); err == nil {
		json.Unmarshal(data, &meta)
	}

	logf, err := os.OpenFile(filepath.Join(dir, "transcribe.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	fmt.Fprintf(logf, "--- %s\n", time.Now().Format(time.RFC3339))

	var segments []segment
	for _, t := range []struct{ track, speaker string }{{"mic", "me"}, {"system", "them"}} {
		track, speaker := t.track, t.speaker
		wav, cleanup, err := whisperInput(dir, track, logf)
		if err != nil {
			return fmt.Errorf("%s: %w", track, err)
		}
		if wav == "" {
			continue
		}
		fmt.Printf("transcribing %s (%s)…\n", filepath.Base(dir), track)
		out, err := runWhisper(cli, model, wav, logf)
		cleanup()
		if err != nil {
			return fmt.Errorf("%s: %w", track, err)
		}
		offset := meta.StartOffsetMS[track]
		for _, seg := range out.Transcription {
			text := strings.TrimSpace(seg.Text)
			if text == "" || isWhisperNoise(text) {
				continue
			}
			segments = append(segments, segment{
				Speaker: speaker,
				StartMS: seg.Offsets.From + offset,
				EndMS:   seg.Offsets.To + offset,
				Text:    text,
			})
		}
	}
	// Both tracks share one clock now; interleaving is a plain sort.
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].StartMS < segments[j].StartMS })

	canonical := map[string]any{
		"engine":   "whisper.cpp / " + filepath.Base(model),
		"created":  time.Now().Format(time.RFC3339),
		"segments": segments,
	}
	data, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.json"), data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.md"), []byte(renderMarkdown(filepath.Base(dir), segments)), 0o644); err != nil {
		return err
	}
	syncSession(dir, segments, logf)
	fmt.Printf("transcript ready: %s\n", filepath.Join(dir, "transcript.md"))
	return nil
}

// whisperInput returns a 16kHz WAV path for a track: FLAC archives are
// transcoded to a temporary WAV (cleanup removes it), legacy 16kHz WAV
// sessions are used as-is. An empty path means the track file is absent.
func whisperInput(dir, track string, logf *os.File) (path string, cleanup func(), err error) {
	cleanup = func() {}
	if flacPath := filepath.Join(dir, track+".flac"); fileExists(flacPath) {
		tmp := filepath.Join(dir, track+".16k.wav")
		if err := flacTo16kWAV(flacPath, tmp); err != nil {
			os.Remove(tmp)
			return "", cleanup, err
		}
		return tmp, func() { os.Remove(tmp) }, nil
	}
	if wavPath := filepath.Join(dir, track+".wav"); fileExists(wavPath) {
		return wavPath, cleanup, nil
	}
	fmt.Fprintf(logf, "%s track missing, skipping\n", track)
	return "", cleanup, nil
}

// runWhisper transcribes one WAV, returning parsed segments; whisper's
// stderr goes to the session's transcribe.log.
func runWhisper(cli, model, wav string, logf *os.File) (*whisperOutput, error) {
	outPrefix := strings.TrimSuffix(wav, ".wav")
	args := []string{"-m", model, "-f", wav, "-oj", "-of", outPrefix, "-np"}
	if lang := transcriptionLanguage(); lang != "" {
		args = append(args, "-l", lang)
	}
	cmd := exec.Command(cli, args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper-cli: %w (see transcribe.log)", err)
	}
	jsonPath := outPrefix + ".json"
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	defer os.Remove(jsonPath)
	var out whisperOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// isWhisperNoise filters the annotations whisper emits for non-speech, like
// "[BLANK_AUDIO]", "(soft music)", or "[typing]".
func isWhisperNoise(text string) bool {
	return (strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]")) ||
		(strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")"))
}

func renderMarkdown(session string, segments []segment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", session)
	last := ""
	for _, s := range segments {
		if s.Speaker != last {
			fmt.Fprintf(&b, "\n**%s** — %s\n", s.Speaker, msClock(s.StartMS))
			last = s.Speaker
		}
		fmt.Fprintf(&b, "%s\n", s.Text)
	}
	return b.String()
}

func msClock(ms int) string {
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, (s/60)%60, s%60)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
