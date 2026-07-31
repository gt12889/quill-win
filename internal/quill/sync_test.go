//go:build windows

package quill

import (
	"strings"
	"testing"
	"time"
)

func TestRenderNote(t *testing.T) {
	started := time.Date(2026, 7, 30, 14, 35, 2, 0, time.FixedZone("CDT", -5*3600))
	note := renderNote("2026.07.30-1435", noteMeta{
		Started:         started,
		DurationSeconds: 125,
		MicDevice:       "Microphone (fifine Microphone)",
		SystemDevice:    "Speakers (HT-NT5 B81D342)",
	}, []segment{
		{Speaker: "me", StartMS: 0, EndMS: 900, Text: "Hi."},
		{Speaker: "them", StartMS: 2000, EndMS: 3000, Text: "Hello."},
	})

	for _, want := range []string{
		"---\ndate: 2026-07-30\n",
		"started: 2026-07-30T14:35:02-05:00\n",
		"duration_minutes: 2\n",
		"mic: Microphone (fifine Microphone)\n",
		"system: Speakers (HT-NT5 B81D342)\n",
		"source: quill\n---\n",
		"# 2026.07.30-1435\n",
		"**me** — 00:00:00\nHi.\n",
		"**them** — 00:00:02\nHello.\n",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q\n---\n%s", want, note)
		}
	}
	if !strings.HasPrefix(note, "---\n") {
		t.Errorf("note must start with frontmatter, got:\n%s", note[:40])
	}
}
