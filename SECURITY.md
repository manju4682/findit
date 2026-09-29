# Security policy

## Reporting a vulnerability

Please **do not** open a public issue for security problems. Use GitHub's
[private vulnerability reporting](../../security/advisories/new) for this
repository instead. Include the FindIt version, macOS version, and steps to
reproduce. We aim to acknowledge reports within a week.

## Security model

FindIt reads whole drives, which on macOS requires administrator rights. To keep
the privileged surface small:

- **The app itself never runs as root.** Two small helpers do, and only after
  you approve the native macOS password prompt:
  - `findit-imagecopy` copies a drive to an image file you chose. It opens the
    drive read-only, refuses to write onto the drive being copied, won't replace
    a file you don't own, and hands the finished image to you with mode `0600`.
  - `findit-devopen` (used by "scan the drive directly") opens the drive
    read-only, passes the open descriptor back to the app over a per-user unix
    socket, and runs only `fls`/`icat` against that drive on request. Requests
    are structured and validated (`internal/engines/tsk.Args`); no command line
    from the app is executed. It exits when the app closes the session.
- Helper arguments are passed through AppleScript `quoted form of`, so no path
  or drive name can alter the command that runs as root.
- **Sources are never written.** All reads go through `internal/storage`, and
  recovered files can't be saved onto the drive being recovered.
- **Recovered data is untrusted input.** File names are sanitized so they can't
  escape the destination folder, existing files are never overwritten, and
  images are size-checked before decoding.
- FindIt makes no network connections.

## Known limitations

- The helpers are launched from inside the app bundle via `osascript`, and the
  password prompt names `osascript`, not FindIt. Because a drag-installed app
  is owned by your user account, other software running as you could swap a
  helper before you approve the prompt. The planned fix is Developer ID signing
  with a helper registered through `SMAppService`, which macOS verifies before
  granting privileges.
