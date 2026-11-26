#!/bin/sh
# isotty-opeco-session status TEXT | notify TEXT | close
#
# Acts on the opeco session the VM keeps in __SESSION_DIR__, when there is
# one; with none, nobody paired, and there is nothing to tell. Run as the
# VM user who owns the directory. A status that cannot be sent, the session
# having expired among the reasons, is recorded in the VM event log and
# does not fail the caller: it runs as the VM starts or stops, which a lost
# status must not hold up.
set -u
dir="__SESSION_DIR__"
command="${1:-}"
[ $# -gt 0 ] && shift
for state in "$dir"/opeco-session-*/state.json; do
  [ -e "$state" ] || exit 0
done
case "$command" in
  status) output="$(opeco status "$*" 2>&1)" ;;
  notify) output="$(opeco notify "$*" 2>&1)" ;;
  close) output="$(opeco close 2>&1)" ;;
  *) echo "usage: isotty-opeco-session status TEXT | notify TEXT | close" >&2; exit 2 ;;
esac
status=$?
if [ "$status" -ne 0 ]; then
  echo "opeco $command failed: $output" >&2
  sudo python3 -c 'import datetime, json, sys; entry = {"time": datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"), "type": "opeco-" + sys.argv[2], "result": sys.argv[3]}; handle = open(sys.argv[1], "a", encoding="utf-8"); handle.write(json.dumps(entry, separators=(",", ":")) + "\n"); handle.close()' \
    "__EVENT_LOG__" "$command" "$output"
fi
exit 0
