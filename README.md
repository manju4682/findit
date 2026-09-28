<div align="center">
  <img src="frontend/src/assets/appicon.png" width="120" alt="FindIt" />
  <h1>FindIt</h1>
  <p><b>Recover your files — a friendly layer over proven recovery engines.</b></p>
</div>

FindIt helps ordinary people get their files back from a USB stick or SD card that
suddenly looks **empty**, was **reformatted**, had its **filesystem changed**
(e.g. NTFS → exFAT), or lost files to **deletion or corruption** — without needing
to understand partitions, MFTs, sectors, or carving.

It sits *above* mature recovery engines (The Sleuth Kit for filesystems, plus a
built-in carver) and adds the missing layer: **automatic diagnosis, a safe
guided workflow, previews, and honest per-file assessment.**

## Install

1. Download **`FindIt.dmg`** from the [Releases](../../releases) page.
2. Open it and drag **FindIt** to Applications.
3. First launch: right-click → **Open** (the app is signed but not yet
   notarized, so Gatekeeper asks once).

Everything is bundled — no Homebrew, no extra downloads.

> macOS (Apple Silicon) for now. Windows is a planned port.

## How it works

```
pick a drive  →  make a safe copy (clone)  →  scan  →  preview  →  recover
```

- **Image-first & read-only.** By default FindIt clones the drive to a `.bin`
  image and works only on the copy. It **never writes to your disk** — reading a
  device uses the native macOS permission prompt.
- **Diagnosis.** Detects the current filesystem and finds evidence of previous
  ones underneath ("your old files appear to still be here").
- **Per-source results.** Each filesystem and the raw scan are shown separately —
  a Finder-style folder tree for filesystems, a flat list for raw finds — with
  clear **Recoverable / Limited** status on every file.
- **Configurable raw recovery.** Choose exactly which file types to carve.

## Build from source

Requires Go 1.23+, Node, and the [Wails](https://wails.io) CLI.

```bash
brew install sleuthkit           # only needed for `wails dev` (bundled in the .dmg)
go install github.com/wailsapp/wails/v2/cmd/wails@latest

wails dev                        # run in development
./scripts/build_dmg.sh           # produce a self-contained build/FindIt.dmg
go test ./...                    # run the test suite
```

Recovery-engine binaries are vendored into the `.app` by
[`scripts/bundle_engines.sh`](scripts/bundle_engines.sh) so the shipped build has
no external dependencies.

## License

Apache-2.0. Bundled engines (The Sleuth Kit) retain their own licenses and are
invoked as separate processes.
