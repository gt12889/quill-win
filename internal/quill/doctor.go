//go:build windows

package quill

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows/registry"
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

	allowed, detail := micConsentAllowed()
	check(allowed, "mic privacy", detail)

	cli := findWhisperCLI()
	check(cli != "", "whisper-cli", orHint(cli, "run `quill setup`"))
	model := findModel()
	check(model != "", "model", orHint(model, "run `quill setup`"))

	err := os.MkdirAll(recordingsRoot(), 0o755)
	check(err == nil, "recordings dir", recordingsRoot())

	if dir := syncDir(); dir != "" {
		if _, statErr := os.Stat(dir); statErr != nil {
			check(false, "sync dir", dir+" — does not exist")
		} else if _, lookErr := exec.LookPath("git"); lookErr != nil {
			check(false, "sync git", "— git not on PATH")
		} else if wtErr := gitIn(dir, "rev-parse", "--is-inside-work-tree"); wtErr != nil {
			check(false, "sync dir", dir+" — not a git work tree")
		} else if remoteErr := gitIn(dir, "ls-remote", "--exit-code", "origin", "HEAD"); remoteErr != nil {
			check(false, "sync remote", "— origin missing or unreachable")
		} else {
			check(true, "sync", dir)
		}
	}

	if fileExists(configPath()) {
		fmt.Printf("ok       config %s\n", configPath())
	} else {
		fmt.Printf("ok       config none (defaults; %s)\n", configPath())
	}

	if !ok {
		return fmt.Errorf("some checks failed")
	}
	return nil
}

// micConsentAllowed reads Windows' microphone privacy consent for desktop
// apps. WASAPI just delivers silence when access is denied, so without this
// check a denied mic looks like a broken one.
func micConsentAllowed() (bool, string) {
	const base = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\microphone`
	for _, sub := range []string{base, base + `\NonPackaged`} {
		k, err := registry.OpenKey(registry.CURRENT_USER, sub, registry.QUERY_VALUE)
		if err != nil {
			// Missing keys (older Windows) mean no consent gate.
			continue
		}
		value, _, err := k.GetStringValue("Value")
		k.Close()
		if err == nil && value == "Deny" {
			return false, "— denied in Settings > Privacy & security > Microphone"
		}
	}
	return true, "microphone access allowed for desktop apps"
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
