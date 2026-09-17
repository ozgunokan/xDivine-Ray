#!/bin/sh
# The update check, end to end: a real daemon asking a real HTTP server.
#
# The unit tests cover the pieces — comparing versions, reading a checksum
# file, refusing a release that cannot be verified. What they cannot cover is
# the wiring: that the daemon reads the repository out of its own
# configuration, asks, stores the answer, and tells the CLI and the interface
# the same thing. That wiring is where a feature like this usually breaks,
# because every piece works.
#
# The release page here is a few files served out of a directory, which is all
# the parts of GitHub's API this uses.
#
#   sh test/update.sh
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
API=18801
XWRT_API=18802

fail=0
ok()  { echo "ok   $*"; }
bad() { echo "FAIL $*" >&2; fail=1; }

cleanup() {
	[ -f "$WORK/daemon.pid" ] && kill "$(cat "$WORK/daemon.pid")" 2>/dev/null
	[ -f "$WORK/http.pid" ] && kill "$(cat "$WORK/http.pid")" 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

command -v python3 >/dev/null || { echo "skip python3 yok"; exit 0; }

busy() {
	python3 -c "
import socket, sys
s = socket.socket()
sys.exit(0 if s.connect_ex(('127.0.0.1', $1)) == 0 else 1)
" 2>/dev/null
}
for p in "$API" "$XWRT_API"; do
	if busy "$p"; then
		echo "127.0.0.1:$p dolu — yarida kalmis bir testten kalma olabilir." >&2
		exit 1
	fi
done

BIN="$WORK/xwrt"
# Built with a version stamped in, the way a release is. An unstamped build
# calls itself "dev", and "dev" is deliberately not comparable to anything — so
# a test against one would prove only that nothing is ever offered.
HERE_VERSION=$(cat "$ROOT/VERSION" 2>/dev/null || echo 1.0.0)
( cd "$ROOT" && go build \
	-ldflags "-X xwrt/internal/app.Version=$HERE_VERSION" \
	-o "$BIN" ./cmd/xwrt ) || { echo "build failed" >&2; exit 1; }
[ "$("$BIN" --version)" = "$HERE_VERSION" ] || {
	echo "the test binary reports $("$BIN" --version), not $HERE_VERSION" >&2
	exit 1
}

# --- a release page ----------------------------------------------------------
#
# Served as static files, in the shape the API answers: one JSON document per
# repository, plus the assets it names.
mkdir -p "$WORK/www/repos/someone/xwrt/releases" "$WORK/www/dl"

# A bundle, and the checksum that vouches for it.
echo "this stands in for a release bundle" > "$WORK/www/dl/xwrt-any.tar.gz"
sum=$(sha256sum "$WORK/www/dl/xwrt-any.tar.gz" | cut -d' ' -f1)

# The architecture this build looks for in a release's assets.
case "$(uname -m)" in
	x86_64) arch=x86_64 ;;
	aarch64|arm64) arch=aarch64 ;;
	*) arch=$(uname -m) ;;
esac
cp "$WORK/www/dl/xwrt-any.tar.gz" "$WORK/www/dl/xwrt-$arch.tar.gz"
printf '%s  xwrt-%s.tar.gz\n' "$sum" "$arch" > "$WORK/www/dl/sha256sums"

write_release() { # write_release <version> <with-checksums 0|1>
	sums=""
	[ "$2" = "1" ] && sums=',
		{"name": "sha256sums",
		 "browser_download_url": "http://127.0.0.1:'"$API"'/dl/sha256sums"}'
	cat > "$WORK/www/repos/someone/xwrt/releases/latest" <<JSON
{
  "tag_name": "v$1",
  "body": "notes for $1",
  "html_url": "http://example/releases/$1",
  "assets": [
    {"name": "xwrt-$arch.tar.gz",
     "browser_download_url": "http://127.0.0.1:$API/dl/xwrt-$arch.tar.gz",
     "size": 42}$sums
  ]
}
JSON
}

( cd "$WORK/www" && python3 -m http.server "$API" >/dev/null 2>&1 & echo $! > "$WORK/http.pid" )
sleep 1
busy "$API" || { echo "the release server did not start" >&2; exit 1; }

# --- a daemon ----------------------------------------------------------------
XWRT_CONFDIR="$WORK/cfg"
export XWRT_CONFDIR XWRT_UPDATE_API="http://127.0.0.1:$API"
mkdir -p "$XWRT_CONFDIR"
cat > "$XWRT_CONFDIR/xwrt" <<CFG
package xwrt

config xwrt 'main'
	option api_port '$XWRT_API'
	option update_check '1'
	option update_repo 'someone/xwrt'
	option dns_mode 'off'
CFG

"$BIN" daemon -no-restore >"$WORK/daemon.log" 2>&1 &
echo $! > "$WORK/daemon.pid"
i=0
while [ "$i" -lt 20 ]; do
	"$BIN" status >/dev/null 2>&1 && break
	sleep 1
	i=$((i + 1))
done
"$BIN" status >/dev/null 2>&1 || { echo "the daemon did not start" >&2; exit 1; }

field() { # field <key>   read one value out of the update answer
	"$BIN" update 2>/dev/null | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print(''); raise SystemExit
print(d.get('$1', ''))
"
}

# --- a newer release ---------------------------------------------------------
write_release 99.0.0 1
if [ "$(field available)" = "True" ]; then
	ok "a newer release is reported as available (running $HERE_VERSION, found $(field latest))"
else
	bad "a release newer than $HERE_VERSION was not reported: $("$BIN" update)"
fi
[ "$(field installable)" = "True" ] &&
	ok "and it is installable: it has a bundle for this architecture and a checksum file" ||
	bad "the release has both a bundle and checksums but was not installable"

# --- the same version --------------------------------------------------------
write_release "$HERE_VERSION" 1
if [ "$(field available)" = "False" ]; then
	ok "the version already running is not offered as an update"
else
	bad "the running version was offered as an update to itself"
fi

# --- an older one ------------------------------------------------------------
write_release 0.0.1 1
[ "$(field available)" = "False" ] &&
	ok "an older release is not offered either" ||
	bad "a release older than the running one was offered as an update"

# --- newer, but with nothing to verify it ------------------------------------
#
# The rule the whole updater rests on: this downloads something and runs it as
# root, so a release that cannot be verified is not installable, however new.
write_release 99.0.0 0
if [ "$(field available)" = "True" ] && [ "$(field installable)" = "False" ]; then
	ok "a release with no checksum file is reported, but not installable"
else
	bad "a release with no checksum file was treated as installable"
fi
"$BIN" update 2>/dev/null | grep -q "cannot be installed from here" &&
	ok "and it says why" ||
	bad "it does not say why the newer release cannot be installed"

# And asking to install it is refused rather than attempted.
if "$BIN" update install 2>&1 | grep -q "verifiable"; then
	ok "asking to install it anyway is refused"
else
	bad "an unverifiable release was not refused at install time: $("$BIN" update install 2>&1)"
fi

# --- a source that is not a repository ---------------------------------------
"$BIN" set update_repo=nonsense >/dev/null 2>&1
if "$BIN" update 2>/dev/null | grep -q "owner/name"; then
	ok "a source that is not an owner/name repository is refused with a reason"
else
	bad "a nonsense update source did not produce a clear error"
fi

echo
[ "$fail" = 0 ] && echo "the update check reads the release page, and refuses what it cannot verify"
exit "$fail"
