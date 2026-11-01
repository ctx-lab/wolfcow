#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
./bin/wolfcow spike workspace --allow-tcp example.com:80 examples/stage-b -- /bin/sh -c '
  set -eu
  test "$HTTPS_PROXY" = http://192.168.127.1:80
  response=$({ /bin/busybox printf "CONNECT example.com:80 HTTP/1.1\r\nHost: example.com:80\r\n\r\n";
    /bin/busybox printf "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n";
    /bin/busybox sleep 6; } | /bin/busybox nc -w 8 192.168.127.1 80) || true
  case "$response" in
    *"200 Connection Established"*"HTTP/1."*) echo "external TCP request and response passed" ;;
    *) echo "$response"; exit 1 ;;
  esac
  expect_connect_denied() {
    target=$1
    name=$2
    response=$({ /bin/busybox printf "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n" "$target" "$target";
      /bin/busybox sleep 1; } | /bin/busybox nc -w 3 192.168.127.1 80) || true
    case "$response" in
      *"403 Forbidden"*) echo "$name denied" ;;
      *) echo "$response"; echo "$name was not denied" >&2; exit 1 ;;
    esac
  }
  expect_connect_denied blocked.example:80 "blocked hostname"
  expect_connect_denied example.com.evil:80 "hostname suffix"
  expect_connect_denied example.com:81 "other port"
  expect_connect_denied 127.0.0.1:80 "raw IP"
  if denial=$(/bin/busybox wget -Y off -T 3 -O- http://198.51.100.1:80/ 2>&1); then
    echo "direct raw IP bypassed policy" >&2
    exit 1
  fi
  case "$denial" in
    *"Connection refused"*) echo "direct raw IP denied" ;;
    *) echo "$denial"; echo "direct raw IP did not fail promptly" >&2; exit 1 ;;
  esac
'
echo 'external network probe passed'
