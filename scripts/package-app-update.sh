#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:?version tag required}
STAGE="$ROOT/dist/dmg-stage-universal"
[ -d "$STAGE/DJOneHubNotifier.app" ] || { echo 'Build universal DMG first' >&2; exit 1; }
TEMP=$(mktemp -d)
trap 'rm -rf "$TEMP"' EXIT
mkdir -p "$TEMP/DJOneHubUpdate"
# Dereference the libusb convenience symlink; updater deliberately rejects links.
cp -RL "$STAGE/djonehub" "$TEMP/DJOneHubUpdate/runtime"
cp -RL "$STAGE/DJOneHubNotifier.app" "$TEMP/DJOneHubUpdate/DJOneHubNotifier.app"
/usr/bin/ditto -c -k --norsrc --noextattr --keepParent "$TEMP/DJOneHubUpdate" "$ROOT/dist/DJOneHub-macOS-universal-update.zip"
(cd "$ROOT/dist" && shasum -a 256 DJOneHub-macOS-universal-update.zip > DJOneHub-macOS-universal-update.zip.sha256)
echo "Update asset built for $VERSION"
