#!/bin/bash
# Build the ZombieBuddy Linux Installer AppImage from source.
#
# Requirements:
#   - Go 1.25 or newer  (https://go.dev/dl/)
#   - curl (only needed once, to fetch appimagetool)
#   - nothing else - the build is static and needs no root rights
#
# Usage:
#   ./build.sh              -> ZombieBuddyInstaller-Linux-v4.2-x86_64.AppImage
#   OUT=my.AppImage ./build.sh
set -euo pipefail
cd "$(dirname "$0")"

VERSION=4.2
OUT="${OUT:-ZombieBuddyInstaller-Linux-v4.2-x86_64.AppImage}"

# --- 1. Go toolchain -------------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
    echo "error: Go toolchain not found on PATH." >&2
    echo "  Install Go 1.25+ from https://go.dev/dl/ and re-run." >&2
    exit 1
fi

# --- 2. Compile the installer (static, no cgo) -----------------------------
echo "[1/3] compiling zombiebuddy-linux-installer..."
( cd installer && CGO_ENABLED=0 go build -trimpath -o zombiebuddy-linux-installer . )

# --- 3. Assemble the AppDir ------------------------------------------------
echo "[2/3] assembling AppDir..."
rm -rf build/appdir
mkdir -p build/appdir/usr/bin \
         build/appdir/usr/share/icons/hicolor/128x128/apps \
         build/appdir/usr/share/icons/hicolor/256x256/apps
cp installer/zombiebuddy-linux-installer build/appdir/usr/bin/
cp appimage/AppRun build/appdir/AppRun
chmod +x build/appdir/AppRun
cp appimage/zombiebuddy-installer.desktop build/appdir/
cp appimage/icons/zombiebuddy-installer-128.png \
   build/appdir/usr/share/icons/hicolor/128x128/apps/zombiebuddy-installer.png
cp appimage/icons/zombiebuddy-installer-256.png \
   build/appdir/usr/share/icons/hicolor/256x256/apps/zombiebuddy-installer.png
cp appimage/icons/zombiebuddy-installer-256.png build/appdir/zombiebuddy-installer.png
ln -sf zombiebuddy-installer.png build/appdir/.DirIcon

# --- 4. Package with appimagetool ------------------------------------------
echo "[3/3] packaging AppImage..."
TOOL=build/appimagetool-x86_64.AppImage
if [ ! -x "$TOOL" ]; then
    echo "  downloading appimagetool (one time)..."
    curl -fSL -o "$TOOL" \
        https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage
    chmod +x "$TOOL"
fi

# APPIMAGE_EXTRACT_AND_RUN=1 lets appimagetool run without FUSE.
APPIMAGE_EXTRACT_AND_RUN=1 ARCH=x86_64 "$TOOL" build/appdir "$OUT"

echo
echo "built: $OUT"
echo "run it:  chmod +x \"$OUT\" && ./$OUT"
