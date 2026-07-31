//go:build windows

package quill

import (
	"fmt"
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
