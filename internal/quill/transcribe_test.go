//go:build windows

package quill

import (
	"strings"
	"testing"
)

func TestMsClock(t *testing.T) {
	cases := map[int]string{
		0:         "00:00:00",
		61_000:    "00:01:01",
		3_599_999: "00:59:59",
		3_661_000: "01:01:01",
	}
	for ms, want := range cases {
		if got := msClock(ms); got != want {
			t.Errorf("msClock(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestIsWhisperNoise(t *testing.T) {
	for _, noise := range []string{"[BLANK_AUDIO]", "(soft music)", "[typing]"} {
		if !isWhisperNoise(noise) {
			t.Errorf("isWhisperNoise(%q) = false", noise)
		}
	}
	for _, speech := range []string{"Hello there", "(almost) done", "well [sic] put"} {
		if isWhisperNoise(speech) {
			t.Errorf("isWhisperNoise(%q) = true", speech)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	md := renderMarkdown("2026.07.30-1400", []segment{
		{Speaker: "me", StartMS: 0, EndMS: 900, Text: "Hi."},
		{Speaker: "me", StartMS: 1000, EndMS: 1900, Text: "All good?"},
		{Speaker: "them", StartMS: 2000, EndMS: 3000, Text: "Yes."},
	})
	if !strings.HasPrefix(md, "# 2026.07.30-1400\n") {
		t.Errorf("missing title header:\n%s", md)
	}
	// Consecutive same-speaker segments share one heading.
	if strings.Count(md, "**me**") != 1 || strings.Count(md, "**them**") != 1 {
		t.Errorf("speaker headings wrong:\n%s", md)
	}
	if !strings.Contains(md, "**them** — 00:00:02") {
		t.Errorf("them heading/timestamp wrong:\n%s", md)
	}
}
