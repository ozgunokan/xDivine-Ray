#!/bin/sh
# Removes xwrt, checks that it really is gone and that nothing it left behind
# is still in the kernel, and puts it back.
#
# Runs ON THE ROUTER, from an unpacked bundle. test/install.sh already does this
# cycle against a throwaway filesystem, and what it cannot do is the part that
# matters here: a real firewall, a real dnsmasq and a real tunnel. The failure
# this exists for is specific and quiet — an uninstall that removes the binary
# but leaves the capture rules installed points every LAN connection at a proxy
# that no longer exists, and the LAN loses the internet with nothing left on the
# device to explain why.
#
# The tunnel goes down while this runs. On a line whose only way out is the
# tunnel, that means no internet for the length of the cycle, so:
#
#   - it detaches from the terminal, like the flap test, and logs to
#     /tmp/xwrt-uninstall-cycle.log;
#   - a watchdog reinstalls from this bundle if the script dies halfway;
#   - it reconnects at the end if the tunnel was connected at the start.
#
# Run it from inside the unpacked bundle:
#
#   cd /tmp/xwrt-aarch64 && sh uninstall-cycle.sh
#
# KEEP=1 leaves it uninstalled at the end (for someone who is removing it for
# real and wants the checks).

KEEP="${KEEP:-0}"
LOG=/tmp/xwrt-uninstall-cycle.log
HERE=$(pwd)

[ -f "$HERE/uninstall.sh" ] && [ -f "$HERE/install.sh" ] || {
	echo "run this from inside an unpacked bundle (where install.sh is)"
	exit 1
}

if [ "${CYCLE_DETACHED:-0}" != "1" ]; then
	CYCLE_DETACHED=1
	export CYCLE_DETACHED KEEP
	: > "$LOG"
	if command -v setsid >/dev/null 2>&1; then
		setsid sh "$0" "$@" >>"$LOG" 2>&1 &
	else
		nohup sh "$0" "$@" >>"$LOG" 2>&1 &
	fi
	echo "running in the background; output also in $LOG"
	echo
	i=0
	while [ "$i" -lt 180 ]; do
		grep -q '^== done' "$LOG" 2>/dev/null && break
		sleep 2
		i=$((i + 2))
	done
	cat "$LOG"
	exit 0
fi

say() { echo "$@"; }
fail=0
bad() { say "FAIL $*"; fail=1; }

say "== xwrt uninstall cycle, $(date)"
say "bundle:         $HERE ($(cat "$HERE/VERSION" 2>/dev/null || echo '?'))"

# --- before ------------------------------------------------------------------
conn_before=no
xwrt status 2>/dev/null | grep -q '"connected":true' && conn_before=yes
active_before=$(uci -q get xwrt.main.active 2>/dev/null || echo "")
say "tunnel before:  connected=$conn_before"

# --- the watchdog ------------------------------------------------------------
# If this script dies between the uninstall and the install, the device is left
# without the thing carrying its internet. In its own session and under its own
# name, for the reason the flap test explains: the moment recovery matters most
# is the moment someone kills a script that looks stuck.
if [ "$KEEP" != "1" ]; then
	if command -v setsid >/dev/null 2>&1; then
		setsid sh -c "sleep 150; [ -x /usr/sbin/xwrt ] || { cd $HERE && sh install.sh; }" \
			>/dev/null 2>&1 &
	else
		( trap '' HUP INT TERM; sleep 150
		  [ -x /usr/sbin/xwrt ] || { cd "$HERE" && sh install.sh; } ) >/dev/null 2>&1 &
	fi
fi

# --- uninstall ---------------------------------------------------------------
say
say "-- uninstalling"
sh "$HERE/uninstall.sh" 2>&1 | sed 's/^/   /'

sleep 2

say
say "-- what is left"

# Every file the bundle ships, checked by reading the bundle rather than by a
# list written here: a file added to the package and not to the uninstaller is
# exactly the kind of leftover this is for, and a list kept by hand would be
# updated in the same commit that forgot.
left=""
for f in $(cd "$HERE" && find etc usr www -type f 2>/dev/null); do
	# The configuration is kept on purpose, and the uninstaller says so.
	[ "/$f" = "/etc/config/xwrt" ] && continue
	[ -e "/$f" ] && left="$left /$f"
done
if [ -n "$left" ]; then
	bad "these are still installed:$left"
else
	say "files:          all removed (except /etc/config/xwrt, kept on purpose)"
fi

# The one that takes the LAN offline: rules pointing at a core that is gone.
rules=$(nft list table ip xwrt 2>/dev/null | wc -l)
ipt=$(iptables-save 2>/dev/null | grep -c -i xwrt)
if [ "$rules" -gt 1 ] || [ "${ipt:-0}" -gt 0 ]; then
	bad "capture rules are still installed (nft $rules lines, iptables $ipt" \
		"rules). Every LAN connection is being sent to a proxy that no longer" \
		"exists. Recover with: nft delete table ip xwrt; fw4 restart"
else
	say "capture rules:  gone"
fi

# And the one that takes name resolution with it.
if [ -f /tmp/dnsmasq.d/xwrt.conf ] || ls /tmp/dnsmasq.d/*/xwrt.conf >/dev/null 2>&1; then
	bad "the dnsmasq snippet is still there; DNS is still pointed at a core" \
		"that is gone. Recover with: rm -f /tmp/dnsmasq.d/xwrt.conf;" \
		"/etc/init.d/dnsmasq restart"
else
	say "dnsmasq:        clean"
fi

if ip link show xwrt0 >/dev/null 2>&1; then
	bad "the tunnel device xwrt0 is still there"
else
	say "tun device:     gone"
fi

if pgrep -f 'xray .*xwrt' >/dev/null 2>&1 || pgrep -x xwrtd >/dev/null 2>&1; then
	bad "something of xwrt's is still running"
else
	say "processes:      none left"
fi

if [ -f /etc/config/xwrt ]; then
	say "configuration:  kept, as the uninstaller says"
else
	bad "/etc/config/xwrt was deleted without being asked"
fi

if [ "$KEEP" = "1" ]; then
	say
	say "left uninstalled, as asked (KEEP=1)."
	say "== done"
	exit "$fail"
fi

# --- and back ----------------------------------------------------------------
say
say "-- installing it again"
sh "$HERE/install.sh" 2>&1 | sed 's/^/   /'

# install.sh reconnects a tunnel that was up when it ran — but nothing was
# running a moment ago, so it cannot have seen one. If there was a connection
# before this script started, it is this script's job to put it back.
if [ "$conn_before" = yes ]; then
	i=0
	while [ "$i" -lt 20 ]; do
		xwrt status >/dev/null 2>&1 && break
		sleep 1
		i=$((i + 1))
	done
	if ! xwrt status 2>/dev/null | grep -q '"connected":true'; then
		say
		say "-- reconnecting (it was connected before this started)"
		xwrt connect "$active_before" >/dev/null 2>&1 || xwrt connect >/dev/null 2>&1
		sleep 3
	fi
fi

conn_after=no
xwrt status 2>/dev/null | grep -q '"connected":true' && conn_after=yes
say
say "tunnel after:   connected=$conn_after (was $conn_before)"
if [ "$conn_before" = yes ] && [ "$conn_after" != yes ]; then
	bad "the tunnel did not come back. xwrt connect, then xwrt errors."
fi

say
if [ "$fail" = 0 ]; then
	say "uninstalled cleanly, left nothing in the kernel, and came back."
else
	say "see the failures above."
fi
say "== done"
exit "$fail"
