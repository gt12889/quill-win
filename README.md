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
| `mic.flac` | your side (default mic, 48kHz mono, lossless) |
| `system.flac` | everything the PC played — the other side of the call |
| `meta.json` | start/end timestamps, duration, per-track start offsets |
| `transcript.json` | canonical transcript — engine + timed, speaker-tagged segments |
| `transcript.md` | the same transcript rendered for reading |
| `transcribe.log` | transcription progress/errors for this session |

Two tracks on purpose, same as the original: speech models do better on clean
single-source audio, and mic-vs-system is free two-party diarization — `me`
vs `them` with no speaker-identification model. FLAC on purpose: lossless at
48kHz, roughly half the size of WAV for speech and near-nothing for the
silence that dominates the system track, and every frame is independent — if
the process dies mid-meeting, everything already flushed is still playable.

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

Want a better model? `quill setup --model small.en` (or `large-v3-turbo`,
or a multilingual one like `small`), then select it in the config below.
Non-English meetings: use a multilingual model and set `language` to your
language code, or `"auto"`.

## Config

Optional, at `%APPDATA%\quill\config.json` — same shape as the original:

```json
{
  "recordings_dir": "D:\\Recordings",
  "transcription": { "enabled": true, "model": "small.en", "language": "auto" },
  "on_stop": "my-hook.cmd"
}
```

`on_stop` runs after each session finishes (transcribed or not) with the
session folder as its argument — handy for syncing or indexing.

Environment variables win over the file: `QUILL_RECORDINGS_DIR`,
`QUILL_WHISPER` (path to `whisper-cli.exe`), `QUILL_MODEL` (path to a ggml
model), `QUILL_LANG`.

## Differences from the macOS original

- CLI instead of a menu-bar tray (a tray build is a natural next step).
- FLAC (48kHz mono, lossless) instead of CAF/AAC.
- whisper.cpp instead of Parakeet/FluidAudio (which are Core ML, Apple-only).
- System audio via WASAPI loopback instead of Core Audio process taps.

## License

MIT, same as the original quill.
