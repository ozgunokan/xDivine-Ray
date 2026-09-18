#!/bin/sh
# The OpenWrt package and the tarball bundle must install the same thing.
#
# There are two ways xwrt gets onto a device — `make menuconfig` and an image
# built from the feed, or release.sh's tarball unpacked over / — and they are
# described in two different files. A file added to one and not the other
# produces a device that is missing something, and which half is missing
# depends on how it was installed. That is the worst kind of difference: it
# cannot be reproduced by whoever did not install it the same way.
#
# So the two lists are compared, by reading both files rather than by keeping a
# third list here. The version is compared too: the package used to say 0.1.0
# while the daemon inside it reported 0.23.0.
#
#   sh test/package.sh
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

fail=0
ok()  { echo "ok   $*"; }
bad() { echo "FAIL $*" >&2; fail=1; }

MK=package/xwrt/Makefile
LUCI_MK=luci-app-xwrt/Makefile

# --- one version ---------------------------------------------------------
version=$(cat VERSION 2>/dev/null)
if [ -z "$version" ]; then
	bad "there is no VERSION file; the version would be written in three places"
else
	ok "the version is $version, from the VERSION file"
fi
for f in "$MK" "$LUCI_MK" build.sh release.sh; do
	grep -q 'VERSION' "$f" || bad "$f does not read the version from anywhere"
done
# A literal version in a Makefile is the drift that already happened once.
for f in "$MK" "$LUCI_MK"; do
	if grep -E '^PKG_VERSION:=[0-9]+\.[0-9]+' "$f" >/dev/null 2>&1; then
		bad "$f has the version written into it; it will drift from VERSION"
	fi
done
[ "$fail" = 0 ] && ok "both Makefiles take the version from that one file"

# --- the same files ------------------------------------------------------
#
# What the package installs, read out of its install block: every source path
# under ./files/.
pkg_files=$(sed -n '/define Package\/xwrt\/install/,/^endef/p' "$MK" |
	grep -o '\./files/[^ ]*' | sed 's|^\./files||' | sort -u)

# And what the bundle ships, which release.sh builds by copying the same
# directory wholesale — so the bundle's list is the directory itself.
bundle_files=$(cd package/xwrt/files && find . -type f | sed 's|^\.||' | sort -u)

missing=""
for f in $bundle_files; do
	echo "$pkg_files" | grep -qx "$f" || missing="$missing $f"
done
if [ -n "$missing" ]; then
	bad "the bundle ships these and the package does not install them:$missing"
else
	ok "every file the bundle ships is installed by the package too"
fi

extra=""
for f in $pkg_files; do
	[ -f "package/xwrt/files$f" ] || extra="$extra $f"
done
if [ -n "$extra" ]; then
	bad "the package installs files that do not exist:$extra"
else
	ok "the package installs nothing that is not there"
fi

# --- the whole source tree reaches the buildroot --------------------------
#
# The package does not download its sources; Build/Prepare copies them in, one
# directory at a time, by name. That list is the only thing standing between a
# new top-level package directory and a firmware build that fails hundreds of
# lines later with "no required module provides package xwrt/internal/<new>".
# Nobody adding a directory thinks to open a Makefile, so the check is here
# instead: every directory that holds Go code has to be in the copy list.
prepare=$(sed -n '/define Build\/Prepare/,/^endef/p' "$MK")
for d in $(find . -maxdepth 1 -type d ! -name . ! -name .git | sed 's|^\./||' | sort); do
	case "$d" in
		dist|release|test|package|luci-app-xwrt) continue ;;
	esac
	# Only directories that actually contain Go sources matter to the build.
	[ -n "$(find "$d" -name '*.go' -print -quit 2>/dev/null)" ] || continue
	echo "$prepare" | grep -q "/$d " ||
		bad "$d holds Go code but Build/Prepare never copies it into the" \
			"buildroot; an in-tree build would fail on a missing package"
done
echo "$prepare" | grep -q '/go.mod' ||
	bad "Build/Prepare does not copy go.mod; the build would not be a module"
[ "$fail" = 0 ] && ok "every Go directory in the tree is copied into the buildroot"

# --- the runtime requirements --------------------------------------------
#
# The point of building this into an image is that the image has everything.
# Each of these is needed by a capture mode the interface offers, so a package
# without it produces a menu entry that fails at connect time.
for dep in xray-core ip-full kmod-tun hev-socks5-tunnel kmod-nft-tproxy; do
	if grep -q -- "+$dep" "$MK"; then
		:
	else
		bad "$dep is not a dependency; an image built from this could not use" \
			"every capture mode the interface offers"
	fi
done
[ "$fail" = 0 ] && ok "the core, ip-full, both tunnel pieces and tproxy are dependencies"

# --- the package finds its own sources ------------------------------------
#
# The package does not download a tarball; it copies the Go sources from the
# tree it lives in, found by relative path. That path has to survive being
# reached through a feed, which is how every real build reaches it: OpenWrt
# symlinks package/feeds/<feed>/xwrt at the feed, and the feed at the source
# tree. Make reports the physical path, so ../.. lands back in the source —
# but that is a property of make and of nothing else, and this is the one
# assumption in the build that cannot be checked by reading the Makefile.
#
# So it is checked by running it, in the three layouts that exist: the source
# tree itself, a doubly-symlinked feed, and a buildroot that copied the package
# directory instead (where the answer must be "no idea", so Build/Prepare can
# say so rather than building nothing).
if command -v make >/dev/null 2>&1; then
	probe=$(mktemp)
	sed -n '/^XWRT_SRC ?=/,/^$/p' "$MK" > "$probe"
	printf 'all: ; @echo "$(XWRT_SRC)"\n' >> "$probe"
	tmp=$(mktemp -d)

	direct=$(cd package/xwrt && make -s -f "$probe" 2>/dev/null)
	[ "$direct" = "$ROOT" ] ||
		bad "from its own directory the package resolves the sources to" \
			"\"$direct\", not $ROOT"

	# The feed layout, both links and all.
	mkdir -p "$tmp/package/feeds/xwrt"
	ln -s "$ROOT" "$tmp/feed"
	ln -s "$tmp/feed/package/xwrt" "$tmp/package/feeds/xwrt/xwrt"
	feed=$(cd "$tmp/package/feeds/xwrt/xwrt" && make -s -f "$probe" 2>/dev/null)
	[ "$feed" = "$ROOT" ] ||
		bad "through a feed symlink the package resolves the sources to" \
			"\"$feed\", not $ROOT; an SDK or buildroot build would copy" \
			"nothing and fail later on a missing Go package"

	# And a copy, with no sources anywhere near it.
	mkdir -p "$tmp/copy"
	cp "$MK" "$tmp/copy/Makefile"
	copied=$(cd "$tmp/copy" && make -s -f "$probe" 2>/dev/null)
	[ -z "$copied" ] ||
		bad "a package directory copied away from its source tree resolved" \
			"the sources to \"$copied\", which is not where they are"

	rm -rf "$tmp" "$probe"
	[ "$fail" = 0 ] && ok "the package finds its sources from its own directory and through a feed"
else
	echo "note make is not installed here, so the source lookup was not run"
fi

# --- the SDK workflow builds everything there is to build -----------------
#
# .apk and .ipk are produced by a workflow that names the packages explicitly,
# because the SDK will happily build a feed's worth of nothing and exit zero.
# A package added here and not there is a release where half of xwrt has a
# package and the other half does not — and the half without is the interface,
# so the device installs cleanly and has no pages.
WF=.github/workflows/packages.yml
if [ ! -f "$WF" ]; then
	bad "there is no $WF, so no release carries real packages"
else
	pkgnames=$(grep -h '^PKG_NAME:=' "$MK" "$LUCI_MK" | sed 's/^PKG_NAME:=//')
	listed=$(sed -n 's/^ *PACKAGES: *//p' "$WF")
	for p in $pkgnames; do
		case " $listed " in
			*" $p "*) ;;
			*) bad "$p is not in the SDK workflow's PACKAGES, so no .apk or" \
				".ipk of it is ever built" ;;
		esac
	done
	# And the other direction: a name left behind after a rename makes the
	# whole SDK job fail with "package not found", at release time.
	for p in $listed; do
		echo "$pkgnames" | grep -qx "$p" ||
			bad "the SDK workflow builds \"$p\", which is not a package in" \
				"this tree any more"
	done
	[ "$fail" = 0 ] && ok "the SDK workflow builds every package in the tree ($listed)"
fi

# The source archive has to carry the workflows, because it is how source
# reaches the repository. Without them, an archive updates every file except
# the ones that decide whether the update is any good — and a CI fix shipped
# that way is tested by the CI it was supposed to replace.
src_line=$(sed -n '/^tar czf "\$OUT\/xwrt-src.tar.gz"/,/^$/p' release.sh)
echo "$src_line" | grep -q '\.github' ||
	bad "release.sh leaves .github out of the source archive; a workflow" \
		"change shipped in it would never reach the repository"
for d in cmd internal package luci-app-xwrt test; do
	echo "$src_line" | grep -q "$d" ||
		bad "release.sh leaves $d out of the source archive"
done
[ "$fail" = 0 ] && ok "the source archive carries the workflows and every source directory"

# And the workflows have to parse.
#
# A broken workflow file does not fail loudly; GitHub simply does not run it,
# and the run that was supposed to prove a release is missing rather than red.
# One heredoc inside a YAML block scalar did that here: the terminator has to
# be at column zero to end the heredoc, and column zero ends the block scalar
# instead, so the file became unparseable and every workflow in it stopped.
if python3 -c 'import yaml' 2>/dev/null; then
	for wf in .github/workflows/*.yml; do
		[ -f "$wf" ] || continue
		err=$(python3 -c '
import sys, yaml
try:
    d = yaml.safe_load(open(sys.argv[1]))
except Exception as e:
    print(str(e).splitlines()[0]); sys.exit(0)
if not isinstance(d, dict) or not d.get("jobs"):
    print("no jobs in it")
' "$wf" 2>&1)
		[ -z "$err" ] || bad "$wf does not parse: $err"
	done
	[ "$fail" = 0 ] && ok "every workflow parses and has jobs in it"
else
	echo "note python3 with PyYAML is not installed here, so the workflow" \
		"files were not parsed"
fi

# --- one repository, named once ------------------------------------------
#
# The update source appears in three places: the daemon's default, the hint
# under the field in Settings, and the package's URL. A device whose default
# points at a repository that does not exist checks for updates forever and
# finds none, and the only sign is a line in a log nobody reads.
repo=$(sed -n 's/^const DefaultUpdateRepo = "\(.*\)"$/\1/p' internal/model/model.go)
if [ -z "$repo" ]; then
	bad "the daemon has no default update repository"
else
	hint=$(sed -n "s/.*o.placeholder = '\([^']*\/[^']*\)';.*/\1/p" \
		luci-app-xwrt/htdocs/luci-static/resources/view/xwrt/settings.js | head -1)
	if [ "$hint" != "$repo" ]; then
		bad "the daemon defaults to $repo but Settings suggests $hint"
	elif ! grep -q "github.com/$repo" package/xwrt/Makefile; then
		bad "the package's URL does not point at $repo"
	else
		ok "the update source is $repo, and says so in all three places"
	fi
fi

# --- and the teardown is wired into both removal paths --------------------
grep -q 'xwrt-teardown' "$MK" ||
	bad "the package's prerm does not run the teardown: removing it with the" \
		"package manager would leave the capture rules in the kernel"
grep -q 'xwrt-teardown' release.sh ||
	bad "the bundle's uninstaller does not run the teardown"
[ -x package/xwrt/files/usr/libexec/xwrt-teardown ] ||
	bad "the teardown script is not executable, so neither path can run it"
[ "$fail" = 0 ] && ok "both ways of removing xwrt run the same teardown"

# --- the shipped shell scripts parse -------------------------------------
for f in $(cd package/xwrt/files && find . -type f | sed 's|^\./||'); do
	case "$f" in
		etc/config/*) continue ;;
	esac
	head -1 "package/xwrt/files/$f" | grep -q '^#!' || continue
	sh -n "package/xwrt/files/$f" 2>/dev/null ||
		bad "package/xwrt/files/$f has a syntax error"
done
[ "$fail" = 0 ] && ok "every shipped script parses"

echo
[ "$fail" = 0 ] && echo "the package and the bundle install the same xwrt"
exit "$fail"
