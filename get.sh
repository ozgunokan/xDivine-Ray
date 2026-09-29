#!/bin/sh
# Install xDivine-Ray on this router, over the network.
#
#   wget -qO- https://raw.githubusercontent.com/ozgunokan/xDivine-Ray/main/get.sh | sh
#
# Not called install.sh, because every release bundle contains one of those and
# they do different jobs: that one installs an unpacked bundle sitting in front
# of it, this one works out which bundle this device needs, fetches it from the
# release page, proves it is the file the release vouches for, and then hands
# over to the one inside it.
#
# It is deliberately suspicious of itself. A router is usually reached only
# through the connection this software manages, so the failure that matters is
# not "the install did not work" — it is an install that half worked on a box
# nobody can log into any more. Everything that can be checked is checked
# before anything is written: the architecture, the download, its checksum, and
# finally the binary itself, which is run once from the unpacked bundle before
# a single file is copied anywhere. A binary built for the wrong instruction
# set dies at that point, in a scratch directory, having changed nothing.
#
#   VERSION=1.0.22 sh get.sh     install a particular release, not the latest
#   ARCH=mipsel    sh get.sh     override the detected architecture
#   REPO=me/fork   sh get.sh     install from a fork
#   BASE=<url>     sh get.sh     take the bundle and sha256sums from there
set -e

REPO="${REPO:-ozgunokan/xDivine-Ray}"

say()  { echo "$@"; }
die()  { echo "" >&2; echo "ERROR: $*" >&2; exit 1; }

# --- which architecture ------------------------------------------------------
#
# OpenWrt knows the answer and writes it down, which is better than guessing:
# `uname -m` says "mips" for both endiannesses, and a little-endian bundle on a
# big-endian router is a binary that will not run.

read_distrib_arch() {
	file="${1:-/etc/openwrt_release}"
	[ -r "$file" ] || return 0
	sed -n "s/^DISTRIB_ARCH='\{0,1\}\([^']*\)'\{0,1\}/\1/p" "$file" | head -1
}

# arch_for is the whole decision, as a function of what was read rather than of
# what is on this device, so that every branch of it can be exercised without
# six routers. Arguments: what OpenWrt says, what uname says, and a file to
# read an ELF header out of when neither settles the endianness.
arch_for() {
	openwrt_arch="$1"
	machine="${2:-unknown}"
	probe="${3:-/bin/busybox}"

	case "$openwrt_arch" in
		aarch64*)   echo aarch64  ; return ;;
		x86_64*)    echo x86_64   ; return ;;
		arm_*)      echo armv7    ; return ;;
		mipsel_*)   echo mipsel   ; return ;;
		mips64el_*) echo mips64el ; return ;;
		riscv64*)   echo riscv64  ; return ;;
		mips_*|mips64_*)
			die "big-endian MIPS ($openwrt_arch) has no bundle." ;;
	esac

	# No DISTRIB_ARCH: fall back to the machine name, and for MIPS read the
	# endianness out of an ELF header, since that is the one thing uname will
	# not say. Byte 4 of an ELF file is 1 for 32-bit and 2 for 64; byte 5 is 1
	# for little-endian and 2 for big.
	case "$machine" in
		aarch64|arm64) echo aarch64 ; return ;;
		x86_64|amd64)  echo x86_64  ; return ;;
		armv7*|armv8l) echo armv7   ; return ;;
		riscv64)       echo riscv64 ; return ;;
		mips*)
			[ -f "$probe" ] || probe=/bin/sh
			endian=$(od -An -t u1 -j 5 -N 1 "$probe" 2>/dev/null | tr -d ' ')
			[ "$endian" = 1 ] ||
				die "this looks like big-endian MIPS, which has no bundle."
			bits=$(od -An -t u1 -j 4 -N 1 "$probe" 2>/dev/null | tr -d ' ')
			[ "$bits" = 2 ] && { echo mips64el; return; }
			echo mipsel
			return ;;
	esac

	die "no bundle for this machine ($machine).
Built: aarch64, x86_64, armv7, mipsel, mips64el, riscv64.
If one of those is right, run it again with ARCH=<name> sh get.sh"
}

detect_arch() {
	arch_for "$(read_distrib_arch)" "$(uname -m 2>/dev/null || echo unknown)"
}

# --- a way to fetch ----------------------------------------------------------

have() { command -v "$1" >/dev/null 2>&1; }

fetch() {
	url="$1"; out="$2"
	if have curl; then
		curl -fsSL --retry 2 -o "$out" "$url" && return 0
	elif have wget; then
		# Covers both GNU wget and OpenWrt's uclient-fetch, which take -O -q.
		wget -q -O "$out" "$url" && return 0
	elif have uclient-fetch; then
		uclient-fetch -q -O "$out" "$url" && return 0
	else
		die "no curl and no wget on this device."
	fi
	return 1
}

fetch_or_explain() {
	fetch "$1" "$2" && return 0
	# The commonest reason by a distance, and the one whose error message says
	# least: an https fetch on a router with no certificate store.
	if [ ! -s /etc/ssl/certs/ca-certificates.crt ] &&
	   [ ! -s /etc/ssl/cert.pem ]; then
		die "could not fetch $1

This device has no certificate store, so https fetches fail. Install one:
    apk add ca-bundle libustream-openssl     (OpenWrt 24.10 and newer)
    opkg update && opkg install ca-bundle libustream-openssl"
	fi
	die "could not fetch $1
Check that this device has a working internet connection."
}

# --- go ----------------------------------------------------------------------

# test/getsh.sh sources this file to exercise the decisions above without
# installing anything. Everything below here touches the network and the disk.
if [ "${GETSH_DRYRUN:-0}" = 1 ]; then
	return 0 2>/dev/null || exit 0
fi

ARCH="${ARCH:-$(detect_arch)}"
BUNDLE="xwrt-$ARCH.tar.gz"

if [ -n "${BASE:-}" ]; then
	# A mirror, or a directory of bundles reached some other way. Also how
	# test/getsh.sh exercises everything below without a network.
	base="$BASE"
	say "==> xDivine-Ray from $base for $ARCH"
elif [ -n "${VERSION:-}" ]; then
	base="https://github.com/$REPO/releases/download/v${VERSION#v}"
	say "==> xDivine-Ray v${VERSION#v} for $ARCH"
else
	# GitHub redirects this to whatever the latest release is, which saves
	# asking an API that rate-limits unauthenticated callers.
	base="https://github.com/$REPO/releases/latest/download"
	say "==> xDivine-Ray (latest release) for $ARCH"
fi

# /tmp is RAM on a router. The bundle is a few megabytes and the unpacked copy
# a few more, so this checks rather than assuming: a device that fills its tmpfs
# mid-install is a device that has just lost its ability to resolve names.
TMP="${TMPDIR:-/tmp}"
free_kb=$(df -k "$TMP" 2>/dev/null | awk 'NR==2 {print $4}')
case "$free_kb" in
	''|*[!0-9]*) ;;
	*) [ "$free_kb" -lt 20480 ] &&
		die "only ${free_kb}K free in $TMP; about 20M is needed to unpack." ;;
esac

work="$TMP/xwrt-get.$$"
rm -rf "$work"
mkdir -p "$work"
cleanup() { cd /; rm -rf "$work"; }
trap cleanup EXIT INT TERM
cd "$work"

say "==> downloading $BUNDLE"
fetch_or_explain "$base/$BUNDLE" "$work/$BUNDLE"
[ -s "$work/$BUNDLE" ] || die "the download is empty. Is there a release with a bundle for $ARCH?"

say "==> checking it against the release's checksums"
fetch_or_explain "$base/sha256sums" "$work/sha256sums"

want=$(awk -v f="$BUNDLE" '$2 == f || $2 == "*"f {print $1; exit}' "$work/sha256sums")
[ -n "$want" ] || die "sha256sums does not list $BUNDLE, so nothing vouches for this download."
have sha256sum || die "no sha256sum on this device, so the download cannot be verified."
got=$(sha256sum "$work/$BUNDLE" | awk '{print $1}')
[ "$want" = "$got" ] || die "the download does not match the release's checksum.
    expected $want
    got      $got
Nothing has been installed. Try again; if it happens twice, do not install it."

say "==> unpacking"
tar xzf "$work/$BUNDLE" -C "$work"
dir="$work/xwrt-$ARCH"
[ -f "$dir/install.sh" ] || die "this bundle has no install.sh in it."

# The last check, and the one that catches everything the others cannot: run
# the binary. A bundle for the wrong instruction set, a corrupt extract, a
# musl/glibc surprise — all of them end here, in a scratch directory, with
# nothing on this device touched yet.
say "==> checking the binary runs on this device"
if ! ver=$("$dir/usr/sbin/xwrt" --version 2>&1); then
	die "the downloaded binary does not run on this device:
    $ver
It is built for $ARCH. If that is not what this router is, run again with
ARCH=<name> sh get.sh — nothing has been installed."
fi
say "    $ver"

if [ -f /etc/config/xwrt ]; then
	say "==> upgrading; your servers and settings in /etc/config/xwrt are kept"
fi

say ""
cd "$dir"
sh install.sh
