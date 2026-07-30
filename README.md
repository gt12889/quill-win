# quill-win

A minimal, fully local Windows meeting recorder + transcriber — a port of
[digimata/quill](https://github.com/digimata/quill) (macOS) to Windows.
One command records your mic and all system audio as two separate tracks;
when you stop, quill transcribes both on-device and writes a speaker-tagged
transcript. Nothing ever leaves the machine.

Single Go binary, no services, no virtual audio devices — system audio comes
from WASAPI loopback, which Windows provides natively.

## Install

Build from WSL or any machine with Go 1.24+:

```sh
GOOS=windows GOARCH=amd64 go build -o quill.exe .
```

Then, once, on the Windows side:

```
quill setup     # downloads whisper.cpp + the base.en model (~290MB total)
quill doctor    # confirms devices, engine, model, folders
```

## How to use

```
quill record            # start; press Enter or Ctrl+C to stop
quill record -d 3600    # or stop automatically after an hour
```

Recording captures the **default** mic and the **default** output device
(`quill devices` shows which, marked `*`). When you stop, transcription runs
automatically and prints the transcript path.

Each session lands in `%USERPROFILE%\Recordings\<yyyy.MM.dd-HHmm>\`:

| File | Contents |
|---|---|
| `mic.wav` | your side (default mic, 16kHz mono PCM) |
| `system.wav` | everything the PC played — the other side of the call |
| `meta.json` | start/end timestamps, duration, per-track start offsets |
| `transcript.json` | canonical transcript — engine + timed, speaker-tagged segments |
| `transcript.md` | the same transcript rendered for reading |
| `transcribe.log` | transcription progress/errors for this session |

Two tracks on purpose, same as the original: speech models do better on clean
single-source audio, and mic-vs-system is free two-party diarization — `me`
vs `them` with no speaker-identification model. The WAV headers are rewritten
every second while recording, so if the process dies mid-meeting everything
already written is still readable.

## Transcription

Local, automatic, via [whisper.cpp](https://github.com/ggml-org/whisper.cpp)
(`quill setup` installs the CPU build and `ggml-base.en.bin` into
`%LOCALAPPDATA%\quill`). Each track is transcribed separately, shifted by its
start offset so both share one clock, and merged by timestamp.

The filesystem is the queue: a session with `meta.json` but no
`transcript.json` is pending, and `quill transcribe` (no argument) catches up
on all of them — so it's fine to record with transcription unavailable, or to
re-run after a crash. Failures append to the session's `transcribe.log` and
never block later jobs.

Want a better model? Drop any `ggml-*.bin` into `%LOCALAPPDATA%\quill\models`
or point `QUILL_MODEL` at one (e.g. `ggml-small.en.bin` for more accuracy,
still fast on CPU).

## Config

Environment variables, all optional:

| Var | Meaning | Default |
|---|---|---|
| `QUILL_RECORDINGS_DIR` | where sessions are written | `%USERPROFILE%\Recordings` |
| `QUILL_WHISPER` | path to `whisper-cli.exe` | auto-detected from `%LOCALAPPDATA%\quill\bin`, then `PATH` |
| `QUILL_MODEL` | path to a ggml model | newest `ggml-*.bin` in `%LOCALAPPDATA%\quill\models` |

## Differences from the macOS original

- CLI instead of a menu-bar tray (a tray build is a natural next step).
- WAV (16kHz mono PCM, whisper's native format) instead of CAF/AAC.
- whisper.cpp instead of Parakeet/FluidAudio (which are Core ML, Apple-only).
- System audio via WASAPI loopback instead of Core Audio process taps.

## License

MIT, same as the original quill.
