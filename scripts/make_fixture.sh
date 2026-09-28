#!/usr/bin/env bash
#
# make_fixture.sh — generate a synthetic "changed filesystem" disk image.
#
# Simulates the FindIt headline scenario using only native macOS tooling:
#   1. create a raw disk image
#   2. format it FAT32 and populate it with media (the "previous" filesystem)
#   3. overlay a fresh exFAT filesystem on top (the "current" filesystem)
#
# The result: a .bin whose current filesystem is exFAT, but whose FAT32
# remnants and media file bytes still physically exist underneath — exactly
# what the diagnosis + carving pipeline must rediscover.
#
# No sudo required: hdiutil-attached images are owned by the invoking user.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SIZE_MB="${SIZE_MB:-256}"
NAME="${NAME:-changed_fs_fat32_to_exfat}"
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
go run "$ROOT/scripts/genmedia" -out "$STAGE/DCIM"    -prefix IMG -jpg 6 -png 4 -mp4 0
go run "$ROOT/scripts/genmedia" -out "$STAGE/Wedding" -prefix WED -jpg 2 -png 0 -mp4 3

echo "==> Recording manifest (sha256) -> $MANIFEST"
mkdir -p "$OUT_DIR"
( cd "$STAGE" && find . -type f -print0 | sort -z | xargs -0 shasum -a 256 ) > "$MANIFEST"

echo "==> Creating ${SIZE_MB}MB raw image -> $IMG"
rm -f "$IMG"
mkfile "${SIZE_MB}m" "$IMG"

echo "==> Attaching image (raw, no mount)"
DISK="$(hdiutil attach -nomount -imagekey diskimage-class=CRawDiskImage "$IMG" | awk '{print $1}' | head -n1)"
echo "    device: $DISK"

echo "==> Formatting FAT32 (the previous filesystem)"
newfs_msdos -F 32 -v OLDDATA "$DISK" >/dev/null

echo "==> Mounting and copying media in"
diskutil mount "$DISK" >/dev/null
VOL="$(diskutil info "$DISK" | awk -F': *' '/Mount Point/{print $2}')"
cp -R "$STAGE"/DCIM "$STAGE"/Wedding "$VOL"/
sync
diskutil unmount "$DISK" >/dev/null

echo "==> Overlaying exFAT (the current filesystem)"
newfs_exfat -v RESCUE "$DISK" >/dev/null

echo "==> Detaching"
hdiutil detach "$DISK" >/dev/null
DISK=""

echo
echo "==> Fixture ready: $IMG"
echo "    size:     $(du -h "$IMG" | awk '{print $1}')"
echo "    files:    $(wc -l < "$MANIFEST" | tr -d ' ') embedded (see $NAME.manifest.txt)"
echo
echo "==> Quick signature sanity check:"
printf "    current-FS label (should show EXFAT): "
if dd if="$IMG" bs=1 skip=3 count=8 2>/dev/null | grep -aq "EXFAT"; then echo "EXFAT ✓"; else echo "not found"; fi
JPGS=$(LC_ALL=C grep -a -c -o $'\xff\xd8\xff' "$IMG" 2>/dev/null || true)
printf "    JPEG (FFD8FF) signatures found underneath: %s\n" "${JPGS:-0}"
FTYP=$(LC_ALL=C grep -a -c -o 'ftyp' "$IMG" 2>/dev/null || true)
printf "    MP4  (ftyp)   signatures found underneath: %s\n" "${FTYP:-0}"
