//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// runDevices lists active audio endpoints, marking the defaults quill will
// record from.
func runDevices() error {
	return withCOM(func(enum *wca.IMMDeviceEnumerator) error {
		for _, flow := range []struct {
			flow  uint32
			label string
		}{{wca.ECapture, "capture (mic track)"}, {wca.ERender, "render (system track, via loopback)"}} {
			fmt.Printf("%s:\n", flow.label)
			defaultName := ""
			var def *wca.IMMDevice
			if err := enum.GetDefaultAudioEndpoint(flow.flow, wca.EConsole, &def); err == nil {
				defaultName = deviceName(def)
				def.Release()
			}
			var col *wca.IMMDeviceCollection
			if err := enum.EnumAudioEndpoints(flow.flow, wca.DEVICE_STATE_ACTIVE, &col); err != nil {
				return err
			}
			var count uint32
			col.GetCount(&count)
			for i := uint32(0); i < count; i++ {
				var dev *wca.IMMDevice
				if err := col.Item(i, &dev); err != nil {
					continue
				}
				name := deviceName(dev)
				dev.Release()
				marker := "  "
				if name == defaultName {
					marker = "* "
				}
				fmt.Printf("  %s%s\n", marker, name)
			}
			col.Release()
		}
		return nil
	})
}

// runDoctor reports whether everything quill needs is in place.
func runDoctor() error {
	ok := true
	check := func(good bool, label, detail string) {
		mark := "ok"
		if !good {
			mark = "MISSING"
			ok = false
		}
		fmt.Printf("%-8s %s %s\n", mark, label, detail)
	}

	micName, sysName := "", ""
	withCOM(func(enum *wca.IMMDeviceEnumerator) error {
		var dev *wca.IMMDevice
		if err := enum.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &dev); err == nil {
			micName = deviceName(dev)
			dev.Release()
		}
		if err := enum.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &dev); err == nil {
			sysName = deviceName(dev)
			dev.Release()
		}
		return nil
	})
	check(micName != "", "default mic", micName)
	check(sysName != "", "default output", sysName)

	cli := findWhisperCLI()
	check(cli != "", "whisper-cli", orHint(cli, "run `quill setup`"))
	model := findModel()
	check(model != "", "model", orHint(model, "run `quill setup`"))

	err := os.MkdirAll(recordingsRoot(), 0o755)
	check(err == nil, "recordings dir", recordingsRoot())

	if !ok {
		return fmt.Errorf("some checks failed")
	}
	return nil
}

func orHint(value, hint string) string {
	if value == "" {
		return "— " + hint
	}
	return value
}

// withCOM runs fn with COM initialized on a locked thread and a device
// enumerator ready.
func withCOM(fn func(*wca.IMMDeviceEnumerator) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return err
	}
	defer ole.CoUninitialize()
	var enum *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enum); err != nil {
		return err
	}
	defer enum.Release()
	return fn(enum)
}
