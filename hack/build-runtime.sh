#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
ROOT=$(pwd)
. ./hack/runtime.env

if [ "$(uname -s)/$(uname -m)" != Darwin/arm64 ]; then
  echo 'wolfcow v0.0.1 runtime builds require Apple Silicon macOS' >&2
  exit 1
fi
for tool in go cargo rustc clang codesign install_name_tool curl shasum tar make cmp patch; do
  command -v "$tool" >/dev/null || { echo "Missing build tool: $tool" >&2; exit 1; }
done

mkdir -p .cache/downloads .cache/build .cache/tools bin/runtime
fetch() {
  url=$1 expected=$2 dest=$3
  if [ ! -f "$dest" ]; then
    echo "Fetching $(basename "$dest")" >&2
    partial="$dest.partial.$$"
    trap 'rm -f "$partial"' EXIT HUP INT TERM
    curl --fail --location --silent --show-error --retry 2 \
      --connect-timeout 15 --max-time 180 "$url" -o "$partial"
    actual=$(shasum -a 256 "$partial" | cut -d ' ' -f 1)
    [ "$actual" = "$expected" ] || { echo "Checksum mismatch: $url" >&2; exit 1; }
    mv "$partial" "$dest"
    trap - EXIT HUP INT TERM
  fi
  actual=$(shasum -a 256 "$dest" | cut -d ' ' -f 1)
  [ "$actual" = "$expected" ] || { echo "Checksum mismatch: remove $dest and retry" >&2; exit 1; }
}

fetch "$KRUN_URL" "$KRUN_SHA256" .cache/downloads/libkrun.tgz
fetch "$FIRMWARE_URL" "$FIRMWARE_SHA256" .cache/downloads/libkrunfw.tgz
fetch "$ZIG_URL" "$ZIG_SHA256" .cache/downloads/zig.tar.xz
fetch "$IMAGE_URL" "$IMAGE_SHA256" .cache/downloads/rootfs.tar.gz
fetch "$GVPROXY_URL" "$GVPROXY_SHA256" .cache/downloads/gvproxy-source.tgz
fetch "$GVPROXY_GO_URL" "$GVPROXY_GO_SHA256" .cache/downloads/go1.25.0.darwin-arm64.tar.gz

if [ ! -d .cache/build/libkrun ]; then
  mkdir -p .cache/build/libkrun
  tar -xzf .cache/downloads/libkrun.tgz -C .cache/build/libkrun --strip-components=1
fi
if [ ! -d .cache/build/libkrunfw/libkrunfw ]; then
  mkdir -p .cache/build/libkrunfw
  tar -xzf .cache/downloads/libkrunfw.tgz -C .cache/build/libkrunfw
fi
if [ ! -d .cache/tools/zig-aarch64-macos-0.14.1 ]; then
  tar -xJf .cache/downloads/zig.tar.xz -C .cache/tools
fi
if [ ! -x .cache/tools/go1.25.0/bin/go ]; then
  mkdir -p .cache/tools/go1.25.0
  tar -xzf .cache/downloads/go1.25.0.darwin-arm64.tar.gz -C .cache/tools/go1.25.0 --strip-components=1
fi

# Recreate the source before each patch so stale build state cannot hide drift.
rm -rf .cache/build/gvproxy-source
mkdir -p .cache/build/gvproxy-source
tar -xzf .cache/downloads/gvproxy-source.tgz -C .cache/build/gvproxy-source --strip-components=1
patch -d .cache/build/gvproxy-source -p1 < hack/patches/gvproxy-deny-egress.patch
(
  cd .cache/build/gvproxy-source
  GOTOOLCHAIN=local GOCACHE="$ROOT/.cache/go-build" GOMODCACHE="$ROOT/.cache/go-mod" \
    "$ROOT/.cache/tools/go1.25.0/bin/go" test -mod=vendor ./pkg/services/dns \
    -run '^TestWolfcowUnknownNameDeniedWithoutHostResolver$' -count=1
  GOTOOLCHAIN=local GOCACHE="$ROOT/.cache/go-build" GOMODCACHE="$ROOT/.cache/go-mod" \
    "$ROOT/.cache/tools/go1.25.0/bin/go" test -mod=vendor ./cmd/gvproxy \
    -run '^TestWolfcow' -count=1
  GOTOOLCHAIN=local GOCACHE="$ROOT/.cache/go-build" GOMODCACHE="$ROOT/.cache/go-mod" \
    "$ROOT/.cache/tools/go1.25.0/bin/go" build -mod=vendor -trimpath \
    -o "$ROOT/bin/runtime/gvproxy" ./cmd/gvproxy
)

export ZIG_GLOBAL_CACHE_DIR="$ROOT/.cache/zig"
.cache/tools/zig-aarch64-macos-0.14.1/zig cc -target aarch64-linux-musl -O2 -static \
  -o .cache/build/init .cache/build/libkrun/src/init_blob/init/init.c \
  .cache/build/libkrun/src/init_blob/init/dhcp.c
export KRUN_INIT_BINARY_PATH="$ROOT/.cache/build/init"
cargo build --manifest-path .cache/build/libkrun/Cargo.toml --release --locked -p libkrun --features net
make -C .cache/build/libkrunfw/libkrunfw
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w' \
  -o bin/runtime/wolfcow-guest ./cmd/wolfcow-guest

cp .cache/build/libkrun/target/release/libkrun.dylib bin/runtime/libkrun.1.dylib
cp .cache/build/libkrunfw/libkrunfw/libkrunfw.5.dylib bin/runtime/
cp .cache/downloads/rootfs.tar.gz bin/runtime/
GOCACHE="$ROOT/.cache/go-build" go build -o .cache/tools/image-manifest ./hack/image-manifest
.cache/tools/image-manifest -image bin/runtime/rootfs.tar.gz \
  -source "$IMAGE_URL" -version "$IMAGE_VERSION" > .cache/build/rootfs.manifest.json
if ! cmp -s images/alpine-minirootfs-3.22.2-aarch64.json .cache/build/rootfs.manifest.json; then
  echo 'Linux image inventory differs from the checked-in manifest' >&2
  exit 1
fi
cp .cache/build/rootfs.manifest.json bin/runtime/rootfs.manifest.json
install_name_tool -id @rpath/libkrun.1.dylib bin/runtime/libkrun.1.dylib
install_name_tool -id @rpath/libkrunfw.5.dylib bin/runtime/libkrunfw.5.dylib
clang -Wall -Wextra -Werror -O2 -I.cache/build/libkrun/include \
  native/shell.c bin/runtime/libkrun.1.dylib -Wl,-rpath,@loader_path -o bin/runtime/wolfcow-krun
codesign --force --sign - bin/runtime/libkrun.1.dylib
codesign --force --sign - bin/runtime/libkrunfw.5.dylib
codesign --force --sign - bin/runtime/gvproxy
codesign --force --sign - --entitlements native/entitlements.plist bin/runtime/wolfcow-krun

mkdir -p bin/runtime/licenses
cp .cache/build/libkrun/LICENSE bin/runtime/licenses/libkrun-APACHE-2.0
cp .cache/build/libkrunfw/libkrunfw/LICENSE-* bin/runtime/licenses/
cp LICENSE bin/runtime/licenses/gvproxy-APACHE-2.0
cp THIRD_PARTY.md bin/runtime/
cp LICENSE bin/runtime/
echo 'macOS runtime ready' >&2
