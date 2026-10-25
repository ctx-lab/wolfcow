#!/bin/sh
# Exploratory host-side App Sandbox test. It never modifies the release helper.
set -eu
cd "$(dirname "$0")/.."

[ "$(uname -s)/$(uname -m)" = Darwin/arm64 ] || { echo 'Requires Apple Silicon macOS' >&2; exit 1; }
[ -f .cache/build/libkrun/include/libkrun.h ] || { echo 'Run task build first' >&2; exit 1; }
for file in bin/runtime/libkrun.1.dylib bin/runtime/libkrunfw.5.dylib; do
  [ -f "$file" ] || { echo 'Run task build first' >&2; exit 1; }
done

work=$(mktemp -d "${TMPDIR:-/tmp}/wolfcow-app-sandbox.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
cp bin/runtime/libkrun.1.dylib bin/runtime/libkrunfw.5.dylib "$work/"
make_info_plist() {
  name=$1
  executable=$2
  cat > "$work/$name.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleIdentifier</key><string>com.ctx-lab.wolfcow.spike.$name</string>
  <key>CFBundleExecutable</key><string>$executable</string>
  <key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
EOF
}
make_info_plist minimal minimal
printf 'int main(void) { return 0; }\n' > "$work/minimal.c"
clang "$work/minimal.c" -Wl,-sectcreate,__TEXT,__info_plist,"$work/minimal.plist" -o "$work/minimal"
codesign --force --sign "${WOLFCOW_SIGN_IDENTITY:--}" \
  --identifier com.ctx-lab.wolfcow.spike.minimal --options runtime \
  --entitlements hack/app-sandbox-only.entitlements.plist "$work/minimal"
set +e
"$work/minimal" 2>"$work/minimal.log"
minimal_status=$?
set -e
if [ "$minimal_status" -ne 0 ]; then
  echo "Minimal App Sandbox process failed before libkrun was involved (exit $minimal_status)" >&2
  cat "$work/minimal.log" >&2
  exit 1
fi
make_info_plist krun wolfcow-krun
clang -Wall -Wextra -Werror -O2 -DWOLFCOW_SECURITY_PROBE \
  -I.cache/build/libkrun/include native/shell.c \
  "$work/libkrun.1.dylib" -Wl,-rpath,@loader_path \
  -Wl,-sectcreate,__TEXT,__info_plist,"$work/krun.plist" -o "$work/wolfcow-krun"
codesign --force --sign "${WOLFCOW_SIGN_IDENTITY:--}" \
  --identifier com.ctx-lab.wolfcow.spike.krun --options runtime \
  --entitlements hack/app-sandbox-spike.entitlements.plist "$work/wolfcow-krun"

marker="$PWD/.cache/app-sandbox-probe-marker"
printf 'synthetic\n' > "$marker"
trap 'rm -rf "$work"; rm -f "$marker"' EXIT HUP INT TERM

set +e
"$work/wolfcow-krun" --probe-read /etc/hosts >/dev/null 2>"$work/system.log"
system_status=$?
"$work/wolfcow-krun" --probe-read "$marker" >/dev/null 2>"$work/private.log"
private_status=$?
set -e
if [ "$system_status" -ne 0 ]; then
  echo "Sandboxed helper did not launch or could not read a system fixture (exit $system_status)" >&2
  cat "$work/system.log" >&2
  exit 1
fi
if [ "$private_status" -ne 77 ]; then
  echo "Private file read was not denied as expected (exit $private_status)" >&2
  cat "$work/private.log" >&2
  exit 1
fi
echo 'App Sandbox file-access probe passed; VM boot remains untested' >&2
