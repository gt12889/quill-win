//go:build windows

package quill

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// Tracks are archived as FLAC: lossless at the capture rate, roughly half the
// size of WAV for speech, and near-zero for the silence that dominates the
// system track. Every frame is independent, so a crash mid-meeting loses at
// most the samples still buffered below one block.
const flacBlockSize = 4096

type flacWriter struct {
	f   *os.File
	enc *flac.Encoder
	// pending holds samples not yet grouped into a full block.
	pending  []int32
	frameNum uint64
	total    int
	// err is sticky: the capture loop can't stop for a failing disk, so
	// the first write error is kept and surfaced by flush/close.
	err error
}

func newFLACWriter(path string, sampleRate int) (*flacWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	info := &meta.StreamInfo{
		BlockSizeMin:  flacBlockSize,
		BlockSizeMax:  flacBlockSize,
		SampleRate:    uint32(sampleRate),
		NChannels:     1,
		BitsPerSample: 16,
	}
	enc, err := flac.NewEncoder(f, info)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &flacWriter{f: f, enc: enc}, nil
}

// writePCM16 appends little-endian 16-bit mono PCM.
func (w *flacWriter) writePCM16(b []byte) error {
	for i := 0; i+1 < len(b); i += 2 {
		w.pending = append(w.pending, int32(int16(binary.LittleEndian.Uint16(b[i:]))))
	}
	w.total += len(b) / 2
	return w.drain()
}

func (w *flacWriter) writeSilence(frames int) error {
	w.pending = append(w.pending, make([]int32, frames)...)
	w.total += frames
	return w.drain()
}

func (w *flacWriter) drain() error {
	for len(w.pending) >= flacBlockSize {
		if err := w.encodeBlock(w.pending[:flacBlockSize]); err != nil {
			if w.err == nil {
				w.err = err
			}
			return err
		}
		w.pending = w.pending[flacBlockSize:]
	}
	return nil
}

func (w *flacWriter) encodeBlock(samples []int32) error {
	f := &frame.Frame{
		Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         uint16(len(samples)),
			SampleRate:        w.enc.Info.SampleRate,
			Channels:          frame.ChannelsMono,
			BitsPerSample:     16,
			Num:               w.frameNum,
		},
		Subframes: []*frame.Subframe{{
			// Verbatim lets the encoder's analysis pick constant/fixed
			// prediction per block, which is what shrinks silence.
			SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
			Samples:   samples,
			NSamples:  len(samples),
		}},
	}
	if err := w.enc.WriteFrame(f); err != nil {
		return err
	}
	w.frameNum++
	return nil
}

// frames reports samples accepted so far — the track clock includes what's
// still pending, since it will reach the file.
func (w *flacWriter) frames() int {
	return w.total
}

func (w *flacWriter) flush() error {
	if w.err != nil {
		return w.err
	}
	return w.f.Sync()
}

// close pads the final partial block with silence so every frame in the
// stream has the fixed block size, then finalizes StreamInfo.
func (w *flacWriter) close() error {
	if w.err != nil {
		w.enc.Close()
		return w.err
	}
	if n := len(w.pending); n > 0 {
		w.pending = append(w.pending, make([]int32, flacBlockSize-n)...)
		if err := w.drain(); err != nil {
			w.enc.Close()
			return err
		}
	}
	return w.enc.Close()
}

// flacTo16kWAV stream-decodes a mono capture-rate FLAC and writes the 16kHz
// mono WAV whisper wants, averaging each group of three samples as a cheap
// anti-alias filter (48000/16000 = 3).
func flacTo16kWAV(src, dst string) error {
	// Not flac.Open: it loses the *os.File behind a bufio.Reader and its
	// Stream.Close never closes the file — which on Windows would leave
	// the session locked for the life of a long-running tray process.
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	stream, err := flac.New(f)
	if err != nil {
		return err
	}
	if stream.Info.SampleRate != captureRate || stream.Info.NChannels != 1 {
		return fmt.Errorf("unexpected FLAC format: %d Hz, %d ch", stream.Info.SampleRate, stream.Info.NChannels)
	}

	w, err := newWAVWriter(dst, whisperRate, 1)
	if err != nil {
		return err
	}
	const ratio = captureRate / whisperRate
	var carry []int32
	out := make([]byte, 0, flacBlockSize)
	for {
		f, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			w.close()
			return err
		}
		carry = append(carry, f.Subframes[0].Samples...)
		out = out[:0]
		i := 0
		for ; i+ratio <= len(carry); i += ratio {
			avg := (carry[i] + carry[i+1] + carry[i+2]) / ratio
			out = binary.LittleEndian.AppendUint16(out, uint16(int16(avg)))
		}
		carry = carry[i:]
		if err := w.write(out); err != nil {
			w.close()
			return err
		}
	}
	return w.close()
}
