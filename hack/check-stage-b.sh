#!/bin/sh
set -eu

work=$(mktemp -d "${TMPDIR:-/tmp}/wolfcow-stage-b.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
cp examples/stage-b/note.txt "$work/note.txt"

result=$(./bin/wolfcow spike workspace "$work" -- /bin/sh -c 'printf "guest changed this file\n" > note.txt; printf "new file\n" > new.txt')
printf '%s\n' "$result"

case "$result" in *"created host-note.txt"*) ;; *) echo "missing host write" >&2; exit 1 ;; esac
case "$result" in *"created new.txt"*) ;; *) echo "missing guest creation" >&2; exit 1 ;; esac
case "$result" in *"modified note.txt"*) ;; *) echo "missing guest modification" >&2; exit 1 ;; esac
case "$result" in *"--- a/note.txt"*"+++ b/note.txt"*"+guest changed this file"*) ;; *) echo "missing unified diff" >&2; exit 1 ;; esac
cmp examples/stage-b/note.txt "$work/note.txt"
test ! -e "$work/new.txt"
test ! -e "$work/host-note.txt"
echo "Stage B copied-workspace smoke checks passed"
