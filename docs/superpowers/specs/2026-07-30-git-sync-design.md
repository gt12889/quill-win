# Git sync for finished transcripts — design

2026-07-30. Approved approach: built-in sync (option A), over an on_stop hook
script or a full Obsidian exporter.

## Purpose

After each meeting finishes transcribing, quill files the transcript into a
notes folder and commits/pushes it to a git repo — hands-off from the tray.
The note format is Obsidian-compatible so the same folder can later be opened
as a vault without rework.

## Configuration

New optional block in `%APPDATA%\quill\config.json`:

```json
{
  "sync": { "dir": "C:\\Users\\<user>\\meeting-notes", "push": true }
}
```

- Absent block: feature off, no behavior change.
- `dir` (required to enable): destination folder; must be inside a git work
  tree for commit/push to happen (a plain folder still receives note files).
- `push` (default true): `false` commits locally without pushing.

## Behavior

In `FinishSession`, after transcription succeeds and before the `on_stop`
hook:

1. Render the note to `<dir>\<session-name>.md` (e.g. `2026.07.30-1435.md`):
   YAML frontmatter — `date`, `started` (RFC3339), `duration_minutes`,
   `devices` (mic/system), `source: quill` — then the transcript body using
   the existing speaker-tagged markdown rendering.
2. If `dir` is inside a git work tree: `git add <file>`,
   `git commit -m "meeting: <session-name>"`, and if `push` is true,
   `git push` — all executed with `dir` as the working directory, using the
   system `git` from PATH.

Sessions that finish without a transcript (`--no-transcribe`, engine not
installed) are not synced; when they are later transcribed by
`quill transcribe` or the tray's pending-resume, sync runs then. Audio files
never sync — transcripts and metadata only.

## Error handling

- Sync failure (git missing, offline, rejected push, bad dir) prints to
  stderr and appends to the session's `transcribe.log`; the session itself
  still succeeds. Local recording and transcript always win.
- No retry queue. The next successful sync pushes earlier unpushed commits
  (git's natural behavior); re-running `quill transcribe` on a synced session
  is idempotent — same filename, and committing an unchanged file is a no-op.
- `doctor` gains a sync check when sync is configured: dir exists, `git` on
  PATH, dir is a git work tree, a remote is configured.

## Components

- `internal/quill/sync.go`: note rendering + git plumbing
  (`syncSession(dir string) error`, pure `renderNote(...)` helper).
- `FinishSession` in `session.go`: one call added.
- `doctor.go`: sync checks.
- `config.go`: `Sync` struct fields.

## Testing

- Unit: `renderNote` frontmatter + body (runs via the existing
  Windows-compiled test binary).
- E2E on the target machine: configure sync at a fresh private repo, record a
  short TTS session, verify the note lands on the remote; then a failure-path
  run (invalid remote) proving the session completes and the error is logged.
