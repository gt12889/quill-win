//go:build windows

package quill

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// recordingsRoot is where session folders land: %USERPROFILE%\Recordings,
// overridable with QUILL_RECORDINGS_DIR or the config file.
func recordingsRoot() string {
	if dir := os.Getenv("QUILL_RECORDINGS_DIR"); dir != "" {
		return dir
	}
	if config.RecordingsDir != "" {
		return config.RecordingsDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return `C:\Recordings`
	}
	return filepath.Join(home, "Recordings")
}

// appDir is quill's own state: %LOCALAPPDATA%\quill, holding the whisper.cpp
// binaries (bin\) and models (models\) that `quill setup` downloads.
func appDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "quill")
}

// findWhisperCLI locates whisper-cli.exe: QUILL_WHISPER env var first, then
// anywhere under appDir()\bin, then PATH.
func findWhisperCLI() string {
	if p := os.Getenv("QUILL_WHISPER"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	var found string
	filepath.WalkDir(filepath.Join(appDir(), "bin"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "whisper-cli.exe" {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if found != "" {
		return found
	}
	if p, err := exec.LookPath("whisper-cli.exe"); err == nil {
		return p
	}
	return ""
}

// findModel locates the ggml model: QUILL_MODEL first, then the config
// file's transcription.model (a path, or a name like "small.en" resolved in
// the models dir), then any ggml-*.bin under appDir()\models.
func findModel() string {
	if p := os.Getenv("QUILL_MODEL"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if m := config.Transcription.Model; m != "" {
		for _, candidate := range []string{m, filepath.Join(appDir(), "models", "ggml-"+m+".bin")} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	matches, _ := filepath.Glob(filepath.Join(appDir(), "models", "ggml-*.bin"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}
