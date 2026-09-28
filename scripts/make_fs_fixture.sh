#!/usr/bin/env bash
#
# make_fs_fixture.sh — generate an INTACT, populated filesystem image for
# testing FindIt's filesystem-enumeration adapter (The Sleuth Kit).
#
# Unlike make_fixture.sh (which overlays exFAT to simulate a changed filesystem
# for diagnosis + carving), this keeps a single healthy FAT32 filesystem with a
# real directory structure, and DELETES a few files so we can also exercise
# deleted-file recovery. TSK `fls -r` enumerates the tree (including deleted
# entries) and `icat` recovers file contents.
#
# Native macOS tooling only; no sudo (hdiutil-attached images are user-owned).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SIZE_MB="${SIZE_MB:-128}"
NAME="${NAME:-fat32_files}"
OUT_DIR="$ROOT/testdata/fixtures"
IMG="$OUT_DIR/$NAME.bin"
MANIFEST="$OUT_DIR/$NAME.manifest.txt"
STAGE="$(mktemp -d)"
DISK=""

cleanup() {
  [[ -n "$DISK" ]] && hdiutil detach "$DISK" >/dev/null 2>&1 || true
  rm -rf "$STAGE"
}
trap cleanup EXIT

echo "==> Staging sample media in $STAGE"
mkdir -p "$STAGE/DCIM" "$STAGE/Wedding"
go run "$ROOT/scripts/genmedia" -out "$STAGE/DCIM"    -prefix IMG -jpg 4 -png 2 -mp4 0
go run "$ROOT/scripts/genmedia" -out "$STAGE/Wedding" -prefix WED -jpg 1 -png 0 -mp4 2

echo "==> Creating ${SIZE_MB}MB raw image -> $IMG"
mkdir -p "$OUT_DIR"
rm -f "$IMG"
mkfile "${SIZE_MB}m" "$IMG"

echo "==> Attaching image (raw, no mount)"
DISK="$(hdiutil attach -nomount -imagekey diskimage-class=CRawDiskImage "$IMG" | awk '{print $1}' | head -n1)"
echo "    device: $DISK"

echo "==> Formatting FAT32"
newfs_msdos -F 32 -v FINDIT "$DISK" >/dev/null

echo "==> Mounting and copying media in"
diskutil mount "$DISK" >/dev/null
VOL="$(diskutil info "$DISK" | awk -F': *' '/Mount Point/{print $2}')"
cp -R "$STAGE"/DCIM "$STAGE"/Wedding "$VOL"/
sync

echo "==> Deleting a couple of files (to test deleted-file recovery)"
rm -f "$VOL/Wedding/WED_0002.mp4" "$VOL/DCIM/IMG_0004.jpg"
sync
diskutil unmount "$DISK" >/dev/null

echo "==> Detaching"
hdiutil detach "$DISK" >/dev/null
DISK=""

echo "==> Recording manifest (sha256 of the files that remain present)"
# Present files only (the two deleted ones are intentionally excluded).
rm -f "$STAGE/Wedding/WED_0002.mp4" "$STAGE/DCIM/IMG_0004.jpg"
( cd "$STAGE" && find . -type f -print0 | sort -z | xargs -0 shasum -a 256 ) > "$MANIFEST"

echo
echo "==> Fixture ready: $IMG ($(du -h "$IMG" | awk '{print $1}'))"
echo "    present files: $(wc -l < "$MANIFEST" | tr -d ' ') (see $NAME.manifest.txt); 2 files deleted"
echo
echo "==> fls -r preview:"
fls -r "$IMG" 2>/dev/null | head -30
