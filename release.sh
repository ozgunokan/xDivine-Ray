#!/bin/sh
# Build installable bundles: one per architecture, plus the source.
#
# Each bundle is a plain tarball that unpacks over / on the router, with an
# install script that does the three things a package manager would otherwise
# do: create the symlink the daemon is invoked through, run the uci-defaults
# script once, and start the services in the right order.
set -e

cd "$(dirname "$0")"
# Overridable, because test/install.sh builds a throwaway bundle and should not
# replace the one someone is about to ship.
OUT="${OUT:-release}"
rm -rf "$OUT"
mkdir -p "$OUT"

# The version goes into the binary at compile time, not into the tarball at
# packaging time, so this script builds rather than packaging whatever happens
# to be lying in dist/.
#
# It used to do the latter, and the result was worse than a wrong number: three
# bundles went out named for a version whose code was not in them. The
# interface files are copied from source here and were new; the binary was a
# week old. Everything looked installed, nothing behaved as it should, and the
# only thing that said so was the version check at the end of install.sh.
#
# SKIP_BUILD=1 packages dist/ as it stands, for the one case that needs it: a
# binary built somewhere else. It still has to agree with the version asked
# for, which dist/VERSION records.
VERSION="${VERSION:-$(cat VERSION 2>/dev/null || git describe --tags --always 2>/dev/null || date +%Y%m%d)}"

if [ "${SKIP_BUILD:-0}" != "1" ]; then
	# OUT is this script's own (where the bundles go); build.sh reads the same
	# name for where the binaries go, so it is given its own value explicitly.
	# Letting it inherit put the binaries in the bundle directory and left
	# dist/ holding the previous version — which this script then refused to
	# package, correctly but confusingly.
	OUT=dist VERSION="$VERSION" ./build.sh
fi

[ -d dist ] || { echo "run ./build.sh first"; exit 1; }

built=$(cat dist/VERSION 2>/dev/null || echo unknown)
if [ "$built" != "$VERSION" ]; then
	echo "the binaries in dist/ are version $built, not $VERSION." >&2
	echo "they would be packaged under a name whose code is not in them." >&2
	echo "run ./build.sh (or drop SKIP_BUILD) and try again." >&2
	exit 1
fi

for target in dist/*/; do
	arch=$(basename "$target")
	[ -f "$target/xwrt" ] || continue

	stage="$OUT/.stage-$arch"
	rm -rf "$stage"
	mkdir -p "$stage/usr/sbin"

	# The binary and everything the package would ship.
	cp "$target/xwrt" "$stage/usr/sbin/xwrt"
	chmod 755 "$stage/usr/sbin/xwrt"
	cp -r package/xwrt/files/* "$stage/"

	# The LuCI app: views, the translation catalog, and the ACL/menu
	# declarations.
	mkdir -p "$stage/www/luci-static/resources/view/xwrt" \
		"$stage/www/luci-static/resources/xwrt"
	cp luci-app-xwrt/htdocs/luci-static/resources/xwrt.js \
		"$stage/www/luci-static/resources/xwrt.js"
	cp luci-app-xwrt/htdocs/luci-static/resources/xwrt/*.js \
		"$stage/www/luci-static/resources/xwrt/"
	cp luci-app-xwrt/htdocs/luci-static/resources/view/xwrt/*.js \
		"$stage/www/luci-static/resources/view/xwrt/"
	mkdir -p "$stage/usr/share/luci/menu.d" "$stage/usr/share/rpcd/acl.d"
	cp luci-app-xwrt/root/usr/share/luci/menu.d/*.json "$stage/usr/share/luci/menu.d/"
	cp luci-app-xwrt/root/usr/share/rpcd/acl.d/*.json "$stage/usr/share/rpcd/acl.d/"

	chmod 755 "$stage/etc/init.d/xwrt" "$stage/usr/libexec/rpcd/xwrt" \
		"$stage/etc/uci-defaults/99-xwrt" "$stage/etc/hotplug.d/firewall/99-xwrt" \
		"$stage/usr/libexec/xwrt-teardown"

	cat > "$stage/install.sh" <<'INSTALL'
#!/bin/sh
# Install xwrt on this OpenWrt device. Run from the unpacked bundle directory.
set -e

[ -f usr/sbin/xwrt ] || { echo "run this from inside the unpacked bundle"; exit 1; }

fail=0

# --- checks that happen before anything is touched ---------------------------
#
# Everything below this point is ordered by one rule: nothing that can leave
# this device without a working service happens until everything that can be
# checked has been. On a connection that has no way out except through this
# service, an install that stops halfway is not an inconvenience, it is a trip
# home to plug in a cable.
#
# This script parses itself first. A shell reads a script command by command,
# so a syntax error in the second half is not noticed until the first half has
# already run — which, here, means the service is stopped and the binary is
# half replaced. That happened. `sh -n` reads the whole file and says so before
# a single line of it has done anything.
if ! sh -n "$0" 2>/tmp/xwrt-install-check.$$; then
	echo "this installer is damaged and was not run:" >&2
	cat /tmp/xwrt-install-check.$$ >&2
	rm -f /tmp/xwrt-install-check.$$
	echo "nothing was changed; the running service is untouched." >&2
	exit 1
fi
rm -f /tmp/xwrt-install-check.$$

# And the new binary is run before the old one is stopped. A bundle for the
# wrong architecture, a truncated download, a file that lost its executable
# bit: all of them are fatal, all of them are visible here, and here nothing
# has been replaced yet.
chmod 755 usr/sbin/xwrt 2>/dev/null || true
if ! NEW_VERSION=$(./usr/sbin/xwrt --version 2>/dev/null); then
	echo "the binary in this bundle does not run on this device." >&2
	echo "wrong architecture, or an incomplete download." >&2
	echo "nothing was changed; the running service is untouched." >&2
	exit 1
fi

# Said before anything happens, because the commonest way to spend an evening
# on nothing is to unpack one bundle and run the install command from another.
echo "==> installing xDivine-Ray $(cat ./VERSION 2>/dev/null || echo '?') from $(pwd)"

echo "==> checking dependencies"
missing=""
command -v xray >/dev/null 2>&1 || missing="$missing xray-core"
command -v nft >/dev/null 2>&1 || command -v iptables >/dev/null 2>&1 || \
	missing="$missing nftables-or-iptables"
command -v ip >/dev/null 2>&1 || missing="$missing ip-full"
if [ -n "$missing" ]; then
	echo "    missing:$missing"
	# Newer OpenWrt uses apk, older uses opkg; printing the wrong command is a
	# needless dead end for whoever reads this.
	if command -v apk >/dev/null 2>&1; then
		echo "    install them with: apk add xray-core ip-full"
	else
		echo "    install them with: opkg update && opkg install xray-core ip-full"
	fi
	echo "    (mixed and tun modes additionally need: kmod-tun hev-socks5-tunnel)"
	echo
fi

# The daemon has to stop before its own binary is replaced. A running
# executable cannot be written to — the kernel refuses with "Text file busy" —
# so an upgrade that copies first and restarts afterwards silently keeps the
# old binary and reports success. That is exactly how an install can look
# finished while nothing changed.
# The binary that is about to be replaced is kept, because the only honest
# answer to "the new one does not start" is to put the old one back rather than
# to leave the device with neither.
PREV=""
if [ -x /usr/sbin/xwrt ]; then
	PREV=/tmp/xwrt.previous.$$
	cp /usr/sbin/xwrt "$PREV" 2>/dev/null || PREV=""
fi

# Whether there was a working service here a moment ago, which decides what a
# silent service at the end of this means. On an upgrade it means this install
# broke something that was carrying the network, and the old binary goes back.
# On a first install it means only that nothing is configured yet, and putting
# a previous version back would be putting back nothing.
WAS_UP=0
WAS_CONNECTED=0
if STATUS_BEFORE=$(xwrt status 2>/dev/null); then
	WAS_UP=1
	# And whether the tunnel itself was carrying traffic, which is a separate
	# question and the one the operator actually cares about.
	#
	# "Connect on startup" decides what the daemon does on its own — after a
	# reboot, after a crash. An upgrade is not the daemon's own decision: it is
	# this script stopping a service that was working. Leaving the tunnel down
	# afterwards because a box about boot behaviour is unticked means an
	# upgrade silently disconnects a router, which is how an operator ends up
	# driving home. So: if it was up before, it is brought back up after,
	# whatever that box says.
	case "$STATUS_BEFORE" in
		*'"connected":true'*) WAS_CONNECTED=1 ;;
	esac
fi

echo "==> stopping the service"
/etc/init.d/xwrt stop 2>/dev/null || true
sleep 1

echo "==> copying files"
# An existing configuration is kept across an upgrade: it holds the servers,
# the pinned certificates and every port the operator moved, and silently
# replacing it with the defaults would undo all of that.
KEEP=""
if [ -f /etc/config/xwrt ]; then
	KEEP=/tmp/xwrt.config.keep.$$
	cp /etc/config/xwrt "$KEEP"
	echo "    keeping your existing /etc/config/xwrt"
fi

# Errors here are the whole ballgame, so they are printed rather than hidden.
# A full overlay and a busy binary both fail exactly here, and both used to
# pass unnoticed.
fail=0
for d in etc usr www; do
	[ -d "./$d" ] || continue
	cp -a "./$d/." "/$d/" || {
		echo "    ERROR: could not write /$d" >&2
		fail=1
	}
done

# The binary again, the way that works even when something still holds it open:
# write it beside the target and rename over it. A rename replaces the
# directory entry without touching the file anyone is still running.
if cp ./usr/sbin/xwrt /usr/sbin/xwrt.new 2>/dev/null; then
	chmod 755 /usr/sbin/xwrt.new
	mv /usr/sbin/xwrt.new /usr/sbin/xwrt || {
		echo "    ERROR: could not replace /usr/sbin/xwrt" >&2
		fail=1
	}
else
	echo "    ERROR: could not write /usr/sbin/xwrt.new (disk full?)" >&2
	rm -f /usr/sbin/xwrt.new 2>/dev/null
	fail=1
fi

if [ -n "$KEEP" ]; then
	mv "$KEEP" /etc/config/xwrt
fi
ln -sf /usr/sbin/xwrt /usr/sbin/xwrtd
chmod 755 /usr/sbin/xwrt /etc/init.d/xwrt /usr/libexec/rpcd/xwrt \
	/etc/uci-defaults/99-xwrt /etc/hotplug.d/firewall/99-xwrt

# Every shipped file is compared with the copy that landed. This is the step
# that was missing: without it, a half-finished install is indistinguishable
# from a finished one until some page shows text from three versions ago.
echo "==> verifying what landed"
same() {
	cmp -s "$1" "$2" 2>/dev/null && return 0
	[ "$(md5sum < "$1" 2>/dev/null)" = "$(md5sum < "$2" 2>/dev/null)" ]
}
differs=0
for f in $(cd ./www 2>/dev/null && find . -type f 2>/dev/null); do
	same "./www/$f" "/www/${f#./}" || {
		echo "    NOT REPLACED: /www/${f#./}" >&2
		differs=$((differs + 1))
	}
done
if [ "$differs" -gt 0 ]; then
	echo "    $differs interface file(s) are still the old ones." >&2
	echo "    Usually the overlay is full — check with: df -h /overlay" >&2
	fail=1
fi

echo "==> first-time setup (firewall zone, defaults)"
sh /etc/uci-defaults/99-xwrt || true

echo "==> restarting rpcd so LuCI sees the new methods"
rm -f /tmp/luci-indexcache /tmp/luci-modulecache/* 2>/dev/null || true
/etc/init.d/rpcd restart 2>/dev/null || echo "    (rpcd restart failed; LuCI may need a reboot)"
# Those two clear the caches on this side. The browser has one of its own, and
# an interface page that was open during the upgrade keeps running the
# JavaScript it already had — with the new daemon underneath it. The result is
# a page that is half upgraded: new numbers, old layout, sentences the new
# daemon sends that the old page cannot translate. It looks like a bug in the
# release and it is a cache, so it is worth one line here.
echo "    (in the browser, reload the page with Ctrl+F5: it caches these files)"

# The service was stopped before the copy; procd's "start" only does something
# to a service that is not running, which is now the case.
echo "==> starting xwrt"
/etc/init.d/xwrt enable 2>/dev/null || true
/etc/init.d/xwrt start 2>/dev/null || echo "    (start failed; run: xwrtd --version to check the binary)"

# And then wait for it, because procd's `start` returns as soon as it has
# accepted the job — not when the service is serving. Asking a second later
# gets a connection refused from a daemon that is seconds from being up, and
# the version report below then prints an empty line under "daemon running"
# and tells the operator their install did not take. It did; the installer was
# just looking too early. This is the only thing that check gets wrong, and it
# gets it wrong on every install.
printf "    waiting for the service"
i=0
while [ "$i" -lt 10 ]; do
	if xwrt status >/dev/null 2>&1; then
		echo " — up"
		break
	fi
	printf "."
	sleep 1
	i=$((i + 1))
done

if [ "$i" -ge 10 ] && [ "$WAS_UP" = "0" ]; then
	# Nothing was running before either. That is an ordinary first install on a
	# device with nothing configured yet, not a regression, and there is
	# nothing to roll back to.
	echo " — not answering yet"
	echo "    (nothing was running before this install either; start it with:"
	echo "     /etc/init.d/xwrt start)"
elif [ "$i" -ge 10 ]; then
	echo " — not answering"
	# Put the previous binary back and start it. Whatever is wrong with the new
	# one can be worked out from a device that is online; it cannot be worked
	# out from one that is not.
	if [ -n "$PREV" ] && [ -s "$PREV" ]; then
		echo "==> the new version did not start; restoring the previous one"
		cp "$PREV" /usr/sbin/xwrt.rollback && chmod 755 /usr/sbin/xwrt.rollback &&
			mv /usr/sbin/xwrt.rollback /usr/sbin/xwrt
		/etc/init.d/xwrt start 2>/dev/null || true
		sleep 3
		if xwrt status >/dev/null 2>&1; then
			echo "    the previous version is running again ($(xwrt --version 2>/dev/null))"
			echo "    the interface files are from the new bundle; reinstall the"
			echo "    old bundle if you want them back as well."
		else
			echo "    the previous version did not start either." >&2
			echo "    run: /etc/init.d/xwrt start   and then: xwrt errors" >&2
		fi
	else
		echo "    no previous binary to fall back to." >&2
		echo "    run: /etc/init.d/xwrt start   and then: xwrt errors" >&2
	fi
	fail=1
fi
rm -f "$PREV" 2>/dev/null || true

# The tunnel back the way it was found. Only when it really was up before and
# really is down now: reconnecting a tunnel the operator had deliberately
# disconnected would be just as rude in the other direction.
if [ "$WAS_CONNECTED" = "1" ] && ! xwrt status 2>/dev/null | grep -q '"connected":true'; then
	echo "==> the tunnel was connected before this upgrade; reconnecting"
	if xwrt connect >/dev/null 2>&1; then
		sleep 2
	fi
	if xwrt status 2>/dev/null | grep -q '"connected":true'; then
		echo "    connected"
	else
		echo "    could not reconnect — run: xwrt errors" >&2
	fi
fi

sleep 2
echo
echo "==> status"
xwrt status || true
echo
want=$(cat ./VERSION 2>/dev/null || echo unknown)
disk=$(xwrt --version 2>/dev/null || echo unknown)
live=$(xwrt status 2>/dev/null | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
echo "    this bundle:    $want"
echo "    binary on disk: $disk"
echo "    daemon running: $live"

# And the one line an operator reads the whole output for: is the tunnel up. If
# it is not, the reason is usually a setting rather than a fault, and saying so
# here is cheaper than a support round trip.
#
# Only when the daemon answers: `xwrt status` prints an error object and exits
# non-zero when it cannot reach the daemon, and that is already reported above.
# The `if` also keeps `set -e` from ending the script on that exit status,
# which is how this line, added to be helpful, once truncated the install.
if now=$(xwrt status 2>/dev/null); then
	case "$now" in
		*'"connected":true'*)
			echo "    tunnel:         connected" ;;
		*'"auto_connect":false'*)
			echo "    tunnel:         not connected"
			echo "                    (\"Connect on startup\" is off, so the daemon"
			echo "                     never dials out by itself — connect from LuCI"
			echo "                     or with: xwrt connect)" ;;
		*)
			echo "    tunnel:         not connected (xwrt connect, then xwrt errors)" ;;
	esac
fi
if [ "$disk" != "$want" ]; then
	echo
	echo "    !! The binary on disk is NOT the one in this bundle." >&2
	echo "    !! Nothing you see in the interface will have changed." >&2
	echo "    !! Check free space: df -h /overlay" >&2
	fail=1
fi
if [ "$fail" != "0" ]; then
	echo
	echo "*** INSTALL INCOMPLETE — see the errors above. ***" >&2
	echo "*** Nothing below is worth trying until that is sorted. ***" >&2
	echo
	exit 1
fi
echo "Done. Next:"
echo "  xwrt import '<share link>'"
echo "  xwrt list"
echo "  xwrt fetch-cert <profile-id>   # only if the link had allowInsecure=1"
echo "  xwrt connect <profile-id>"
echo
echo "  xwrt selftest                  # where the latency comes from"
echo
echo "In LuCI: VPN -> xDivine-Ray. Logs and failures: xwrt logs / xwrt errors."
INSTALL
	chmod 755 "$stage/install.sh"

	cat > "$stage/uninstall.sh" <<'UNINSTALL'
#!/bin/sh
# Remove xwrt.
#
# The order is the whole point, and it was wrong once. Removing the files is the
# easy half; the half that matters is making sure nothing this daemon put in the
# kernel outlives it. Capture rules pointing at a core that is gone black-hole
# every LAN connection, and the binary that would have explained it has just
# been deleted.
#
# The sequence that gets that right lives in /usr/libexec/xwrt-teardown, which
# this package installs and which the package manager's prerm runs too. One
# copy: two copies of a delicate sequence is how the package manager's path
# stayed broken after this one was fixed.
#
# Deliberately not `set -e`: half of what follows is allowed to fail, and an
# uninstall that stops in the middle is the failure being guarded against.

if [ -x /usr/libexec/xwrt-teardown ]; then
	/usr/libexec/xwrt-teardown
	teardown=$?
elif [ -x ./usr/libexec/xwrt-teardown ]; then
	# Running from an unpacked bundle against an installation that predates the
	# teardown script.
	./usr/libexec/xwrt-teardown
	teardown=$?
else
	echo "==> stopping the service"
	[ -x /etc/init.d/xwrt ] && /etc/init.d/xwrt stop 2>/dev/null
	[ -x /etc/init.d/xwrt ] && /etc/init.d/xwrt disable 2>/dev/null
	teardown=0
fi

echo "==> removing files"
rm -f /usr/sbin/xwrt /usr/sbin/xwrtd /etc/init.d/xwrt \
	/usr/libexec/rpcd/xwrt /usr/libexec/xwrt-teardown \
	/etc/hotplug.d/firewall/99-xwrt /etc/uci-defaults/99-xwrt
rm -rf /www/luci-static/resources/view/xwrt /www/luci-static/resources/xwrt
rm -f /www/luci-static/resources/xwrt.js \
	/usr/share/luci/menu.d/luci-app-xwrt.json \
	/usr/share/rpcd/acl.d/luci-app-xwrt.json
rm -rf /var/run/xwrt

# The firewall zone the installer added is removed too, so nothing dangles.
if command -v uci >/dev/null 2>&1; then
	while uci -q delete firewall.xwrt >/dev/null 2>&1; do :; done
	uci -q commit firewall 2>/dev/null
	/etc/init.d/firewall reload >/dev/null 2>&1
fi

rm -f /tmp/luci-indexcache /tmp/luci-modulecache/* 2>/dev/null
/etc/init.d/rpcd restart >/dev/null 2>&1

if [ "$teardown" != "0" ]; then
	echo
	echo "!! xwrt is removed, but the teardown above could not clear everything." >&2
	echo "!! LAN traffic may be going nowhere. Recover with:" >&2
	echo "!!   nft delete table ip xwrt; rm -f /tmp/dnsmasq.d/xwrt.conf" >&2
	echo "!!   /etc/init.d/dnsmasq restart; fw4 restart" >&2
	exit 1
fi

echo
echo "xwrt removed, and nothing of it is left in the firewall or the resolver."
echo "Your servers and settings are still in /etc/config/xwrt."
echo "Delete that file too if you do not want to keep them."
UNINSTALL
	chmod 755 "$stage/uninstall.sh"

	# The on-router tests travel with the bundle. They are the ones that cannot
	# run anywhere else — the device's own kernel, firewall and upstream link
	# are the thing under test — and asking someone to paste a two-hundred-line
	# script into an SSH session is how a script arrives with a line missing.
	for t in wanflap uninstall-cycle; do
		[ -f "test/$t.sh" ] || continue
		cp "test/$t.sh" "$stage/$t.sh"
		chmod 755 "$stage/$t.sh"
	done

	# install.sh reads this to say out loud which version it is installing, and
	# to check afterwards that the binary on disk is really that one. Unpacking
	# yesterday's bundle by mistake is otherwise invisible.
	echo "$VERSION" > "$stage/VERSION"

	cat > "$stage/README.txt" <<EOF
xDivine-Ray $VERSION — $arch

The web interface lives under VPN -> xDivine-Ray in LuCI. The package, the
service and the config file are still called xwrt, so an existing
/etc/config/xwrt keeps working untouched.

Unpack this on the router and run install.sh:

    tar xzf xwrt-$arch.tar.gz
    cd xwrt-$arch
    sh install.sh

Uninstall:

    sh uninstall.sh

Your servers and settings live in /etc/config/xwrt and are kept; delete that
file as well if you want them gone.

Also in here:

    sh wanflap.sh        take the upstream link down for 20 seconds and
                         report what came back: the capture rules, the
                         tunnel, the default route. Safe to run over SSH
                         from the LAN side — it detaches itself and brings
                         the interface back even if it is killed.

    sh uninstall-cycle.sh
                         remove xwrt, check that nothing it installed is
                         left in the kernel — capture rules, the dnsmasq
                         snippet, the tunnel device — and put it back.
                         KEEP=1 leaves it uninstalled.
EOF

	# The scripts are checked before they are packaged.
	#
	# They are written here inside a quoted heredoc, which means nothing in
	# them is parsed at the time they are written: a broken quote in this file
	# produces a perfectly valid release.sh and an install.sh that dies on the
	# line it reaches. `sh -n` on this script cannot see it — the heredoc is
	# opaque text — so the generated files have to be checked as files.
	#
	# The cost of not doing this was a router left with its service stopped,
	# its files copied, and the line that starts it never reached, on a
	# connection that has no way out without the service.
	for script in "$stage/install.sh" "$stage/uninstall.sh" "$stage/wanflap.sh" \
		"$stage/uninstall-cycle.sh"; do
		[ -f "$script" ] || continue
		if ! sh -n "$script" 2>"$OUT/.shcheck"; then
			echo "$script has a syntax error and was not packaged:" >&2
			cat "$OUT/.shcheck" >&2
			rm -f "$OUT/.shcheck"
			exit 1
		fi
	done
	rm -f "$OUT/.shcheck"

	mv "$stage" "$OUT/xwrt-$arch"
	(cd "$OUT" && tar czf "xwrt-$arch.tar.gz" "xwrt-$arch" && rm -rf "xwrt-$arch")
	echo "  $OUT/xwrt-$arch.tar.gz  $(du -h "$OUT/xwrt-$arch.tar.gz" | cut -f1)"
done

# The source, without build output or module cache.
#
# .github is in this list for a reason that cost a day. The workflows are part
# of the source — a change to how a release is built is a change to the project
# — but they used to be left out, so unpacking this archive over a checkout
# updated everything except the thing that would have to run to prove it. A CI
# fix shipped in one of these archives, went in, and the workflow that was
# supposed to pick it up was the old one. The failure looked identical to the
# bug that had just been fixed.
tar czf "$OUT/xwrt-src.tar.gz" \
	--exclude=dist --exclude=release --exclude=.git \
	cmd internal package luci-app-xwrt test .github go.mod VERSION LICENSE \
	build.sh release.sh README.md
echo "  $OUT/xwrt-src.tar.gz  $(du -h "$OUT/xwrt-src.tar.gz" | cut -f1)"

# And the checksums, which are not a formality here.
#
# The daemon's own updater downloads a bundle from a release page and hands it
# to a script that unpacks it into /usr/sbin and runs it as root. It refuses to
# do that for a release with no sha256sums file — there is no point having an
# updater that installs whatever it was given. So this file is part of the
# release, not an extra: upload it alongside the bundles or in-place updating
# will not work.
(
	cd "$OUT" || exit 1
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum xwrt-*.tar.gz > sha256sums
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 xwrt-*.tar.gz > sha256sums
	else
		echo "no sha256sum here; the release has no checksums and the" >&2
		echo "in-place updater will refuse to install it." >&2
		exit 1
	fi
)
echo "  $OUT/sha256sums  ($(wc -l < "$OUT/sha256sums") files)"
echo
echo "Upload every xwrt-*.tar.gz AND sha256sums to the release."
echo "Without sha256sums, xwrt update will refuse to install this version." 
