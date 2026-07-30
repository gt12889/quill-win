//go:build windows

package quill

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

// Tracks are captured at 48kHz mono — full speech quality for replay —
// with WASAPI's AUTOCONVERTPCM converting from whatever the device's mix
// format is. Transcription downsamples to whisper's 16kHz separately.
const (
	captureRate     = 48000
	captureChannels = 1
	whisperRate     = 16000
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

// attachment is one live WASAPI capture stream, on a device or (for --app)
// on a process tree.
type attachment struct {
	dev        *wca.IMMDevice
	ac         *wca.IAudioClient
	acc        *wca.IAudioCaptureClient
	event      windows.Handle
	id         string
	name       string
	blockAlign int
}

func (a *attachment) release() {
	if a.ac != nil {
		a.ac.Stop()
	}
	if a.acc != nil {
		a.acc.Release()
	}
	if a.ac != nil {
		a.ac.Release()
	}
	if a.dev != nil {
		a.dev.Release()
	}
	if a.event != 0 {
		windows.CloseHandle(a.event)
	}
	a.acc, a.ac, a.dev, a.event = nil, nil, nil, 0
}

// processTarget selects per-app capture for the system track; the zero value
// means capture the whole default output device.
type processTarget struct {
	pid  uint32
	name string
}

// attach opens a capture stream on the current default device for the track.
func attach(enum *wca.IMMDeviceEnumerator, kind trackKind) (*attachment, error) {
	dataFlow := uint32(wca.ECapture)
	if kind == trackSystem {
		dataFlow = wca.ERender
	}
	a := &attachment{}
	fail := func(stage string, err error) (*attachment, error) {
		a.release()
		return nil, fmt.Errorf("%s: %w", stage, err)
	}

	if err := enum.GetDefaultAudioEndpoint(dataFlow, wca.EConsole, &a.dev); err != nil {
		return fail("default endpoint", err)
	}
	a.dev.GetId(&a.id)
	a.name = deviceName(a.dev)

	if err := a.dev.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &a.ac); err != nil {
		return fail("activate audio client", err)
	}

	wfx := wca.WAVEFORMATEX{
		WFormatTag:      1, // PCM
		NChannels:       captureChannels,
		NSamplesPerSec:  captureRate,
		NAvgBytesPerSec: captureRate * captureChannels * 2,
		NBlockAlign:     captureChannels * 2,
		WBitsPerSample:  16,
	}
	a.blockAlign = int(wfx.NBlockAlign)
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
	if kind == trackSystem {
		flags |= wca.AUDCLNT_STREAMFLAGS_LOOPBACK
	}
	// 1-second engine buffer: we poll every 10ms, so this is generous
	// headroom against scheduling hiccups.
	if err := a.ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, wca.REFERENCE_TIME(10_000_000), 0, &wfx, nil); err != nil {
		return fail("initialize", err)
	}
	if err := a.ac.GetService(wca.IID_IAudioCaptureClient, &a.acc); err != nil {
		return fail("capture service", err)
	}
	if err := a.ac.Start(); err != nil {
		return fail("start", err)
	}
	return a, nil
}

// captureTrack records one track until ctx is cancelled. It owns its OS
// thread for the lifetime of the capture because COM apartments are
// per-thread. If the device disappears mid-recording (headset unplugged) or
// the user switches the Windows default, capture reattaches to the new
// default and the wall-clock padding below covers the gap — a device change
// costs a moment of silence, never the session.
func captureTrack(ctx context.Context, kind trackKind, path string, target processTarget, started chan<- string, result chan<- trackResult) {
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

	openStream := func() (*attachment, error) {
		if kind == trackSystem && target.pid != 0 {
			return attachProcess(target.pid, target.name)
		}
		return attach(enum, kind)
	}

	att, err := openStream()
	if err != nil {
		fail("attach", err)
		return
	}
	defer func() { att.release() }()
	res.device = att.name

	w, err := newFLACWriter(path, captureRate)
	if err != nil {
		fail("create flac", err)
		return
	}
	defer w.close()

	startedAt := time.Now()
	res.clockStart = startedAt
	started <- res.device

	lastFlush := startedAt
	lastDeviceCheck := startedAt
	for {
		select {
		case <-ctx.Done():
			drainPackets(att, w)
			res.frames = w.frames()
			if err := w.flush(); err != nil && res.err == nil {
				res.err = fmt.Errorf("%s: flush: %w", kind, err)
			}
			return
		default:
		}

		if err := drainPackets(att, w); err != nil {
			fmt.Fprintf(os.Stderr, "%s: device lost (%v), reattaching…\n", kind, err)
			att = reattach(ctx, openStream, kind, att)
		} else if target.pid == 0 && time.Since(lastDeviceCheck) > 2*time.Second {
			lastDeviceCheck = time.Now()
			if id := currentDefaultID(enum, kind); id != "" && id != att.id {
				fmt.Fprintf(os.Stderr, "%s: default device changed, following it…\n", kind)
				att = reattach(ctx, openStream, kind, att)
			}
		}

		// Keep the file in step with the wall clock: loopback delivers
		// nothing while the machine plays silence, a mic can lag or
		// underrun, and a device swap leaves a hole. Pad with silence
		// whenever the track falls more than 100ms behind, so both
		// tracks stay merge-ably aligned on the clock that started at
		// capture start.
		expected := int(time.Since(startedAt).Seconds() * captureRate)
		if gap := expected - w.frames(); gap > captureRate/10 {
			w.writeSilence(gap)
		}

		if time.Since(lastFlush) > time.Second {
			if err := w.flush(); err != nil && res.err == nil {
				res.err = fmt.Errorf("%s: %w", kind, err)
			}
			lastFlush = time.Now()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// reattach tears down a dead attachment and opens a new one, retrying until
// it succeeds or the recording stops. It always returns a usable-or-inert
// attachment so the capture loop can keep padding the clock while the source
// is missing.
func reattach(ctx context.Context, openStream func() (*attachment, error), kind trackKind, old *attachment) *attachment {
	old.release()
	for {
		select {
		case <-ctx.Done():
			return &attachment{}
		default:
		}
		att, err := openStream()
		if err == nil {
			fmt.Fprintf(os.Stderr, "%s: now recording from %s\n", kind, att.name)
			return att
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func currentDefaultID(enum *wca.IMMDeviceEnumerator, kind trackKind) string {
	dataFlow := uint32(wca.ECapture)
	if kind == trackSystem {
		dataFlow = wca.ERender
	}
	var dev *wca.IMMDevice
	if err := enum.GetDefaultAudioEndpoint(dataFlow, wca.EConsole, &dev); err != nil {
		return ""
	}
	defer dev.Release()
	var id string
	dev.GetId(&id)
	return id
}

// drainPackets copies every packet the engine has ready into the writer. A
// non-nil error means the device is gone, not that data ran out.
func drainPackets(att *attachment, w *flacWriter) error {
	if att.acc == nil {
		return nil
	}
	for {
		var packetFrames uint32
		if err := att.acc.GetNextPacketSize(&packetFrames); err != nil {
			return err
		}
		if packetFrames == 0 {
			return nil
		}
		var (
			data          *byte
			frames, flags uint32
			devPos, qpc   uint64
		)
		if err := att.acc.GetBuffer(&data, &frames, &flags, &devPos, &qpc); err != nil {
			return err
		}
		if frames > 0 {
			if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 {
				w.writeSilence(int(frames))
			} else {
				w.writePCM16(unsafe.Slice(data, int(frames)*att.blockAlign))
			}
		}
		att.acc.ReleaseBuffer(frames)
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
