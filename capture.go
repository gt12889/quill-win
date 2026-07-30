//go:build windows

package main

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// Both tracks are captured at whisper's native format so transcription needs
// no resampling step; WASAPI's AUTOCONVERTPCM does the conversion in the
// audio engine.
const (
	captureRate     = 16000
	captureChannels = 1
)

type trackKind int

const (
	trackMic trackKind = iota
	// trackSystem records what the machine plays (the other side of the
	// call) via WASAPI loopback on the default render device.
	trackSystem
)

func (k trackKind) String() string {
	if k == trackMic {
		return "mic"
	}
	return "system"
}

// trackResult is what a finished capture goroutine reports.
type trackResult struct {
	kind   trackKind
	device string
	// clockStart is the wall time of the track file's t=0; the capture
	// loop pads silence to keep the file in step with this clock.
	clockStart time.Time
	frames     int
	err        error
}

// captureTrack records one track until ctx is cancelled. It owns its OS
// thread for the lifetime of the capture because COM apartments are
// per-thread.
func captureTrack(ctx context.Context, kind trackKind, path string, started chan<- string, result chan<- trackResult) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	res := trackResult{kind: kind}
	defer func() { result <- res }()

	fail := func(stage string, err error) {
		res.err = fmt.Errorf("%s: %s: %w", kind, stage, err)
		select {
		case started <- "":
		default:
		}
	}

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		fail("CoInitializeEx", err)
		return
	}
	defer ole.CoUninitialize()

	var enum *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enum); err != nil {
		fail("device enumerator", err)
		return
	}
	defer enum.Release()

	dataFlow := uint32(wca.ECapture)
	if kind == trackSystem {
		dataFlow = wca.ERender
	}
	var dev *wca.IMMDevice
	if err := enum.GetDefaultAudioEndpoint(dataFlow, wca.EConsole, &dev); err != nil {
		fail("default endpoint", err)
		return
	}
	defer dev.Release()
	res.device = deviceName(dev)

	var ac *wca.IAudioClient
	if err := dev.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		fail("activate audio client", err)
		return
	}
	defer ac.Release()

	wfx := wca.WAVEFORMATEX{
		WFormatTag:      1, // PCM
		NChannels:       captureChannels,
		NSamplesPerSec:  captureRate,
		NAvgBytesPerSec: captureRate * captureChannels * 2,
		NBlockAlign:     captureChannels * 2,
		WBitsPerSample:  16,
	}
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
	if kind == trackSystem {
		flags |= wca.AUDCLNT_STREAMFLAGS_LOOPBACK
	}
	// 1-second engine buffer: we poll every 10ms, so this is generous
	// headroom against scheduling hiccups.
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, wca.REFERENCE_TIME(10_000_000), 0, &wfx, nil); err != nil {
		fail("initialize", err)
		return
	}

	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		fail("capture service", err)
		return
	}
	defer acc.Release()

	w, err := newWAVWriter(path, captureRate, captureChannels)
	if err != nil {
		fail("create wav", err)
		return
	}
	defer w.close()

	if err := ac.Start(); err != nil {
		fail("start", err)
		return
	}
	defer ac.Stop()
	startedAt := time.Now()
	res.clockStart = startedAt
	started <- res.device

	blockAlign := int(wfx.NBlockAlign)
	lastFlush := startedAt
	for {
		select {
		case <-ctx.Done():
			drainPackets(acc, w, blockAlign)
			res.frames = w.frames()
			if err := w.flush(); err != nil && res.err == nil {
				res.err = fmt.Errorf("%s: flush: %w", kind, err)
			}
			return
		default:
		}

		drainPackets(acc, w, blockAlign)

		// Keep the file in step with the wall clock: loopback delivers
		// nothing while the machine plays silence, and a mic can lag or
		// underrun. Pad with silence whenever the track falls more than
		// 100ms behind, so both tracks stay merge-ably aligned on the
		// clock that started at capture start.
		expected := int(time.Since(startedAt).Seconds() * captureRate)
		if gap := expected - w.frames(); gap > captureRate/10 {
			w.writeSilence(gap)
		}

		if time.Since(lastFlush) > time.Second {
			w.flush()
			lastFlush = time.Now()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// drainPackets copies every packet the engine has ready into the writer.
func drainPackets(acc *wca.IAudioCaptureClient, w *wavWriter, blockAlign int) {
	for {
		var packetFrames uint32
		if err := acc.GetNextPacketSize(&packetFrames); err != nil || packetFrames == 0 {
			return
		}
		var (
			data          *byte
			frames, flags uint32
			devPos, qpc   uint64
		)
		if err := acc.GetBuffer(&data, &frames, &flags, &devPos, &qpc); err != nil {
			return
		}
		if frames > 0 {
			if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 {
				w.writeSilence(int(frames))
			} else {
				w.write(unsafe.Slice(data, int(frames)*blockAlign))
			}
		}
		acc.ReleaseBuffer(frames)
	}
}

func deviceName(dev *wca.IMMDevice) string {
	var ps *wca.IPropertyStore
	if err := dev.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return "unknown"
	}
	defer ps.Release()
	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
		return "unknown"
	}
	return pv.String()
}
