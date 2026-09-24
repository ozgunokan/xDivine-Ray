#!/bin/sh
# Install, upgrade, uninstall, reinstall — against a real filesystem.
#
# This is the section of the plan that had never been run at all, and it is the
# one with the worst failure: an uninstall that leaves capture rules pointing
# at a binary that is gone takes the LAN off the internet, and the person who
# finds out is the one who can no longer reach the router to fix it.
#
# It needs a throwaway machine, because it writes to /usr/sbin, /etc and /www
# for real. That is the point — the scripts name absolute paths, and a test
# that rewrote them to point somewhere else would be testing something else.
# It refuses to run anywhere that looks like a device someone uses.
#
#   sh test/install.sh                 build a bundle and run the cycle
#   BUNDLE=release/xwrt-x86_64.tar.gz sh test/install.sh
#
# What it does not cover: procd and rpcd are not here, so starting and stopping
# the service is not exercised — install.sh reports those failures and carries
# on, which is what the output shows. uci is stubbed, and what the stub records
# is checked: an uninstall has to remove its own firewall zone and nothing else.
set -u

fail=0
ok()  { echo "ok   $*"; }
bad() { echo "FAIL $*" >&2; fail=1; }

# --- refuse to run on anything real ------------------------------------------
if [ -f /etc/openwrt_release ] || [ -d /overlay/upper ]; then
	echo "this is an OpenWrt device; run it on a throwaway machine instead" >&2
	exit 1
fi
if [ "$(id -u)" != "0" ]; then
	echo "skip needs root (it installs into /usr/sbin, /etc and /www)"
	exit 0
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

BUNDLE="${BUNDLE:-}"
if [ -z "$BUNDLE" ] && ! command -v go >/dev/null 2>&1; then
	# Said here rather than inside the build, because the build's own message
	# ("env: 'go': No such file or directory") reads like a broken test.
	echo "go is not on PATH, and this test builds a bundle before installing it." >&2
	echo "Under sudo that usually means sudo's own PATH: try" >&2
	echo "  sudo -E env \"PATH=\$PATH\" sh test/install.sh" >&2
	echo "or point it at a bundle that is already built:" >&2
	echo "  BUNDLE=release/xwrt-x86_64.tar.gz sh test/install.sh" >&2
	exit 1
fi
if [ -z "$BUNDLE" ]; then
	# Into its own directory: this bundle is a throwaway, and writing it to
	# release/ would replace whatever is there — including, at the wrong
	# moment, the bundle someone is about to ship.
	( cd "$ROOT" && OUT="$WORK/release" VERSION=0.0.0-test ./release.sh \
		>"$WORK/build.log" 2>&1 ) || {
		echo "the release build failed:" >&2
		tail -5 "$WORK/build.log" >&2
		exit 1
	}
	BUNDLE="$WORK/release/xwrt-x86_64.tar.gz"
fi
[ -f "$BUNDLE" ] || { echo "no bundle at $BUNDLE" >&2; exit 1; }

tar xzf "$BUNDLE" -C "$WORK"
PKG="$(find "$WORK" -maxdepth 1 -type d -name 'xwrt-*' | head -1)"
[ -d "$PKG" ] || { echo "the bundle has no package directory" >&2; exit 1; }

# --- a uci that records rather than acts --------------------------------------
# The real one is not here, and what matters is not what it would have done but
# what it was asked to do. An uninstall that deletes more of the firewall than
# its own zone is the kind of thing nobody notices until a port stops
# forwarding weeks later.
mkdir -p "$WORK/bin"
cat > "$WORK/bin/uci" <<'STUB'
#!/bin/sh
echo "uci $*" >> "$UCI_LOG"
if [ "$1" = "-q" ] && [ "$2" = "delete" ]; then
	# The uninstaller deletes in a loop until one fails; say no the second time.
	[ "$(grep -c "delete $3\$" "$UCI_LOG")" -le 1 ] && exit 0 || exit 1
fi
exit 0
STUB
chmod 755 "$WORK/bin/uci"
UCI_LOG="$WORK/uci.log"
export UCI_LOG
PATH="$WORK/bin:$PATH"
export PATH

SHIPPED="$WORK/shipped.txt"
( cd "$PKG" && find etc usr www -type f | sed 's|^|/|' ) > "$SHIPPED"
[ -s "$SHIPPED" ] || { echo "the bundle ships no files" >&2; exit 1; }

want=$(cat "$PKG/VERSION")

# --- install -----------------------------------------------------------------
# The exit status is checked, and that is not a detail. This test used to throw
# it away: an install.sh that died halfway still copied the files it had
# reached, so every assertion below passed and the run reported success for a
# script that could not be run to the end. It shipped that way once, and the
# router it was installed on was left with its service stopped.
: > "$UCI_LOG"
if ( cd "$PKG" && sh install.sh ) > "$WORK/install.log" 2>&1; then
	ok "install.sh ran to the end"
else
	bad "install.sh exited $? — last lines:"
	tail -5 "$WORK/install.log" >&2
fi

missing=0
while read -r f; do
	[ -e "$f" ] || { echo "   missing after install: $f"; missing=1; }
done < "$SHIPPED"
[ "$missing" = 0 ] && ok "every shipped file landed" || bad "the install left files behind"

# The version the installer reports has to be the version in the bundle. This
# is not a formality: release.sh used to package whatever binary happened to be
# in dist/, so three bundles went out carrying a week-old daemon under a new
# name. Nothing else noticed; this line is what caught it.
disk=$(/usr/sbin/xwrt --version 2>/dev/null || echo unknown)
if [ "$disk" = "$want" ]; then
	ok "the installed binary is the one in the bundle ($disk)"
else
	bad "the bundle says $want but the binary on disk says $disk"
fi

if [ -L /usr/sbin/xwrtd ]; then
	ok "xwrtd points at the binary"
else
	bad "xwrtd is not a symlink to /usr/sbin/xwrt"
fi

# --- settings survive an upgrade ---------------------------------------------
MARK="# a line the operator added"
echo "$MARK" >> /etc/config/xwrt
( cd "$PKG" && sh install.sh ) >> "$WORK/install.log" 2>&1 ||
	bad "install.sh failed when upgrading over itself"
if grep -qF "$MARK" /etc/config/xwrt; then
	ok "an upgrade keeps the existing configuration"
else
	bad "the upgrade overwrote /etc/config/xwrt — servers and pins would be gone"
fi

# --- uninstall ---------------------------------------------------------------
: > "$UCI_LOG"
( cd "$PKG" && sh uninstall.sh ) > "$WORK/uninstall.log" 2>&1 ||
	bad "uninstall.sh exited non-zero"

left=""
while read -r f; do
	[ -e "$f" ] || continue
	# The configuration is kept on purpose, and the uninstaller says so.
	[ "$f" = "/etc/config/xwrt" ] && continue
	left="$left $f"
done < "$SHIPPED"
[ -z "$left" ] && ok "the uninstall left nothing behind" || bad "still installed:$left"

for d in /www/luci-static/resources/xwrt /www/luci-static/resources/view/xwrt \
	/var/run/xwrt; do
	[ -e "$d" ] && bad "$d survived the uninstall"
done
[ -e /usr/sbin/xwrt ] && bad "the binary survived the uninstall"
[ -e /usr/sbin/xwrtd ] && bad "the xwrtd symlink survived the uninstall"

if [ -f /etc/config/xwrt ]; then
	ok "the configuration is kept, as the uninstaller says it is"
else
	bad "the uninstall deleted /etc/config/xwrt without being asked"
fi

# What it asked uci to do, and nothing more. Any other delete is a change to
# the operator's firewall that xwrt has no business making.
if grep -q "delete firewall.xwrt" "$UCI_LOG"; then
	ok "the uninstall removes its own firewall zone"
else
	bad "the uninstall never removed the firewall zone it added"
fi
others=$(grep "delete" "$UCI_LOG" | grep -v "delete firewall.xwrt" || true)
[ -z "$others" ] && ok "and deletes nothing else" ||
	bad "the uninstall also deleted: $others"

# --- reinstall ---------------------------------------------------------------
( cd "$PKG" && sh install.sh ) > "$WORK/reinstall.log" 2>&1 ||
	bad "install.sh failed on the reinstall"
if grep -qF "$MARK" /etc/config/xwrt && [ -x /usr/sbin/xwrt ]; then
	ok "reinstalling works and the kept configuration is still there"
else
	bad "a reinstall after an uninstall did not come back cleanly"
fi

# --- the safety net ------------------------------------------------------------
#
# The case this exists for: an upgrade on a router whose only way out is the
# service being upgraded. If the new version cannot serve, the device must not
# be left with it — the old binary goes back, because a working old version is
# the only thing that lets anyone look into what is wrong with the new one.
#
# Faked here by a "binary" that answers --version, which is all the installer
# can check before committing, and then cannot serve.
( cd "$PKG" && sh install.sh ) > "$WORK/pre-rollback.log" 2>&1 ||
	bad "could not set up the rollback case"
good_version=$(/usr/sbin/xwrt --version 2>/dev/null)

# Something has to be answering for the installer to count this as an upgrade
# of a working service rather than a first install.
/usr/sbin/xwrt daemon -no-restore >"$WORK/pre-rollback-daemon.log" 2>&1 &
daemon_pid=$!
sleep 3

if xwrt status >/dev/null 2>&1; then
	cp "$PKG/usr/sbin/xwrt" "$WORK/real-binary"
	cat > "$PKG/usr/sbin/xwrt" <<'BROKEN'
#!/bin/sh
# Answers the one question the installer asks before committing, and nothing
# else. This is what a binary that starts and then dies looks like from the
# outside.
[ "$1" = "--version" ] && { echo "9.9.9-broken"; exit 0; }
exit 1
BROKEN
	chmod 755 "$PKG/usr/sbin/xwrt"

	( cd "$PKG" && sh install.sh ) > "$WORK/rollback.log" 2>&1
	after=$(/usr/sbin/xwrt --version 2>/dev/null)
	if [ "$after" = "$good_version" ]; then
		ok "a version that cannot serve is rolled back to the previous binary"
	else
		bad "the broken version was left installed ($after); the device would " \
			"be offline with no way back"
	fi
	grep -q "restoring the previous one" "$WORK/rollback.log" ||
		bad "the rollback happened without saying so"

	cp "$WORK/real-binary" "$PKG/usr/sbin/xwrt"
	chmod 755 "$PKG/usr/sbin/xwrt"
else
	echo "skip the rollback case (no daemon to stand in for a running service)"
fi
kill "$daemon_pid" 2>/dev/null
wait "$daemon_pid" 2>/dev/null

# --- an upgrade does not leave the tunnel down --------------------------------
#
# "Connect on startup" decides what the daemon does on its own. An upgrade is
# not the daemon's own decision — it is the installer stopping a service that
# was carrying the network — so a tunnel that was up before must be up after,
# whatever that box says. It shipped the other way round once: an operator with
# the box unticked upgraded a router he was not sitting next to, and the tunnel
# stayed down.
#
# The daemon cannot be made to connect here (that needs a server to connect
# to), so what is tested is the installer's decision, with an `xwrt` that
# answers the way a real one would. It is the decision that was wrong.
fake_xwrt() {
	cat > "$WORK/bin/xwrt" <<'FAKE'
#!/bin/sh
# Stands in for the daemon's client: the state lives in a file, `connect`
# changes it, and the first status answers as things were before the service
# was stopped. Nothing here pretends to be a tunnel.
state=$(cat "$XWRT_FAKE_STATE" 2>/dev/null || echo down)
case "$1" in
--version) echo "$XWRT_FAKE_VERSION" ;;
status)
	case "$state" in
		# Asked once before the service is stopped, and the restart puts it
		# down — which is precisely the case under test.
		pre) echo down > "$XWRT_FAKE_STATE"; conn=true ;;
		up)  conn=true ;;
		*)   conn=false ;;
	esac
	echo "{\"connected\":$conn,\"auto_connect\":false,\"version\":\"$XWRT_FAKE_VERSION\"}"
	;;
connect)
	echo connect >> "$XWRT_FAKE_LOG"
	echo up > "$XWRT_FAKE_STATE"
	;;
*) : ;;
esac
exit 0
FAKE
	chmod 755 "$WORK/bin/xwrt"
}

XWRT_FAKE_STATE="$WORK/fake.state"
XWRT_FAKE_LOG="$WORK/fake.log"
XWRT_FAKE_VERSION="$want"
export XWRT_FAKE_STATE XWRT_FAKE_LOG XWRT_FAKE_VERSION
fake_xwrt

echo pre > "$XWRT_FAKE_STATE"
: > "$XWRT_FAKE_LOG"
( cd "$PKG" && sh install.sh ) > "$WORK/reconnect.log" 2>&1 ||
	bad "install.sh failed with a connected tunnel"
if grep -q connect "$XWRT_FAKE_LOG"; then
	ok "an upgrade brings back a tunnel that was connected before it"
else
	bad "the upgrade left the tunnel down — a remote router would be offline"
fi
grep -q "reconnecting" "$WORK/reconnect.log" ||
	bad "it reconnected without saying so"

# And the other direction: a tunnel that was deliberately down stays down, and
# the installer says why rather than leaving the operator to guess.
echo down > "$XWRT_FAKE_STATE"
: > "$XWRT_FAKE_LOG"
( cd "$PKG" && sh install.sh ) > "$WORK/nocon.log" 2>&1 ||
	bad "install.sh failed with a disconnected tunnel"
if grep -q connect "$XWRT_FAKE_LOG"; then
	bad "the installer connected a tunnel the operator had left disconnected"
else
	ok "a tunnel left disconnected is not connected behind the operator's back"
fi
if grep -q "Connect on startup" "$WORK/nocon.log"; then
	ok "and the install says why it is not connected"
else
	bad "the install ends without saying the tunnel is down or why"
fi
rm -f "$WORK/bin/xwrt"

# --- dependencies ------------------------------------------------------------
#
# The installer used to print a list of missing packages and leave the person
# reading it to type the commands. Someone installed xwrt for a friend, hit
# that, and fixed it by hand — so now it installs them, and this is where that
# is checked.
#
# There is no apk or opkg on the machine running this, and there must not be:
# a test that really installs packages is a test that changes the machine it
# runs on. A fake one on PATH records what it was asked for, which is the only
# thing worth asserting anyway.
DEPLOG="$WORK/pkg.log"
make_pkg_manager() {   # make_pkg_manager <name> <exit status for add/install>
	cat > "$WORK/bin/$1" <<PKGSTUB
#!/bin/sh
echo "\$@" >> "$DEPLOG"
case "\$1" in
	update) exit 0 ;;
esac
exit $2
PKGSTUB
	chmod 755 "$WORK/bin/$1"
}
drop_pkg_managers() { rm -f "$WORK/bin/apk" "$WORK/bin/opkg"; }

# A dependency that is present must not be reinstalled, and one that is absent
# must be. xray-core is the one that can be faked from PATH either way.
make_pkg_manager apk 0
: > "$DEPLOG"
printf '#!/bin/sh\nexit 0\n' > "$WORK/bin/xray"
chmod 755 "$WORK/bin/xray"
( cd "$PKG" && sh install.sh ) > "$WORK/deps-present.log" 2>&1 ||
	bad "install.sh failed while installing dependencies"
if grep -q "xray-core" "$DEPLOG"; then
	bad "xray-core was already on this device and was installed again"
else
	ok "a dependency that is already there is left alone"
fi
grep -q "xray-core: already here" "$WORK/deps-present.log" ||
	bad "the installer does not say which dependencies it found"

rm -f "$WORK/bin/xray"
: > "$DEPLOG"
( cd "$PKG" && sh install.sh ) > "$WORK/deps-missing.log" 2>&1 ||
	bad "install.sh failed when a dependency was missing"
if grep -q "add xray-core" "$DEPLOG"; then
	ok "a missing dependency is installed rather than just reported"
else
	bad "xray-core was missing and the installer did not try to install it:"
	sed 's/^/     /' "$DEPLOG" >&2
fi
grep -q "update" "$DEPLOG" ||
	bad "the package lists were never refreshed, so every package would be unknown"

# A package manager that refuses. The install must still finish — the files are
# worth having — and the end of the output must name what is missing, say what
# it is for, and give the command, because that is the whole point of the
# exercise.
make_pkg_manager apk 1
: > "$DEPLOG"
if ( cd "$PKG" && sh install.sh ) > "$WORK/deps-fail.log" 2>&1; then
	ok "an install whose dependencies could not be fetched still completes"
else
	bad "a failed dependency install aborted the whole installation"
fi
for want in "still missing" "xray-core" "the proxy core" "apk add"; do
	grep -q -- "$want" "$WORK/deps-fail.log" ||
		bad "the closing report does not mention \"$want\""
done
if grep -q "no connection can be made at all" "$WORK/deps-fail.log"; then
	ok "and it says plainly what a missing core costs"
else
	bad "xray-core is missing and the report does not say the tunnel cannot start"
fi

# The way out it offers has to be a way out. With this stub refusing every
# package, kmod-nft-tproxy is missing too, so suggesting tproxy as the
# alternative to a broken tun would send someone to the one other mode that
# cannot work either.
if grep -q "tproxy does not" "$WORK/deps-fail.log"; then
	ok "and it does not offer a mode whose own dependency is missing"
elif grep -q "tproxy modes still work" "$WORK/deps-fail.log"; then
	bad "tproxy is missing and the report still offers it as the way out"
fi

# --no-deps is for the person who manages packages themselves, and for the
# updater running on a device where the answer is already known.
: > "$DEPLOG"
( cd "$PKG" && sh install.sh --no-deps ) > "$WORK/deps-skip.log" 2>&1 ||
	bad "install.sh --no-deps failed"
if [ -s "$DEPLOG" ]; then
	bad "--no-deps still ran the package manager:"
	sed 's/^/     /' "$DEPLOG" >&2
else
	ok "--no-deps touches no packages"
fi
grep -q "still missing" "$WORK/deps-skip.log" ||
	bad "--no-deps skipped the install and also skipped saying what is missing"

# And a device with neither manager is told so, rather than being left to
# wonder why nothing happened.
drop_pkg_managers
: > "$DEPLOG"
# Removing the stubs is enough: this machine has no real apk or opkg either,
# which the guard at the top of this file would have complained about if it
# did.
( cd "$PKG" && sh install.sh ) > "$WORK/deps-none.log" 2>&1 ||
	bad "install.sh failed on a device with no package manager"
grep -q "neither apk nor opkg" "$WORK/deps-none.log" ||
	bad "a device with no package manager gets no explanation"
ok "a device with no package manager is told why nothing was installed"

# --- tidy up -------------------------------------------------------------------
( cd "$PKG" && sh uninstall.sh ) >/dev/null 2>&1
rm -f /etc/config/xwrt

echo
[ "$fail" = 0 ] && echo "install, upgrade, uninstall and reinstall all behave"
exit "$fail"
