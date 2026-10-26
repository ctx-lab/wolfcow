#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
./bin/wolfcow spike workspace --allow-tcp example.com:80 examples/stage-b -- /bin/sh -c '
  set -eu
  test "$HTTPS_PROXY" = http://192.168.127.1:80
  response=$({ /bin/busybox printf "CONNECT example.com:80 HTTP/1.1\r\nHost: example.com:80\r\n\r\n";
    /bin/busybox sleep 6; } | /bin/busybox nc -w 8 192.168.127.1 80) || true
  case "$response" in
    *"200 Connection Established"*) echo "external TCP tunnel established" ;;
    *) echo "$response"; exit 1 ;;
  esac
'
