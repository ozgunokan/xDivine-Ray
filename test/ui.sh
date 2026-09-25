#!/bin/sh
# Every check under luci-app-xwrt/test, in one run.
#
# They were written one at a time and run one at a time, by whoever had just
# written one. That means a check written for the Status page went unrun for
# every change to the settings page, which is precisely backwards: a test is
# worth having because it catches the change nobody thought would touch it.
#
# The harness files are skipped by name rather than by a list kept here: a new
# check has to be picked up without anyone remembering to add it, or this ends
# up being one more list to fall out of date.
#
#   sh test/ui.sh
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

if ! command -v node >/dev/null 2>&1; then
	echo "FAIL node is not installed, so none of the interface checks ran." >&2
	echo "     They are the only thing that reads the pages; skipping them" >&2
	echo "     quietly would report a green run that checked no interface." >&2
	exit 1
fi

fail=0
ran=0
for f in luci-app-xwrt/test/*.js; do
	case "$(basename "$f")" in
		lucistub.js|fixtures.js|formstub.js) continue ;;
	esac
	ran=$((ran + 1))
	echo "--- $f"
	if node "$f"; then
		:
	else
		fail=1
	fi
done

# A glob that matched nothing leaves the loop body unrun and the script
# exiting zero, which looks exactly like a clean pass.
if [ "$ran" -eq 0 ]; then
	echo "FAIL no interface checks were found under luci-app-xwrt/test" >&2
	exit 1
fi

echo
if [ "$fail" -eq 0 ]; then
	echo "ok   $ran interface checks passed"
else
	echo "FAIL some interface checks failed" >&2
fi
exit "$fail"
