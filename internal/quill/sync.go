//go:build windows

package quill

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Transcript sync: after a session transcribes, its transcript is rendered
// as a standalone note (YAML frontmatter + the usual speaker-tagged body)
// into config.sync.dir, and committed/pushed if that folder is a git work
// tree. The frontmatter keys make the folder usable directly as an Obsidian
// vault.

// noteMeta is the session metadata that lands in a note's frontmatter.
type noteMeta struct {
	Started         time.Time
	DurationSeconds int
	MicDevice       string
	SystemDevice    string
}

func renderNote(sessionName string, meta noteMeta, segments []segment) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "date: %s\n", meta.Started.Format("2006-01-02"))
	fmt.Fprintf(&b, "started: %s\n", meta.Started.Format(time.RFC3339))
	fmt.Fprintf(&b, "duration_minutes: %d\n", (meta.DurationSeconds+30)/60)
	fmt.Fprintf(&b, "mic: %s\n", meta.MicDevice)
	fmt.Fprintf(&b, "system: %s\n", meta.SystemDevice)
	b.WriteString("source: quill\n---\n\n")
	b.WriteString(renderMarkdown(sessionName, segments))
	return b.String()
}

// syncSession renders the session's note into the configured sync dir and
// commits/pushes it. Sync is best-effort by design: any failure is reported
// to stderr and the session's transcribe.log, and the session still counts
// as a success — the local recording and transcript always win.
func syncSession(sessionDir string, segments []segment, logf *os.File) {
	dir := syncDir()
	if dir == "" {
		return
	}
	report := func(err error) {
		fmt.Fprintf(os.Stderr, "sync: %v\n", err)
		if logf != nil {
			fmt.Fprintf(logf, "sync: %v\n", err)
		}
	}

	meta, err := readNoteMeta(sessionDir)
	if err != nil {
		report(err)
		return
	}
	name := filepath.Base(sessionDir)
	notePath := filepath.Join(dir, name+".md")
	if err := os.WriteFile(notePath, []byte(renderNote(name, meta, segments)), 0o644); err != nil {
		report(err)
		return
	}

	if err := gitIn(dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		report(fmt.Errorf("%s is not a git work tree; note written but not committed", dir))
		return
	}
	if err := gitIn(dir, "add", name+".md"); err != nil {
		report(err)
		return
	}
	// A re-sync of an unchanged note has nothing staged; that's success,
	// not an error, so check before committing.
	if err := gitIn(dir, "diff", "--cached", "--quiet"); err == nil {
		return
	}
	if err := gitIn(dir, "commit", "-m", "meeting: "+name); err != nil {
		report(err)
		return
	}
	if !syncPush() {
		return
	}
	if err := gitIn(dir, "push"); err != nil {
		report(fmt.Errorf("%w (the next successful push will carry this commit)", err))
		return
	}
	fmt.Printf("synced %s.md → %s\n", name, dir)
}

// readNoteMeta pulls the frontmatter fields out of a session's meta.json.
func readNoteMeta(sessionDir string) (noteMeta, error) {
	var raw struct {
		Started         string            `json:"started"`
		DurationSeconds int               `json:"duration_seconds"`
		Devices         map[string]string `json:"devices"`
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, "meta.json"))
	if err != nil {
		return noteMeta{}, err
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return noteMeta{}, err
	}
	started, err := time.Parse(time.RFC3339, raw.Started)
	if err != nil {
		return noteMeta{}, fmt.Errorf("meta.json started: %w", err)
	}
	return noteMeta{
		Started:         started,
		DurationSeconds: raw.DurationSeconds,
		MicDevice:       raw.Devices["mic"],
		SystemDevice:    raw.Devices["system"],
	}, nil
}

// gitIn runs one git command with dir as the work tree, capturing output
// into the returned error on failure.
func gitIn(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}
