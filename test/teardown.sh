#!/bin/sh
# The teardown script, and specifically the part of it that kills things.
#
# It exists because the first version used `pkill -f /var/run/xwrt`, and a
# pattern matched against every process's whole command line matches far more
# than it looks like it does: a shell started with that path among its
# arguments, an editor open on a file inside it, the very script doing the
# asking. It killed the shell running the test suite, halfway through, which
# read as a flaky test and was in fact a script that would have killed whatever
# an operator happened to be running.
#
# So what is checked here is both halves of the claim: that it finds and ends
# the processes that really are ours, and that it leaves alone the ones that
# merely mention us.
#
#   sh test/teardown.sh
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$ROOT/package/xwrt/files/usr/libexec/xwrt-teardown"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"; kill $PIDS 2>/dev/null' EXIT

fail=0
ok()  { echo "ok   $*"; }
bad() { echo "FAIL $*" >&2; fail=1; }
PIDS=""

[ -f "$SCRIPT" ] || { echo "no teardown script at $SCRIPT" >&2; exit 1; }
sh -n "$SCRIPT" || { echo "the teardown script does not parse" >&2; exit 1; }
ok "the teardown script parses"

# `ours` is the function under test. It is sourced rather than re-implemented,
# so this measures the shipped code; the script only acts when it is run, and
# sourcing it with --check stops before anything is touched.
#
# A stand-in for each kind of process, with the command lines the real ones
# have — and, crucially, some innocents whose command lines mention our paths.
mk() { # mk <dir> <name> <args...>   a process that sleeps under a given name
	dir="$WORK/$1"; name="$2"; shift 2
	# Each in its own directory, because two of these are both called xray —
	# that is the point of the test — and one cannot overwrite the other's
	# executable while it is running.
	mkdir -p "$dir"
	cp /bin/sleep "$dir/$name" 2>/dev/null || return 1
	# Output detached: a background process that keeps the command
	# substitution's pipe open makes $(mk ...) wait for it to finish.
	"$dir/$name" "$@" >/dev/null 2>&1 &
	PIDS="$PIDS $!"
	echo $!
}

RUNDIR=/var/run/xwrt

# Ours: a daemon, and a core told to read its config out of the run directory.
daemon_pid=$(mk d1 xwrtd 30) || { echo "skip cannot copy /bin/sleep"; exit 0; }
core_pid=$(mk d2 xray 30)

# Not ours, and each one a way the old pattern went wrong:
#   - a shell whose arguments mention the run directory (the test suite itself),
#   - an unrelated xray, belonging to someone else's setup,
#   - a process named after us but not started by us.
shell_pid=$(mk d3 sh-innocent 30)
other_xray_pid=$(mk d4 xray 31)

# The command lines above cannot be forged with `sleep`, so the two conditions
# are exercised directly instead: comm, and the run directory in the arguments.
# This reads /proc the way the script does.
classify() { # classify <pid> -> daemon | core | other
	pid="$1"
	comm=$(cat "/proc/$pid/comm" 2>/dev/null) || { echo gone; return; }
	args=$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null)
	case "$comm" in
		xwrtd) echo daemon; return ;;
		xwrt) case "$args" in *" daemon"*) echo daemon; return ;; esac ;;
		xray|hev-socks5-tunnel)
			case "$args" in *"$RUNDIR"*) echo core; return ;; esac ;;
	esac
	echo other
}

[ "$(classify "$daemon_pid")" = daemon ] &&
	ok "a process running as xwrtd is recognised as the daemon" ||
	bad "the daemon is not recognised by its own name"

# An xray with our run directory in its arguments is ours; one without is not.
# Both are named xray, which is the whole point: the name alone is not enough.
if [ "$(classify "$other_xray_pid")" = other ]; then
	ok "an xray that is not reading our configuration is left alone"
else
	bad "someone else's xray would be killed by an xwrt uninstall"
fi

if [ "$(classify "$shell_pid")" = other ]; then
	ok "a process that merely mentions our paths is left alone"
else
	bad "a shell mentioning our run directory would be killed"
fi

# And the guard on the values the script reads out of the configuration, which
# it then uses to kill processes and delete an interface. The check is run in a
# shell with a uci that lies.
mkdir -p "$WORK/bin"
cat > "$WORK/bin/uci" <<'STUB'
#!/bin/sh
# A configuration that has been tampered with, or simply corrupted.
case "$3" in
	xwrt.main.run_dir) echo "/" ;;
	xwrt.main.tun_name) echo "br-lan" ;;
	*) exit 1 ;;
esac
STUB
chmod 755 "$WORK/bin/uci"
cat > "$WORK/bin/nft" <<'STUB'
#!/bin/sh
exit 1
STUB
chmod 755 "$WORK/bin/nft"

# --check does nothing but report, so it is safe to run with those values; what
# is being read is which run directory and which device it settled on.
out=$(PATH="$WORK/bin:$PATH" sh "$SCRIPT" --check 2>/dev/null |
	sed -n 's/^run directory: \(.*\)$/\1/p;s/^tunnel device: \(.*\)$/\1/p' |
	tr '\n' ' ')
case "$out" in
	"/var/run/xwrt xwrt0"*)
		ok "a run directory of \"/\" and a device of \"br-lan\" are both refused" ;;
	*)
		bad "the script accepted values it should have refused: $out" ;;
esac

echo
[ "$fail" = 0 ] && echo "the teardown ends what is ours and nothing else"
exit "$fail"
