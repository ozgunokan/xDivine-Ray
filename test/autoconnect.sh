#!/bin/sh
# The startup connect, against a real daemon that really cannot connect.
#
# The unit test covers the loop; this covers the wiring around it, which is
# where the original bug lived. The loop was never the thing that failed — what
# failed was that Restore called it once and went home, and no unit test of a
# loop would have noticed, because there was no loop.
#
# So: a daemon with auto_connect on, an active server that cannot be reached,
# and a stopwatch. It has to keep trying. Then the same daemon with the box off,
# which must not dial at all. Then a SIGTERM in the middle of a backoff, which
# has to be prompt — a service stop that waits out a five-minute sleep is its
# own outage.
#
#   sh test/autoconnect.sh
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
API=18790

fail=0
ok()  { echo "ok   $*"; }
bad() { echo "FAIL $*" >&2; fail=1; }

cleanup() {
	[ -f "$WORK/pid" ] && kill "$(cat "$WORK/pid")" 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

busy() {
	python3 -c "
import socket, sys
s = socket.socket()
sys.exit(0 if s.connect_ex(('127.0.0.1', $API)) == 0 else 1)
" 2>/dev/null
}
command -v python3 >/dev/null || { echo "skip python3 yok"; exit 0; }
if busy; then
	echo "127.0.0.1:$API dolu — yarida kalmis bir testten kalma olabilir." >&2
	exit 1
fi

BIN="$WORK/xwrt"
( cd "$ROOT" && go build -o "$BIN" ./cmd/xwrt ) || {
	echo "build failed" >&2; exit 1; }

XWRT_CONFDIR="$WORK/cfg"
export XWRT_CONFDIR
mkdir -p "$XWRT_CONFDIR"

# A server at an address that is reserved precisely so that nothing can ever be
# there: TEST-NET-3, discard port. Every attempt fails, and it fails the way a
# real unreachable server does rather than by being refused instantly.
write_config() { # write_config <auto_connect>
	cat > "$XWRT_CONFDIR/xwrt" <<CFG
package xwrt

config xwrt 'main'
	option api_port '$API'
	option active 'p_test'
	option active_kind 'profile'
	option auto_connect '$1'
	option dns_mode 'off'
	option mode 'redirect'

config profile 'p_test'
	option name 'unreachable'
	option proto 'vless'
	option address '203.0.113.9'
	option port '9'
	option uuid '00000000-0000-0000-0000-000000000000'
CFG
}

start_daemon() {
	"$BIN" daemon >"$WORK/daemon.log" 2>&1 &
	echo $! > "$WORK/pid"
	i=0
	while [ "$i" -lt 20 ]; do
		"$BIN" status >/dev/null 2>&1 && return 0
		sleep 1
		i=$((i + 1))
	done
	return 1
}

stop_daemon() {
	[ -f "$WORK/pid" ] || return 0
	kill "$(cat "$WORK/pid")" 2>/dev/null
	wait "$(cat "$WORK/pid")" 2>/dev/null
	rm -f "$WORK/pid"
}

# How many times the daemon has tried, read from the journal: repeated failures
# collapse into one entry with a count, which is exactly the number wanted here.
attempts() {
	"$BIN" errors --json 2>/dev/null | python3 -c "
import json, sys
try:
    entries = json.load(sys.stdin)
except Exception:
    print(0); raise SystemExit
if isinstance(entries, dict):
    entries = entries.get('errors') or []
n = 0
for e in entries:
    n += 1 + int(e.get('repeats') or 0)
print(n)
"
}

# --- with the box on: it keeps trying ---------------------------------------
#
# The retries are 10s and 20s apart, so 35 seconds is enough for three attempts
# and short enough that nobody skips this test.
write_config 1
start_daemon || { echo "the daemon did not start" >&2; exit 1; }
sleep 35
n=$(attempts)
if [ "${n:-0}" -ge 2 ]; then
	ok "the startup connect keeps trying ($n attempts in 35s)"
else
	bad "only $n attempt(s) in 35 seconds — this is the lockout: a router whose"
	bad "upstream comes back late would stay offline until someone walks over"
fi
stop_daemon

# --- with the box off: it does not dial at all -------------------------------
rm -rf "$XWRT_CONFDIR"; mkdir -p "$XWRT_CONFDIR"
write_config 0
start_daemon || { echo "the daemon did not start" >&2; exit 1; }
sleep 20
n=$(attempts)
if [ "${n:-0}" = "0" ]; then
	ok "with connect-on-startup off it never dials out"
else
	bad "it dialled $n time(s) with connect-on-startup off — the setting is a lie"
fi

# --- and a stop during the backoff is prompt ---------------------------------
stop_daemon
rm -rf "$XWRT_CONFDIR"; mkdir -p "$XWRT_CONFDIR"
write_config 1
start_daemon || { echo "the daemon did not start" >&2; exit 1; }
# Far enough in to be asleep between attempts rather than mid-connect.
sleep 12
pid=$(cat "$WORK/pid")
start=$(date +%s)
kill "$pid" 2>/dev/null
i=0
while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 10 ]; do
	sleep 1
	i=$((i + 1))
done
took=$(( $(date +%s) - start ))
if kill -0 "$pid" 2>/dev/null; then
	bad "the daemon was still running ${took}s after SIGTERM — a service stop"
	bad "would hang waiting for a retry to finish sleeping"
	kill -9 "$pid" 2>/dev/null
else
	ok "a stop during the retry backoff takes ${took}s"
fi
rm -f "$WORK/pid"

echo
[ "$fail" = 0 ] && echo "the startup connect retries, obeys the setting, and stops promptly"
exit "$fail"
