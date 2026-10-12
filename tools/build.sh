#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
NATIVE="$ROOT/native"
NDK=${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}
SDK=${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}

if [ -z "$NDK" ]; then
  for candidate in "$HOME"/Library/Android/sdk/ndk/* "$HOME"/Android/Sdk/ndk/*; do
    [ -d "$candidate" ] && NDK=$candidate
  done
fi
if [ -z "$SDK" ]; then
  for candidate in "$HOME/Library/Android/sdk" "$HOME/Android/Sdk"; do
    [ -d "$candidate/build-tools" ] && SDK=$candidate
  done
fi
[ -n "$NDK" ] && [ -d "$NDK/toolchains/llvm" ] || {
  echo "Set ANDROID_NDK_HOME to an installed Android NDK" >&2
  exit 1
}
[ -n "$SDK" ] && [ -d "$SDK/build-tools" ] || {
  echo "Set ANDROID_SDK_ROOT to an installed Android SDK" >&2
  exit 1
}
command -v adb >/dev/null 2>&1 || { echo "adb is required" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "Go is required" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) HOST_TAG=${ANDROID_NDK_HOST_TAG:-darwin-x86_64} ;;
  Linux) HOST_TAG=${ANDROID_NDK_HOST_TAG:-linux-x86_64} ;;
  *) echo "Unsupported build host: $(uname -s)" >&2; exit 1 ;;
esac

TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/$HOST_TAG/bin"
AIDL=
for candidate in "$SDK"/build-tools/*/aidl; do
  [ -x "$candidate" ] && AIDL=$candidate
done
[ -x "$TOOLCHAIN/aarch64-linux-android32-clang++" ] || {
  echo "Android NDK API 32 toolchain not found: $TOOLCHAIN" >&2
  exit 1
}
[ -n "$AIDL" ] || { echo "Android SDK build-tools aidl is required" >&2; exit 1; }

API=$(adb shell getprop ro.build.version.sdk | tr -d '\r')
ABI=$(adb shell getprop ro.product.cpu.abi | tr -d '\r')
if [ "$API" != 32 ] || [ "$ABI" != arm64-v8a ]; then
  echo "The first single-binary target requires a connected Android 12 (API 32) arm64 device" >&2
  exit 1
fi

BUILD="$NATIVE/_build"
mkdir -p "$BUILD" "$NATIVE/libs" "$NATIVE/generated/include" "$NATIVE/generated/src" "$BUILD/deps"

fetch_archive() {
  url=$1
  destination=$2
  if [ -d "$destination" ] && [ -n "$(find "$destination" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
    return
  fi
  archive="$BUILD/$(basename "$destination").tar.gz"
  mkdir -p "$destination"
  curl -fL --retry 2 --silent --show-error "$url" -o "$archive"
  tar -xzf "$archive" -C "$destination"
}

AOSP="$BUILD/aosp"
FRAMEWORKS="$AOSP/frameworks-native"
BASE="$AOSP/libbase"
UTILS="$AOSP/libutils"
LOG="$AOSP/liblog"
SYSTEM="$AOSP/libsystem"
CUTILS="$AOSP/libcutils"
HARDWARE="$AOSP/libhardware"

fetch_archive "https://android.googlesource.com/platform/frameworks/native/+archive/android12-release.tar.gz" "$FRAMEWORKS"
fetch_archive "https://android.googlesource.com/platform/system/libbase/+archive/android12-release.tar.gz" "$BASE"
fetch_archive "https://android.googlesource.com/platform/system/core/+archive/android12-release/libutils.tar.gz" "$UTILS"
fetch_archive "https://android.googlesource.com/platform/system/logging/+archive/android12-release/liblog.tar.gz" "$LOG"
fetch_archive "https://android.googlesource.com/platform/system/core/+archive/android12-release/libsystem.tar.gz" "$SYSTEM"
fetch_archive "https://android.googlesource.com/platform/system/core/+archive/android12-release/libcutils.tar.gz" "$CUTILS"
fetch_archive "https://android.googlesource.com/platform/hardware/libhardware/+archive/android12-release.tar.gz" "$HARDWARE"

WEBP_VERSION=1.5.0
WEBP_SHA256=7d6fab70cf844bf6769077bd5d7a74893f8ffd4dfb42861745750c63c2a5c92c
WEBP_SOURCE="$BUILD/deps/libwebp"
WEBP_ARCHIVE="$BUILD/libwebp-$WEBP_VERSION.tar.gz"
if [ ! -d "$WEBP_SOURCE/src" ]; then
  curl -fL --retry 2 --silent --show-error \
    "https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-$WEBP_VERSION.tar.gz" \
    -o "$WEBP_ARCHIVE"
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$WEBP_ARCHIVE" | awk '{print $1}')
  else
    ACTUAL=$(shasum -a 256 "$WEBP_ARCHIVE" | awk '{print $1}')
  fi
  [ "$ACTUAL" = "$WEBP_SHA256" ] || { echo "libwebp checksum mismatch" >&2; exit 1; }
  mkdir -p "$WEBP_SOURCE"
  tar -xzf "$WEBP_ARCHIVE" -C "$WEBP_SOURCE" --strip-components=1
fi

"$AIDL" --lang=cpp \
  -I"$FRAMEWORKS/libs/gui/aidl" \
  --out="$NATIVE/generated/src" --header_out="$NATIVE/generated/include" \
  "$FRAMEWORKS/libs/gui/aidl/android/gui/ScreenCaptureResults.aidl" \
  "$FRAMEWORKS/libs/gui/aidl/android/gui/IScreenCaptureListener.aidl"

WEBP_BUILD="$BUILD/deps/webp-build"
if [ ! -f "$WEBP_BUILD/src/.libs/libwebp.a" ]; then
  mkdir -p "$WEBP_BUILD"
  (
    cd "$WEBP_BUILD"
    CC="$TOOLCHAIN/aarch64-linux-android21-clang" \
      AR="$TOOLCHAIN/llvm-ar" RANLIB="$TOOLCHAIN/llvm-ranlib" \
      CFLAGS="-O2 -g0 -ffunction-sections -fdata-sections" \
      "$WEBP_SOURCE/configure" --host=aarch64-linux-android \
        --disable-shared --enable-static --disable-png --disable-jpeg \
        --disable-threading --disable-libwebpmux --disable-libwebpdemux >/dev/null
    make -j"${JOBS:-2}" -C sharpyuv >/dev/null
    make -j"${JOBS:-2}" -C src >/dev/null
  )
fi

for library in libgui.so libbinder.so libui.so libutils.so libbase.so libsync.so liblog.so libcutils.so; do
  [ -f "$NATIVE/libs/$library" ] || adb pull "/system/lib64/$library" "$NATIVE/libs/$library" >/dev/null
done

INCLUDE_FLAGS="-I$ROOT/tools/screenshot-native/include -I$NATIVE -I$NATIVE/include -I$NATIVE/generated/include -I$WEBP_SOURCE/src -I$BASE/include -I$UTILS/include -I$LOG/include -I$SYSTEM/include -I$CUTILS/include -I$HARDWARE/include"
find "$FRAMEWORKS" -type d -name include -print > "$BUILD/include-dirs.txt"
while IFS= read -r include_dir; do
  INCLUDE_FLAGS="$INCLUDE_FLAGS -I$include_dir"
done < "$BUILD/include-dirs.txt"

export GOOS=android GOARCH=arm64 CGO_ENABLED=1
export CC="$TOOLCHAIN/aarch64-linux-android32-clang"
export CXX="$TOOLCHAIN/aarch64-linux-android32-clang++"
export CGO_CFLAGS="-I$NATIVE -I$NATIVE/generated/include -I$WEBP_SOURCE/src"
export CGO_CXXFLAGS="-std=c++17 -fno-rtti -DLIBBINDER_EXPORTED= $INCLUDE_FLAGS"
export CGO_LDFLAGS="-L$NATIVE/libs -Wl,-rpath-link,$NATIVE/libs -L$WEBP_BUILD/src/.libs -L$WEBP_BUILD/sharpyuv/.libs -Wl,--no-as-needed -lgui -lbinder -lui -lutils -lbase -lcutils -lsync -llog -lmediandk -lEGL -lGLESv2 -landroid -lwebp -lsharpyuv -lc++_static -lm"

mkdir -p "$ROOT/bin"
(
  cd "$ROOT"
  go build -trimpath -ldflags='-s -w -linkmode=external -extldflags=-static-libstdc++' \
    -o "$ROOT/bin/screenbridge" ./app
)
chmod 755 "$ROOT/bin/screenbridge"

mkdir -p "$ROOT/dist"
PACKAGE="$ROOT/dist/ScreenBridge-v1.0.1.zip"
rm -f "$PACKAGE"
(
  cd "$ROOT/module"
  zip -r -X "$PACKAGE" module.prop customize.sh service.sh uninstall.sh \
    skip_mount config.conf.example
)
(
  cd "$ROOT"
  zip -X "$PACKAGE" README.md bin/screenbridge
)
echo "Built the single Go+NDK executable and package: $PACKAGE"
