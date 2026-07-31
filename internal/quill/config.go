//go:build windows

package quill

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// configFile mirrors the original quill's optional config, at
// %APPDATA%\quill\config.json. Environment variables win over the file.
type configFile struct {
	RecordingsDir string `json:"recordings_dir"`
	Transcription struct {
		Enabled  *bool  `json:"enabled"`
		Language string `json:"language"`
		Model    string `json:"model"`
	} `json:"transcription"`
	Sync struct {
		Dir  string `json:"dir"`
		Push *bool  `json:"push"`
	} `json:"sync"`
	OnStop string `json:"on_stop"`
}

var config = loadConfig()

func configPath() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(base, "quill", "config.json")
}

func loadConfig() configFile {
	var c configFile
	data, err := os.ReadFile(configPath())
	if err != nil {
		return c
	}
	if err := json.Unmarshal(data, &c); err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring malformed %s: %v\n", configPath(), err)
		return configFile{}
	}
	return c
}

func transcriptionEnabled() bool {
	if c := config.Transcription.Enabled; c != nil {
		return *c
	}
	return true
}

// transcriptionLanguage is what whisper's -l gets: QUILL_LANG, then the
// config file; empty means let whisper use its default (English).
func transcriptionLanguage() string {
	if l := os.Getenv("QUILL_LANG"); l != "" {
		return l
	}
	return config.Transcription.Language
}

// runOnStopHook fires the configured command with the finished session dir
// as its argument. With transcription disabled or unavailable the hook still
// fires — it just gets an untranscribed folder.
func runOnStopHook(sessionDir string) {
	if config.OnStop == "" {
		return
	}
	cmd := exec.Command("cmd", "/C", config.OnStop, sessionDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "on_stop hook: %v\n", err)
	}
}

// syncDir returns the transcript-sync destination; empty means sync is off.
func syncDir() string {
	return config.Sync.Dir
}

// syncPush reports whether sync should git-push (default true).
func syncPush() bool {
	if p := config.Sync.Push; p != nil {
		return *p
	}
	return true
}
