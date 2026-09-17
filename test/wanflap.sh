#!/bin/sh
# Takes the upstream link away and gives it back, then says what survived.
#
# This one runs ON THE ROUTER, unlike everything else in test/. It is the only
# way to test the thing an LTE or PPPoE line does several times a week on its
# own: the WAN goes, comes back a minute later with a different address and a
# different gateway, and the firewall is reloaded around it. Three things can
# be wrong afterwards and all three look like "the internet is slow":
#
#   - the capture rules were flushed by the firewall reload and never came back,
#     so nothing is being proxied any more;
#   - the core is still holding a connection to an address that is no longer
#     reachable, so the tunnel says connected and carries nothing;
#   - the policy route still points at the gateway that has gone.
#
# It is written to be safe to run from an SSH session on the LAN side, which is
# not the same as safe in general: it detaches itself from the terminal, and it
# starts a watchdog that brings the interface back up even if this script is
# killed halfway through. Read those two things before running it on a router
# you cannot reach physically.
#
#   sh /tmp/wanflap.sh              flap for 20 seconds
#   DOWN=60 sh /tmp/wanflap.sh      flap for a minute
#   WAN=wwan_1 sh /tmp/wanflap.sh   name the interface rather than detecting it
#
# Output goes to /tmp/wanflap.log as well as the terminal, so an SSH session
# that drops loses nothing.

DOWN="${DOWN:-20}"
SETTLE="${SETTLE:-45}"
LOG=/tmp/wanflap.log

# --- detach ------------------------------------------------------------------
# Re-exec in a session of its own, once. A flap tests what happens when the
# upstream goes away, and an SSH session that dies at the wrong moment must not
# leave the interface down.
if [ "${WANFLAP_DETACHED:-0}" != "1" ]; then
	WANFLAP_DETACHED=1
	export WANFLAP_DETACHED DOWN SETTLE WAN
	: > "$LOG"
	if command -v setsid >/dev/null 2>&1; then
		setsid sh "$0" "$@" >>"$LOG" 2>&1 &
	else
		nohup sh "$0" "$@" >>"$LOG" 2>&1 &
	fi
	echo "running in the background; following $LOG (Ctrl+C is safe)"
	echo
	sleep 1
	# tail -f would also be fine, but this ends by itself.
	i=0
	while [ "$i" -lt $((DOWN + SETTLE + 40)) ]; do
		if grep -q '^== done' "$LOG" 2>/dev/null; then
			break
		fi
		sleep 2
		i=$((i + 2))
	done
	cat "$LOG"
	exit 0
fi

say() { echo "$@"; }

say "== xwrt WAN flap test, $(date)"

# --- which interface ---------------------------------------------------------
# The logical interface, which is what ifup and ifdown take, found from the
# device the default route is on. On a router with several uplinks this picks
# the one actually carrying traffic, which is the one worth flapping.
if [ -z "${WAN:-}" ]; then
	dev=$(ip route show default 2>/dev/null | awk '/default/ {print $5; exit}')
	if [ -n "$dev" ]; then
		for i in $(ubus list 'network.interface.*' 2>/dev/null | sed 's/network\.interface\.//'); do
			[ "$i" = "loopback" ] && continue
			d=$(ubus call "network.interface.$i" status 2>/dev/null |
				jsonfilter -e '@.l3_device' 2>/dev/null)
			if [ "$d" = "$dev" ]; then
				WAN="$i"
				break
			fi
		done
	fi
fi
if [ -z "${WAN:-}" ]; then
	say "could not work out which interface carries the default route."
	say "run it again naming one, e.g.:  WAN=wwan_1 sh $0"
	say "== done (nothing was touched)"
	exit 1
fi
say "interface:      $WAN  (device $(ip route show default | awk '/default/ {print $5; exit}'))"

# --- before ------------------------------------------------------------------
rules_before=$(nft list table ip xwrt 2>/dev/null | wc -l)
if [ "$rules_before" -lt 2 ]; then
	rules_before=$(iptables-save 2>/dev/null | grep -c -i xwrt)
	backend=iptables
else
	backend=nftables
fi
status_before=$(xwrt status 2>/dev/null)
conn_before=no
case "$status_before" in *'"connected":true'*) conn_before=yes ;; esac
gw_before=$(ip route show default | awk '/default/ {print $3; exit}')
dev=$(ip route show default | awk '/default/ {print $5; exit}')
addr_before=$(ip -4 addr show dev "$dev" 2>/dev/null | awk '/inet /{print $2; exit}')

say "firewall:       $backend, $rules_before lines in xwrt's table"
say "tunnel before:  connected=$conn_before, gateway $gw_before, address $addr_before"
if [ "$conn_before" != "yes" ]; then
	say
	say "the tunnel is not up, so there is nothing for a flap to break."
	say "connect first, then run this again."
	say "== done (nothing was touched)"
	exit 1
fi

# --- the watchdog ------------------------------------------------------------
# Whatever happens below, the interface comes back. This is the part that makes
# the test safe to run on a router that is not in the room.
#
# In a session of its own, and under a name that has nothing to do with this
# script. Both on purpose: a subshell started with `( ... ) &` dies with its
# parent when someone kills the script — which is exactly what a person does
# when a test they are watching seems to have hung, and it is precisely then
# that the interface is down and the recovery is the only thing that matters.
# Tested: kill every process with wanflap in its name while the line is down,
# and the interface still comes back.
if command -v setsid >/dev/null 2>&1; then
	setsid sh -c "sleep $((DOWN + 30)); ifup $WAN" >/dev/null 2>&1 &
else
	( trap '' HUP INT TERM; sleep $((DOWN + 30)); ifup "$WAN" ) >/dev/null 2>&1 &
fi
watchdog=$!

# --- flap --------------------------------------------------------------------
say
say "-- taking $WAN down for ${DOWN}s"
ifdown "$WAN"

# Was it really down, though.
#
# This check is the difference between a test and a ritual. `ifdown` on an
# interface that sits on top of a modem can release a lease and tear down a
# route while the modem stays attached and the session upstream is never
# dropped — and then everything below passes, quickly and reassuringly, having
# proved nothing. The report has to be able to say which of the two happened,
# so the default route is watched while the line is supposed to be gone.
down_seen=no
i=0
while [ "$i" -lt "$DOWN" ]; do
	if [ -z "$(ip route show default 2>/dev/null)" ]; then
		down_seen=yes
		break
	fi
	sleep 1
	i=$((i + 1))
done
if [ "$down_seen" = yes ]; then
	say "   the default route was gone ${i}s in — the line really went away"
else
	say "   the default route never went away"
fi

rest=$((DOWN - i))
[ "$rest" -gt 0 ] && sleep "$rest"
say "-- bringing $WAN back up"
ifup "$WAN"

kill "$watchdog" 2>/dev/null

# --- settle ------------------------------------------------------------------
# The daemon is told about the new interface by procd and reconnects on its
# own; this waits for that rather than measuring the middle of it. It stops
# early once the tunnel is back, so a good router does not sit here for the
# full time.
say "-- waiting up to ${SETTLE}s for the line and the tunnel to come back"
i=0
while [ "$i" -lt "$SETTLE" ]; do
	if xwrt status 2>/dev/null | grep -q '"connected":true' &&
	   [ -n "$(ip route show default 2>/dev/null)" ]; then
		break
	fi
	sleep 3
	i=$((i + 3))
done
say "   settled after ${i}s"
# A moment more, because the capture rules are reinstalled by the firewall
# hotplug script, which runs after the interface is up.
sleep 5

# --- after -------------------------------------------------------------------
if [ "$backend" = nftables ]; then
	rules_after=$(nft list table ip xwrt 2>/dev/null | wc -l)
else
	rules_after=$(iptables-save 2>/dev/null | grep -c -i xwrt)
fi
status_after=$(xwrt status 2>/dev/null)
conn_after=no
case "$status_after" in *'"connected":true'*) conn_after=yes ;; esac
gw_after=$(ip route show default | awk '/default/ {print $3; exit}')
dev_after=$(ip route show default | awk '/default/ {print $5; exit}')
addr_after=$(ip -4 addr show dev "$dev_after" 2>/dev/null | awk '/inet /{print $2; exit}')

say
say "-- after"
say "firewall:       $rules_after lines (was $rules_before)"
say "tunnel:         connected=$conn_after (was $conn_before)"
say "gateway:        $gw_after (was $gw_before)"
if [ "$addr_after" = "$addr_before" ]; then
	say "address:        $addr_after, unchanged"
else
	say "address:        $addr_after (was $addr_before) — a new lease, which is"
	say "                the harder case and the one worth having tested"
fi

fail=0
if [ -z "$gw_after" ]; then
	say "FAIL the line did not come back at all — this is the router, not xwrt."
	fail=1
fi
if [ "$conn_after" != "yes" ]; then
	say "FAIL the tunnel did not come back by itself."
	fail=1
fi
if [ "$rules_after" -lt "$rules_before" ]; then
	say "FAIL capture rules are missing: $rules_after lines where there were" \
		"$rules_before. Nothing is being proxied."
	fail=1
fi

# And the check that matters more than any count: does traffic actually go
# through the tunnel now. A connected status with flushed rules, or a core
# holding a dead connection, both pass every test above and fail this one.
say
say "-- self test"
xwrt selftest 2>&1
say

if [ "$fail" = 0 ] && [ "$down_seen" != yes ]; then
	say "INCONCLUSIVE — the default route never disappeared, so the upstream was"
	say "probably never actually dropped: on a modem, ifdown can take the route"
	say "away without ending the session, or the interface can come back inside"
	say "the second between two checks here."
	say
	say "Everything above came back because nothing had gone. To make it a real"
	say "flap, try a longer outage:   DOWN=60 sh $0"
	say "or pull the upstream cable, or put the modem in flight mode, for a minute."
	say "== done"
	exit 1
fi
if [ "$fail" = 0 ]; then
	say "the line went away and came back, and so did the tunnel."
else
	say "something did not come back. xwrt errors, and the last lines of xwrt logs:"
	xwrt errors 2>&1 | head -20
	xwrt logs 2>&1 | tail -20
fi
say "== done"
exit "$fail"
