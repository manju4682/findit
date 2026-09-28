#!/usr/bin/env bash
#
# bundle_engines.sh — produce a self-contained copy of the recovery engine
# binaries (The Sleuth Kit) with all non-system dylibs vendored and their load
# paths rewritten to @loader_path, so they run on a machine that has no
# Homebrew and no engines installed.
#
# Output layout:
#   <dest>/bin/{fls,icat,fsstat,mmls}
#   <dest>/lib/*.dylib
#
# Usage: scripts/bundle_engines.sh [dest_dir]   (default: build/engines)

set -euo pipefail

DEST="${1:-build/engines}"
BINS=(fls icat fsstat mmls)

rm -rf "$DEST"
mkdir -p "$DEST/bin" "$DEST/lib"

is_system() { case "$1" in /usr/lib/*|/System/*) return 0 ;; *) return 1 ;; esac; }

declare -a QUEUE=()

# rewrite fixes one Mach-O file's dylib references to point at vendored libs,
# copying and queueing any non-system dependency it discovers.
rewrite() {
  local file="$1" dir prefix dep base
  dir="$(cd "$(dirname "$file")" && pwd)"
  if [[ "$dir" == */bin ]]; then prefix="@loader_path/../lib"; else prefix="@loader_path"; fi

  if [[ "$dir" == */lib ]]; then
    install_name_tool -id "@loader_path/${file##*/}" "$file" 2>/dev/null || true
  fi

  while read -r dep; do
    [[ -z "$dep" ]] && continue
    case "$dep" in @*) continue ;; esac
    is_system "$dep" && continue
    [[ -f "$dep" ]] || { echo "  warn: cannot resolve $dep" >&2; continue; }
    base="${dep##*/}"
    if [[ ! -f "$DEST/lib/$base" ]]; then
      cp -L "$dep" "$DEST/lib/$base"
      chmod u+w "$DEST/lib/$base"
      QUEUE+=("$DEST/lib/$base")
    fi
    install_name_tool -change "$dep" "$prefix/$base" "$file" 2>/dev/null || true
  done < <(otool -L "$file" | tail -n +2 | awk '{print $1}')
}

echo "==> Copying engine binaries"
for b in "${BINS[@]}"; do
  src="$(command -v "$b")" || { echo "missing $b — brew install sleuthkit"; exit 1; }
  cp "$src" "$DEST/bin/$b"
  chmod u+w "$DEST/bin/$b"
  QUEUE+=("$DEST/bin/$b")
done

echo "==> Vendoring dylibs and rewriting load paths"
i=0
while ((i < ${#QUEUE[@]})); do
  f="${QUEUE[$i]}"
  i=$((i + 1))
  rewrite "$f"
done

echo "==> Ad-hoc signing (required on Apple Silicon after rewriting)"
find "$DEST/bin" "$DEST/lib" -type f -exec codesign --force --sign - {} \; 2>/dev/null || true

echo "==> Verifying self-containment (no /opt or Homebrew paths should remain)"
leak=0
while read -r f; do
  if otool -L "$f" | tail -n +2 | awk '{print $1}' | grep -qE '^/opt/|/homebrew/'; then
    echo "  LEAK: $f still references external dylibs"
    otool -L "$f" | grep -E '/opt/|/homebrew/' || true
    leak=1
  fi
done < <(find "$DEST/bin" "$DEST/lib" -type f)

echo
echo "==> Bundled $(ls "$DEST/lib" | wc -l | tr -d ' ') dylib(s) into $DEST"
"$DEST/bin/fls" -V 2>/dev/null && echo "==> Bundled fls runs OK"
[[ "$leak" == 0 ]] && echo "==> Self-contained ✓" || { echo "==> Still leaking ✗"; exit 1; }
