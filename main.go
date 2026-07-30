//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const usage = `quill — minimal, fully local meeting recorder + transcriber for Windows

usage:
  quill record [-d seconds] [--no-transcribe]   record mic + system audio, then transcribe
  quill transcribe [session-dir]                transcribe one session, or all pending ones
  quill devices                                 list audio devices (* = will be recorded)
  quill doctor                                  check devices, engine, model, folders
  quill setup                                   download whisper.cpp + model (~150MB, once)

Each session lands in %USERPROFILE%\Recordings\<yyyy.MM.dd-HHmm>\:
mic.wav (you) + system.wav (them), meta.json, transcript.json, transcript.md.
Nothing ever leaves the machine.

env: QUILL_RECORDINGS_DIR, QUILL_WHISPER, QUILL_MODEL
`

func main() {
	if len(os.Args) < 2 {
		os.Stdout.WriteString(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "record":
		fs := flag.NewFlagSet("record", flag.ExitOnError)
		seconds := fs.Int("d", 0, "stop automatically after this many seconds")
		noTranscribe := fs.Bool("no-transcribe", false, "skip transcription after recording")
		fs.Parse(os.Args[2:])
		err = runRecord(time.Duration(*seconds)*time.Second, *noTranscribe)
	case "transcribe":
		arg := ""
		if len(os.Args) > 2 {
			arg = os.Args[2]
		}
		err = runTranscribe(arg)
	case "devices":
		err = runDevices()
	case "doctor":
		err = runDoctor()
	case "setup":
		err = runSetup()
	case "help", "-h", "--help":
		os.Stdout.WriteString(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
