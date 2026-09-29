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
PKG_DIRS="" # newline-separated Homebrew keg dirs we vendored from (bash 3.2: no assoc arrays)

# note_pkg records the Homebrew keg (…/Cellar/<pkg>/<version>) a file came from,
# so its license files can be shipped alongside it.
note_pkg() {
  local real dir
  real="$(realpath "$1" 2>/dev/null || echo "$1")"
  case "$real" in */Cellar/*/*/*) ;; *) return 0 ;; esac
  dir="$(echo "$real" | sed -E 's#(.*/Cellar/[^/]+/[^/]+)/.*#\1#')"
  case $'\n'"$PKG_DIRS" in *$'\n'"$dir"$'\n'*) return 0 ;; esac
  PKG_DIRS+="$dir"$'\n'
}

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
      note_pkg "$dep"
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
  note_pkg "$src"
done

echo "==> Vendoring dylibs and rewriting load paths"
i=0
while ((i < ${#QUEUE[@]})); do
  f="${QUEUE[$i]}"
  i=$((i + 1))
  rewrite "$f"
done

echo "==> Collecting third-party license files"
mkdir -p "$DEST/licenses"
while read -r dir; do
  [[ -z "$dir" ]] && continue
  pkg="$(basename "$(dirname "$dir")")-$(basename "$dir")"
  mkdir -p "$DEST/licenses/$pkg"
  find "$dir" -maxdepth 1 -type f \( -iname 'LICEN[CS]E*' -o -iname 'COPYING*' -o -iname 'NOTICE*' -o -name 'sbom.spdx.json' \) \
    -exec cp {} "$DEST/licenses/$pkg/" \;
  echo "  $pkg"
done <<< "$PKG_DIRS"
cp "$(dirname "$0")/../THIRD_PARTY_NOTICES.md" "$DEST/licenses/"

echo "==> Ad-hoc signing (required on Apple Silicon after rewriting)"
find "$DEST/bin" "$DEST/lib" -type f -exec codesign --force --sign - {} \;

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
