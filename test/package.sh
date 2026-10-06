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

# --- MIPS16 is off, in both spellings ------------------------------------
#
# Go's runtime ships MIPS assembly the assembler rejects in MIPS16 mode, so a
# MIPS build with it on dies inside runtime/cgo with a page of "invalid
# operands" about a file nothing here wrote. It names neither MIPS16 nor this
# package, which is what makes it expensive: it reads like a broken toolchain.
#
# OpenWrt renamed the opt-out — PKG_USE_MIPS16 on 21.02 and 22.03,
# PKG_BUILD_FLAGS on 24.10, which ignores the old name. Carrying only one of
# them means the package builds on one generation of buildroot and fails on the
# other, on MIPS devices only, which nobody testing on ARM would ever see.
# It happened: a Zyxel WSM20 against OpenWrt 24.10.8.
mips16=0
grep -q '^PKG_USE_MIPS16:=0' "$MK" || {
	bad "PKG_USE_MIPS16:=0 is missing, so a MIPS build on 21.02 or 22.03 will" \
		"fail inside runtime/cgo with errors that name neither MIPS16 nor xwrt"
	mips16=1
}
grep -q '^PKG_BUILD_FLAGS:=.*no-mips16' "$MK" || {
	bad "PKG_BUILD_FLAGS:=no-mips16 is missing, so a MIPS build on 24.10 or" \
		"newer will fail the same way: it no longer reads PKG_USE_MIPS16"
	mips16=1
}
[ "$mips16" = 0 ] && ok "MIPS16 is off for both generations of buildroot"

# --- helloXdivine is spelled the same in both places ----------------------
#
# The name is this project's; the core knows it as "hellogolang" and nothing
# else. So the interface offers one spelling and the config builder translates
# that spelling, and if the two ever disagree the result is not an error: the
# core is handed a fingerprint it has never heard of and quietly uses its own
# default. The connection works, the dropdown says what was chosen, and nothing
# anywhere says the choice was discarded.
ui_fp=$(sed -n "s/.*'randomized', '\([^']*\)'.*/\1/p" \
	luci-app-xwrt/htdocs/luci-static/resources/view/xwrt/profiles.js | head -1)
go_fp=$(sed -n 's/^const FingerprintXDivine = "\(.*\)"$/\1/p' \
	internal/xray/fingerprint.go)
if [ -z "$ui_fp" ] || [ -z "$go_fp" ]; then
	bad "the extra fingerprint could not be found in both places" \
		"(interface: '${ui_fp:-}', daemon: '${go_fp:-}')"
elif [ "$(printf %s "$ui_fp" | tr 'A-Z' 'a-z')" != \
      "$(printf %s "$go_fp" | tr 'A-Z' 'a-z')" ]; then
	bad "the interface offers '$ui_fp' but the daemon translates '$go_fp'," \
		"so choosing it would hand the core a name it does not know and the" \
		"core would silently use its default fingerprint instead"
else
	ok "the interface's '$ui_fp' is the name the daemon translates"
fi
grep -q '^const coreFingerprintName = "hellogolang"$' internal/xray/fingerprint.go ||
	bad "the daemon no longer translates to hellogolang, which is the only name" \
		"the core actually has for this fingerprint"

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

# --- and so do the ones in the repository root ---------------------------
#
# get.sh in particular: it is the URL strangers paste into a router, so a
# syntax error in it is a syntax error in the front door.
for f in get.sh build.sh release.sh test/*.sh; do
	[ -f "$f" ] || continue
	sh -n "$f" 2>/dev/null || bad "$f has a syntax error"
done
[ "$fail" = 0 ] && ok "the build, release and install scripts parse"

echo
[ "$fail" = 0 ] && echo "the package and the bundle install the same xwrt"
exit "$fail"
