//go:build windows

package quill

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mewkiz/flac"
)

// readAllFLAC decodes every sample of a mono FLAC file.
func readAllFLAC(t *testing.T, path string) []int32 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	stream, err := flac.New(f)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var samples []int32
	for {
		f, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		samples = append(samples, f.Subframes[0].Samples...)
	}
	return samples
}

// TestFLACRoundTrip writes a sine plus silence through the streaming writer,
// including a partial final block, and expects the decoder to hand back
// exactly what went in (plus the final block's zero padding).
func TestFLACRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roundtrip.flac")
	w, err := newFLACWriter(path, captureRate)
	if err != nil {
		t.Fatal(err)
	}

	const n = flacBlockSize*3 + 1234 // deliberately not block-aligned
	want := make([]int32, n)
	pcm := make([]byte, 0, n*2)
	for i := range want {
		v := int16(12000 * math.Sin(float64(i)/97))
		want[i] = int32(v)
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
	}
	if err := w.writePCM16(pcm); err != nil {
		t.Fatal(err)
	}
	if err := w.writeSilence(flacBlockSize / 2); err != nil {
		t.Fatal(err)
	}
	if got := w.frames(); got != n+flacBlockSize/2 {
		t.Fatalf("frames() = %d, want %d", got, n+flacBlockSize/2)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}

	got := readAllFLAC(t, path)
	if len(got)%flacBlockSize != 0 {
		t.Fatalf("stream not block-padded: %d samples", len(got))
	}
	if len(got) < n {
		t.Fatalf("decoded %d samples, want at least %d", len(got), n)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("sample %d = %d, want %d", i, got[i], v)
		}
	}
	for i := n; i < len(got); i++ {
		if got[i] != 0 && (i-n) < flacBlockSize/2 {
			t.Fatalf("silence sample %d = %d", i, got[i])
		}
	}
}

// TestFlacTo16kWAV checks the decimation: every output sample must be the
// average of its three source samples, in a proper 16kHz mono WAV.
func TestFlacTo16kWAV(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.flac")
	dst := filepath.Join(dir, "dst.wav")

	w, err := newFLACWriter(src, captureRate)
	if err != nil {
		t.Fatal(err)
	}
	const n = flacBlockSize * 2
	pcm := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(int16(i%3000)))
	}
	if err := w.writePCM16(pcm); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}

	if err := flacTo16kWAV(src, dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "RIFF" || string(data[8:16]) != "WAVEfmt " {
		t.Fatalf("not a WAV header: %q", data[:16])
	}
	if rate := binary.LittleEndian.Uint32(data[24:28]); rate != whisperRate {
		t.Fatalf("sample rate = %d, want %d", rate, whisperRate)
	}
	if ch := binary.LittleEndian.Uint16(data[22:24]); ch != 1 {
		t.Fatalf("channels = %d, want 1", ch)
	}
	body := data[44:]
	for i := 0; i+2 <= len(body) && i/2 < n/3; i += 2 {
		got := int32(int16(binary.LittleEndian.Uint16(body[i:])))
		s := i / 2 * 3
		want := (int32(int16(s%3000)) + int32(int16((s+1)%3000)) + int32(int16((s+2)%3000))) / 3
		if got != want {
			t.Fatalf("output sample %d = %d, want %d", i/2, got, want)
		}
	}
}

// TestFLACWriterStickyError makes sure a dead file surfaces on flush, not
// silently never.
func TestFLACWriterStickyError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sticky.flac")
	w, err := newFLACWriter(path, captureRate)
	if err != nil {
		t.Fatal(err)
	}
	w.f.Close() // simulate the disk going away
	big := make([]byte, flacBlockSize*4)
	w.writePCM16(big)
	if err := w.flush(); err == nil {
		t.Fatal("flush() = nil after writes to a closed file")
	}
	if err := w.close(); err == nil {
		t.Fatal("close() = nil after writes to a closed file")
	}
}
