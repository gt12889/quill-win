//go:build windows

package quill

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// startupShortcut is where `quill install` puts the launch-at-login link.
func startupShortcut() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(base, `Microsoft\Windows\Start Menu\Programs\Startup`, "quill.lnk")
}

// runInstall makes quill-tray start at login, the Windows analogue of the
// original's LaunchAgent: a shortcut in the user's Startup folder pointing
// at quill-tray.exe next to this binary.
func runInstall() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tray := filepath.Join(filepath.Dir(self), "quill-tray.exe")
	if !fileExists(tray) {
		return fmt.Errorf("quill-tray.exe not found next to quill.exe (%s)", tray)
	}

	// Shortcuts are a COM affair; WScript.Shell via PowerShell is the
	// no-dependency way to make one.
	script := fmt.Sprintf(
		`$s = (New-Object -ComObject WScript.Shell).CreateShortcut('%s'); $s.TargetPath = '%s'; $s.Description = 'quill meeting recorder'; $s.Save()`,
		startupShortcut(), tray)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create shortcut: %w: %s", err, out)
	}
	fmt.Printf("installed: %s\nquill-tray will start at login; run it now with: %s\n", startupShortcut(), tray)
	return nil
}

// runUninstall removes the launch-at-login shortcut.
func runUninstall() error {
	link := startupShortcut()
	if !fileExists(link) {
		fmt.Println("not installed (no startup shortcut)")
		return nil
	}
	if err := os.Remove(link); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", link)
	return nil
}
