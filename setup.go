//go:build windows

package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const whisperZipURL = "https://github.com/ggml-org/whisper.cpp/releases/download/v1.9.1/whisper-bin-x64.zip"

// knownModels maps --model names to rough download sizes, for the error
// message; any name whisper.cpp publishes works.
var knownModels = map[string]string{
	"tiny.en": "78MB", "tiny": "78MB",
	"base.en": "148MB", "base": "148MB",
	"small.en": "488MB", "small": "488MB",
	"medium.en": "1.5GB", "medium": "1.5GB",
	"large-v3-turbo": "1.6GB",
}

// runSetup downloads the whisper.cpp Windows build and a model into
// %LOCALAPPDATA%\quill. Both steps are idempotent per model.
func runSetup(model string) error {
	binDir := filepath.Join(appDir(), "bin")
	modelDir := filepath.Join(appDir(), "models")
	for _, d := range []string{binDir, modelDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	if findWhisperCLI() != "" {
		fmt.Println("whisper-cli already installed")
	} else {
		zipPath := filepath.Join(appDir(), "whisper-bin-x64.zip")
		if err := download(whisperZipURL, zipPath); err != nil {
			return fmt.Errorf("download whisper.cpp: %w", err)
		}
		if err := unzip(zipPath, binDir); err != nil {
			return fmt.Errorf("unpack whisper.cpp: %w", err)
		}
		os.Remove(zipPath)
		if findWhisperCLI() == "" {
			return fmt.Errorf("whisper-cli.exe not found in downloaded archive")
		}
		fmt.Println("whisper-cli installed")
	}

	if _, known := knownModels[model]; !known {
		names := make([]string, 0, len(knownModels))
		for name := range knownModels {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Printf("note: %q is not a model this tool knows (%s) — trying anyway\n",
			model, strings.Join(names, ", "))
	}
	dest := filepath.Join(modelDir, "ggml-"+model+".bin")
	if fileExists(dest) {
		fmt.Printf("model already installed: %s\n", filepath.Base(dest))
	} else {
		url := "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-" + model + ".bin"
		if size := knownModels[model]; size != "" {
			fmt.Printf("model %s is about %s\n", model, size)
		}
		if err := download(url, dest); err != nil {
			return fmt.Errorf("download model: %w", err)
		}
		fmt.Printf("model installed: %s\n", filepath.Base(dest))
	}

	fmt.Println("setup complete — `quill record` will now transcribe automatically")
	return nil
}

// download fetches url to dest atomically (temp file + rename), printing
// progress since the model is a ~140MB pull.
func download(url, dest string) error {
	fmt.Printf("downloading %s\n", url)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, &progressReader{r: resp.Body, total: resp.ContentLength})
	fmt.Println()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

type progressReader struct {
	r     io.Reader
	total int64
	done  int64
	last  int
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.total > 0 {
		if pct := int(p.done * 100 / p.total); pct != p.last {
			p.last = pct
			fmt.Printf("\r  %d%%", pct)
		}
	}
	return n, err
}

func unzip(src, destDir string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		dest := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if !filepath.IsLocal(f.Name) {
			return fmt.Errorf("unsafe path in archive: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(dest)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
