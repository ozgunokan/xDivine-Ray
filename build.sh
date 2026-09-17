#!/bin/sh
# Cross-compile xwrt for every architecture OpenWrt runs on.
#
# This is the fast path for testing on a device: no SDK, no toolchain, just Go.
# The binaries are static and CGO-free, so they run on musl with no runtime
# dependency beyond the kernel.
#
#   ./build.sh              build every target into ./dist
#   ./build.sh aarch64      build one target
#   ./build.sh list         print the target names
#
set -eu

# The VERSION file is the one place the version is written. The Makefiles read
# it too, so a package and the binary inside it cannot disagree.
VERSION="${VERSION:-$(cat "$(dirname "$0")/VERSION" 2>/dev/null || echo 0.0.0)}"
OUT="${OUT:-dist}"

# Target name, GOARCH, and any extra environment.
#
# The supported floor is 128 MB of RAM and 128 MB of writable storage. These
# are the architectures found in devices that meet it; the 32/64 MB classes
# (ath79 and the older ARM cores) are deliberately absent. Architecture alone
# does not decide it, though: a board of any of these types still has to have
# the RAM and the storage, and the daemon checks both at startup.
#
# GOMIPS=softfloat matters: MIPS routers have no usable FPU, and a hardfloat
# binary dies with SIGILL on the first floating point operation.
TARGETS="
aarch64      arm64    -
x86_64       amd64    -
armv7        arm      GOARM=7
mipsel       mipsle   GOMIPS=softfloat
mips64el     mips64le GOMIPS64=softfloat
riscv64      riscv64  -
"

names() {
	echo "$TARGETS" | awk 'NF {print $1}'
}

WANT="${1:-all}"

case "$WANT" in
	list)
		names
		exit 0
		;;
	all) ;;
	*)
		if ! names | grep -qx "$WANT"; then
			echo "unknown target: $WANT" >&2
			echo "run './build.sh list' to see the available targets" >&2
			exit 1
		fi
		;;
esac

mkdir -p "$OUT"

# -s -w strips the symbol table and DWARF. Flash is still the tighter budget
# even on well-specified devices, since xray-core sits next to this binary.
LDFLAGS="-s -w -X xwrt/internal/app.Version=${VERSION}"

echo "$TARGETS" | while read -r name goarch extra; do
	[ -n "${name:-}" ] || continue
	if [ "$WANT" != "all" ] && [ "$WANT" != "$name" ]; then
		continue
	fi
	[ "$extra" = "-" ] && extra=""

	dir="$OUT/$name"
	mkdir -p "$dir"

	echo "==> $name (GOARCH=$goarch $extra)"
	# One binary serves as both daemon and CLI; xwrtd is a symlink to it.
	# shellcheck disable=SC2086
	env CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" $extra \
		go build -trimpath -ldflags "$LDFLAGS" -o "$dir/xwrt" ./cmd/xwrt
	ls -l "$dir" | awk 'NR>1 {printf "    %-8s %8s bytes\n", $9, $5}'
done

# What version these binaries were stamped with, written where the packaging
# step can read it. Without this, release.sh has no way to tell a fresh build
# from one left over from last week, and the number on the tarball is just a
# label somebody typed.
echo "$VERSION" > "$OUT/VERSION"

echo
echo "Binaries are in $OUT/ (version $VERSION)."
echo "Install on a router with:"
echo "  scp $OUT/<target>/xwrt root@192.168.1.1:/usr/sbin/xwrt"
echo "  scp -r package/xwrt/files/* root@192.168.1.1:/"
echo "  ssh root@192.168.1.1 'ln -sf /usr/sbin/xwrt /usr/sbin/xwrtd; \\"
echo "     chmod +x /usr/sbin/xwrt /etc/init.d/xwrt /usr/libexec/rpcd/xwrt \\"
echo "       /etc/uci-defaults/99-xwrt /etc/hotplug.d/firewall/99-xwrt; \\"
echo "     sh /etc/uci-defaults/99-xwrt; /etc/init.d/rpcd restart; \\"
echo "     /etc/init.d/xwrt enable; /etc/init.d/xwrt start'"
