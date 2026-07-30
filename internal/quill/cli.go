//go:build windows

package quill

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const usage = `quill — minimal, fully local meeting recorder + transcriber for Windows

usage:
  quill record [-d secs] [--app zoom.exe]       record mic + system audio, then transcribe
                                                (--app: only that app's audio, no dings)
  quill transcribe [session-dir]                transcribe one session, or all pending ones
  quill devices                                 list audio devices (* = will be recorded)
  quill doctor                                  check devices, engine, model, folders
  quill setup [--model base.en]                 download whisper.cpp + a model (once)
  quill install | uninstall                     start quill-tray at login (or stop doing so)

Each session lands in %USERPROFILE%\Recordings\<yyyy.MM.dd-HHmm>\:
mic.flac (you) + system.flac (them), meta.json, transcript.json, transcript.md.
Nothing ever leaves the machine.

Optional config at %APPDATA%\quill\config.json:
  {"recordings_dir": "...", "on_stop": "my-hook.cmd",
   "transcription": {"enabled": true, "model": "small.en", "language": "auto"}}

env (win over config): QUILL_RECORDINGS_DIR, QUILL_WHISPER, QUILL_MODEL, QUILL_LANG
`

// CLIMain is the quill command-line entry point.
func CLIMain() {
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
		app := fs.String("app", "", "record only this app's audio on the system track (image name or PID)")
		fs.Parse(os.Args[2:])
		err = runRecord(time.Duration(*seconds)*time.Second, *noTranscribe, *app)
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
	case "install":
		err = runInstall()
	case "uninstall":
		err = runUninstall()
	case "setup":
		fs := flag.NewFlagSet("setup", flag.ExitOnError)
		model := fs.String("model", "base.en", "whisper model to download (e.g. base.en, small.en, large-v3-turbo)")
		fs.Parse(os.Args[2:])
		err = runSetup(*model)
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
