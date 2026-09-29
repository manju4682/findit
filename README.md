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

Requires macOS 12 or later on Apple Silicon.

1. Download **[`FindIt.dmg`](../../releases/latest/download/FindIt.dmg)** from the
   latest [release](../../releases/latest).
2. Open it and drag **FindIt** to Applications.
3. First launch: FindIt isn't notarized yet, so macOS blocks it once. Open
   **System Settings → Privacy & Security**, scroll to the message about
   FindIt, and click **Open Anyway**.

Everything is bundled — no Homebrew, no extra downloads.

> Windows and Intel Macs are not supported yet.

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

## Safety

- Your drive is only ever **read**. Copies and recovered files can't be saved
  onto the drive you're recovering from, and existing files are never
  overwritten.
- Reading a whole drive needs your administrator password. Only two tiny helper
  programs run with that access; the app itself never does. See
  [SECURITY.md](SECURITY.md) for details.
- FindIt works offline and never sends data anywhere.

## Build from source

Requires Go (see `go.mod`), Node 20.19+ or 22.12+, and the [Wails v2](https://wails.io) CLI.

```bash
brew install sleuthkit           # only needed for development (bundled in the .dmg)
go install github.com/wailsapp/wails/v2/cmd/wails@latest

wails dev                        # run in development
./scripts/build_dmg.sh           # produce a self-contained build/FindIt.dmg
```

To run the tests, build the frontend once (`go build` embeds it), then:

```bash
npm --prefix frontend ci && npm --prefix frontend run build
go test ./...
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for fixtures, project layout, and
guidelines. Recovery-engine binaries are vendored into the `.app` by
[`scripts/bundle_engines.sh`](scripts/bundle_engines.sh) so the shipped build has
no external dependencies.

## License

[Apache-2.0](LICENSE). Bundled engines (The Sleuth Kit and its libraries) keep
their own licenses and run as separate processes — see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
