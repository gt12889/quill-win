//go:build windows

package quill

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

// Per-application capture uses the Windows 10 2004+ process-loopback API:
// activating IAudioClient against the virtual loopback device with a target
// PID captures that process tree's audio only — the meeting app without
// notification dings or music players.

const (
	// VIRTUAL_AUDIO_DEVICE_PROCESS_LOOPBACK
	processLoopbackPath = `VAD\Process_Loopback`
	// AUDIOCLIENT_ACTIVATION_TYPE_PROCESS_LOOPBACK
	activationTypeProcessLoopback = 1
	// PROCESS_LOOPBACK_MODE_INCLUDE_TARGET_PROCESS_TREE
	loopbackModeIncludeTree = 0

	sOK          = 0
	eNoInterface = 0x80004002
	vtBlob       = 65
	infiniteWait = 0xFFFFFFFF
	eventWaitMs  = 2000
	activateWait = 5 * time.Second
)

// audioclientActivationParams mirrors AUDIOCLIENT_ACTIVATION_PARAMS with the
// process-loopback union member.
type audioclientActivationParams struct {
	activationType  uint32
	targetProcessID uint32
	loopbackMode    uint32
}

// blobPropVariant is a PROPVARIANT holding a VT_BLOB, laid out for amd64.
type blobPropVariant struct {
	vt     uint16
	_      [6]byte
	cbSize uint32
	_      [4]byte
	pBlob  *byte
}

var (
	iidActivateCompletionHandler = ole.NewGUID("{41D949AB-9862-444A-80F6-C261334DA5EB}")
	iidAgileObject               = ole.NewGUID("{94EA2B94-E9CC-49E0-C0FF-EE64CA8F5B90}")

	modMmdevapi                     = windows.NewLazySystemDLL("mmdevapi.dll")
	procActivateAudioInterfaceAsync = modMmdevapi.NewProc("ActivateAudioInterfaceAsync")

	handlerVtblOnce sync.Once
	handlerVtbl     completionHandlerVtbl
)

// asyncOperation is IActivateAudioInterfaceAsyncOperation.
type asyncOperation struct {
	vtbl *asyncOperationVtbl
}

type asyncOperationVtbl struct {
	queryInterface    uintptr
	addRef            uintptr
	release           uintptr
	getActivateResult uintptr
}

func (op *asyncOperation) getResult() (hr uint32, unk unsafe.Pointer, err error) {
	r, _, _ := syscall.SyscallN(op.vtbl.getActivateResult,
		uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(&hr)),
		uintptr(unsafe.Pointer(&unk)))
	if r != sOK {
		return 0, nil, fmt.Errorf("GetActivateResult failed: 0x%08x", r)
	}
	return hr, unk, nil
}

// completionHandler is our COM object: IActivateAudioInterfaceCompletionHandler
// plus IAgileObject, so the callback arrives on a worker thread with no
// apartment marshaling (and therefore no message pump needed).
type completionHandler struct {
	vtbl *completionHandlerVtbl
	hr   uint32
	unk  unsafe.Pointer
	err  error
	done chan struct{}
}

type completionHandlerVtbl struct {
	queryInterface    uintptr
	addRef            uintptr
	release           uintptr
	activateCompleted uintptr
}

func newCompletionHandler() *completionHandler {
	handlerVtblOnce.Do(func() {
		handlerVtbl = completionHandlerVtbl{
			queryInterface: syscall.NewCallback(func(this unsafe.Pointer, riid *ole.GUID, ppv *unsafe.Pointer) uintptr {
				if ole.IsEqualGUID(riid, ole.IID_IUnknown) ||
					ole.IsEqualGUID(riid, iidAgileObject) ||
					ole.IsEqualGUID(riid, iidActivateCompletionHandler) {
					*ppv = this
					return sOK
				}
				*ppv = nil
				return eNoInterface
			}),
			// The handler outlives the activation by construction (we
			// keep a Go reference until done), so refcounts are inert.
			addRef:  syscall.NewCallback(func(this unsafe.Pointer) uintptr { return 1 }),
			release: syscall.NewCallback(func(this unsafe.Pointer) uintptr { return 1 }),
			activateCompleted: syscall.NewCallback(func(this unsafe.Pointer, op *asyncOperation) uintptr {
				h := (*completionHandler)(this)
				h.hr, h.unk, h.err = op.getResult()
				close(h.done)
				return sOK
			}),
		}
	})
	return &completionHandler{vtbl: &handlerVtbl, done: make(chan struct{})}
}

// activateProcessLoopback returns an IAudioClient capturing the audio of one
// process tree.
func activateProcessLoopback(pid uint32) (*wca.IAudioClient, error) {
	params := audioclientActivationParams{
		activationType:  activationTypeProcessLoopback,
		targetProcessID: pid,
		loopbackMode:    loopbackModeIncludeTree,
	}
	pv := blobPropVariant{
		vt:     vtBlob,
		cbSize: uint32(unsafe.Sizeof(params)),
		pBlob:  (*byte)(unsafe.Pointer(&params)),
	}
	path, err := syscall.UTF16PtrFromString(processLoopbackPath)
	if err != nil {
		return nil, err
	}

	handler := newCompletionHandler()
	var op *asyncOperation
	hr, _, _ := procActivateAudioInterfaceAsync.Call(
		uintptr(unsafe.Pointer(path)),
		uintptr(unsafe.Pointer(wca.IID_IAudioClient)),
		uintptr(unsafe.Pointer(&pv)),
		uintptr(unsafe.Pointer(handler)),
		uintptr(unsafe.Pointer(&op)),
	)
	if int32(hr) < 0 {
		return nil, fmt.Errorf("ActivateAudioInterfaceAsync: 0x%08x", hr)
	}

	select {
	case <-handler.done:
	case <-time.After(activateWait):
		return nil, fmt.Errorf("process-loopback activation timed out")
	}
	if op != nil {
		syscall.SyscallN(op.vtbl.release, uintptr(unsafe.Pointer(op)))
	}
	if handler.err != nil {
		return nil, handler.err
	}
	if int32(handler.hr) < 0 {
		return nil, fmt.Errorf("process-loopback activation result: 0x%08x (is the PID alive?)", handler.hr)
	}
	return (*wca.IAudioClient)(handler.unk), nil
}

// attachProcess opens a capture stream on one process tree's audio. Process
// loopback requires event-driven mode; the capture loop still polls, using
// the event only to satisfy the API contract.
func attachProcess(pid uint32, name string) (*attachment, error) {
	a := &attachment{
		id:   fmt.Sprintf("pid:%d", pid),
		name: fmt.Sprintf("%s (pid %d)", name, pid),
	}
	ac, err := activateProcessLoopback(pid)
	if err != nil {
		return nil, err
	}
	a.ac = ac
	fail := func(stage string, err error) (*attachment, error) {
		a.release()
		return nil, fmt.Errorf("%s: %w", stage, err)
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
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_LOOPBACK | wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK)
	if err := a.ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, wca.REFERENCE_TIME(10_000_000), 0, &wfx, nil); err != nil {
		return fail("initialize", err)
	}
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return fail("create event", err)
	}
	a.event = event
	if err := a.ac.SetEventHandle(uintptr(event)); err != nil {
		return fail("set event handle", err)
	}
	if err := a.ac.GetService(wca.IID_IAudioCaptureClient, &a.acc); err != nil {
		return fail("capture service", err)
	}
	if err := a.ac.Start(); err != nil {
		return fail("start", err)
	}
	return a, nil
}

// resolveApp turns a --app argument (image name or PID) into a live process.
// Liveness matters: activating loopback against a dead PID "succeeds" and
// records silence forever.
func resolveApp(app string) (uint32, string, error) {
	var pid int
	if _, err := fmt.Sscanf(app, "%d", &pid); err == nil && fmt.Sprintf("%d", pid) == app {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return 0, "", fmt.Errorf("no running process with PID %d", pid)
		}
		var code uint32
		alive := windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
		windows.CloseHandle(h)
		if !alive {
			return 0, "", fmt.Errorf("process %d has exited", pid)
		}
		return uint32(pid), app, nil
	}
	want := strings.ToLower(app)
	if !strings.HasSuffix(want, ".exe") {
		want += ".exe"
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, "", err
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.ToLower(name) == want {
			return entry.ProcessID, name, nil
		}
	}
	return 0, "", fmt.Errorf("no running process named %q", app)
}
