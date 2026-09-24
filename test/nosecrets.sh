#!/bin/sh
# Nothing that looks like a real credential may be in the source tree.
#
# A UUID belonging to a live server spent several releases inside a test file,
# in a public repository, because it was pasted in while reproducing a bug with
# a real share link and never swapped for an invented one. Nothing failed.
# Nothing looked wrong. It was found by accident.
#
# The shapes below are what a leak looks like in this project: a UUID that is
# not one of the deliberately fake ones, a real-looking hostname next to a
# proxy scheme, and a base64 subscription blob. The fake forms are all-zeros,
# all-ones and the other obviously-typed ones — a real UUID from a provider
# never looks like that.
#
#   sh test/nosecrets.sh
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

fail=0
bad() { echo "FAIL $*" >&2; fail=1; }

# Where to look: the source, never the build output.
files=$(find . -type f \
	! -path "./.git/*" ! -path "./dist/*" ! -path "./release/*" \
	\( -name '*.go' -o -name '*.js' -o -name '*.sh' -o -name '*.md' \
	   -o -name '*.json' -o -name 'Makefile' -o -name 'VERSION' \) \
	! -name 'nosecrets.sh')

# --- UUIDs ---------------------------------------------------------------
#
# Anything in UUID shape that is not visibly invented. The placeholder forms
# are groups of repeated digits, which no provider hands out.
uuids=$(grep -hoE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' \
	$files 2>/dev/null | sort -u)
for u in $uuids; do
	# Invented ones: every block is a single repeated character, or the whole
	# thing is the documented example nil UUID.
	if echo "$u" | grep -qE '^([0-9a-f])\1{7}-([0-9a-f])\2{3}-([0-9a-f])\3{3}-([0-9a-f])\4{3}-([0-9a-f])\5{11}$'; then
		continue
	fi
	# The shape this project uses for test material: leading zeros, then
	# ascending or trivially patterned blocks.
	case "$u" in
		00000000-*) continue ;;
		11111111-*) continue ;;
		22222222-*) continue ;;
		deadbeef-*) continue ;;
		# Xray's own documentation UUID, which appears in every example
		# config it ships and in the probe this project builds to ask a core
		# what it supports. It belongs to nobody.
		b831381d-6324-4d53-ad4f-8cda48b30811) continue ;;
	esac
	where=$(grep -l "$u" $files 2>/dev/null | tr '\n' ' ')
	bad "a UUID that does not look invented is in the source: $u  ($where)"
	echo "     if that is a real server's, it has to be rotated, not just" >&2
	echo "     deleted — the history of a public repository keeps it." >&2
done

# --- real-looking endpoints in share links -------------------------------
#
# A link is fine; a link pointing at something that is not an example domain is
# someone's actual server.
links=$(grep -hoE '(vless|vmess|trojan|ss)://[^"'"'"' ]+' $files 2>/dev/null |
	grep -oE '@[A-Za-z0-9.-]+' | sed 's/^@//' | sort -u)
for h in $links; do
	case "$h" in
		# Example and reserved names, per RFC 2606 and RFC 6761.
		*.example|*.example.com|*.example.net|*.example.org|example.com|\
		*.invalid|*.test|*.local|localhost)
			continue ;;
		# Anything with no dot in it is a placeholder word, not a host.
		*.*) ;;
		*) continue ;;
	esac
	case "$h" in
		# Documentation and private ranges: RFC 5737, RFC 1918, loopback, and
		# the two literals everyone writes when they mean "some address".
		192.0.2.*|198.51.100.*|203.0.113.*) continue ;;
		10.*|192.168.*|127.*) continue ;;
		172.1[6-9].*|172.2[0-9].*|172.3[01].*) continue ;;
		1.2.3.4|10.20.30.40) continue ;;
	esac
	bad "a share link in the source points at $h, which is not an example host"
done

# --- subscription bodies -------------------------------------------------
subs=$(grep -hoE 'https://[A-Za-z0-9.-]+/sub/[A-Za-z0-9]{8,}' $files 2>/dev/null |
	sort -u)
for s in $subs; do
	case "$s" in
		https://example.com/*|https://*.example/*|https://*.example.com/*) continue ;;
	esac
	bad "a subscription URL that is not an example is in the source: $s"
done

echo
if [ "$fail" = 0 ]; then
	echo "no real credentials in the source tree"
fi
exit "$fail"
