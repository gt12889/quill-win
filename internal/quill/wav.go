//go:build windows

package quill

import (
	"encoding/binary"
	"os"
)

// wavWriter streams 16-bit PCM to disk. The RIFF header is rewritten with
// current sizes on every flush(), so a crash mid-recording leaves a file
// that's readable up to the last flush — the same crash-safety goal as the
// original quill's CAF choice.
type wavWriter struct {
	f          *os.File
	sampleRate int
	channels   int
	dataBytes  int
}

func newWAVWriter(path string, sampleRate, channels int) (*wavWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &wavWriter{f: f, sampleRate: sampleRate, channels: channels}
	if err := w.writeHeader(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *wavWriter) writeHeader() error {
	blockAlign := w.channels * 2
	h := make([]byte, 0, 44)
	le := binary.LittleEndian
	u32 := func(v uint32) { h = le.AppendUint32(h, v) }
	u16 := func(v uint16) { h = le.AppendUint16(h, v) }

	h = append(h, "RIFF"...)
	u32(uint32(36 + w.dataBytes))
	h = append(h, "WAVEfmt "...)
	u32(16)
	u16(1) // PCM
	u16(uint16(w.channels))
	u32(uint32(w.sampleRate))
	u32(uint32(w.sampleRate * blockAlign))
	u16(uint16(blockAlign))
	u16(16) // bits per sample
	h = append(h, "data"...)
	u32(uint32(w.dataBytes))

	if _, err := w.f.WriteAt(h, 0); err != nil {
		return err
	}
	return nil
}

func (w *wavWriter) write(pcm []byte) error {
	n, err := w.f.WriteAt(pcm, int64(44+w.dataBytes))
	w.dataBytes += n
	return err
}

// writeSilence appends n frames of zeros.
func (w *wavWriter) writeSilence(frames int) error {
	return w.write(make([]byte, frames*w.channels*2))
}

func (w *wavWriter) frames() int {
	return w.dataBytes / (w.channels * 2)
}

func (w *wavWriter) flush() error {
	if err := w.writeHeader(); err != nil {
		return err
	}
	return w.f.Sync()
}

func (w *wavWriter) close() error {
	if err := w.writeHeader(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}
