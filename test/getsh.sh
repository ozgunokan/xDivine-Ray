#!/bin/sh
# The one-line installer's decisions, without six routers.
#
# get.sh works out which bundle a device needs, and the cost of getting that
# wrong is a binary that will not run — which the installer does catch, by
# running it before installing anything, but catching it there means a person
# has already waited through a download. The table is worth testing.
#
# The part that matters most is MIPS. `uname -m` says "mips" for both
# endiannesses, and a little-endian binary on a big-endian router is a device
# that does not come back. So the endianness comes from OpenWrt where it can,
# and from an ELF header where it cannot.
#
#   sh test/getsh.sh
cd "$(dirname "$0")/.."

GETSH_DRYRUN=1
export GETSH_DRYRUN
. ./get.sh

fail=0
check() {
	name="$1"; want="$2"; shift 2
	got=$(arch_for "$@" 2>/dev/null) || got="<refused>"
	if [ "$got" = "$want" ]; then
		echo "ok   $name"
	else
		echo "FAIL $name: wanted $want, got $got"
		fail=1
	fi
}

echo "-- what OpenWrt says, which is the answer whenever it is there"
check "aarch64 router"      aarch64  "aarch64_cortex-a53" ""
check "ipq807x router"      aarch64  "aarch64_cortex-a53" "aarch64"
check "x86 box"             x86_64   "x86_64" "x86_64"
check "armv7 router"        armv7    "arm_cortex-a7_neon-vfpv4" "armv7l"
check "little-endian mips"  mipsel   "mipsel_24kc" "mips"
check "mips64 router"       mips64el "mips64el_octeonplus" "mips64"
check "riscv64 board"       riscv64  "riscv64_riscv64" "riscv64"

echo
echo "-- and the ones with no bundle, which must be refused rather than guessed"
check "big-endian mips"     "<refused>" "mips_24kc" "mips"
check "big-endian mips64"   "<refused>" "mips64_octeonplus" "mips64"

echo
echo "-- no DISTRIB_ARCH: the machine name, and an ELF header for endianness"
check "aarch64 by uname"    aarch64  "" "aarch64"
check "x86_64 by uname"     x86_64   "" "x86_64"
check "armv7 by uname"      armv7    "" "armv7l"

# A 64-bit little-endian ELF header is bytes 7f 45 4c 46 02 01; a 32-bit
# big-endian one is 7f 45 4c 46 01 02. Only bytes 4 and 5 are read.
elf() { printf '\177ELF%b%b\0\0\0\0\0\0\0\0\0\0' "$1" "$2" > "$3"; }
tmp="${TMPDIR:-/tmp}/getsh.$$"
mkdir -p "$tmp"
elf '\001' '\001' "$tmp/mips32le"
elf '\002' '\001' "$tmp/mips64le"
elf '\001' '\002' "$tmp/mips32be"

check "mips, LE by ELF"     mipsel      "" "mips" "$tmp/mips32le"
check "mips, 64-bit LE"     mips64el    "" "mips" "$tmp/mips64le"
check "mips, BE by ELF"     "<refused>" "" "mips" "$tmp/mips32be"
rm -rf "$tmp"

echo
echo "-- a machine nobody builds for is named, not guessed at"
check "something else"      "<refused>" "" "ppc64"
check "nothing known"       "<refused>" "" "unknown"

echo
echo "-- reading DISTRIB_ARCH out of the file OpenWrt writes"
tmp="${TMPDIR:-/tmp}/getsh-rel.$$"
mkdir -p "$tmp"
cat > "$tmp/openwrt_release" <<'EOF'
DISTRIB_ID='OpenWrt'
DISTRIB_RELEASE='25.12'
DISTRIB_ARCH='aarch64_cortex-a53'
DISTRIB_TARGET='qualcommax/ipq807x'
EOF
got=$(read_distrib_arch "$tmp/openwrt_release")
if [ "$got" = "aarch64_cortex-a53" ]; then
	echo "ok   the quoted value is read"
else
	echo "FAIL the quoted value is read: got '$got'"
	fail=1
fi
# A device with no such file must produce nothing, not an error and not a
# stray line that then fails to match anything.
got=$(read_distrib_arch "$tmp/does-not-exist")
if [ -z "$got" ]; then
	echo "ok   a missing file reads as nothing"
else
	echo "FAIL a missing file read as '$got'"
	fail=1
fi
rm -rf "$tmp"

echo
echo "-- and then the whole of it, against a bundle on disk"
#
# The decisions above are half the job. The other half is the sequence: fetch,
# check the fetch against what the release says, unpack, and prove the binary
# runs before a single file is copied anywhere. A router is usually reachable
# only through the tunnel this installs, so the order is the safety.
#
# The bundle here is a stand-in with the same shape, and its install.sh only
# says it ran: what is being tested is get.sh, not what it hands over to.

# The whole file was sourced above with this set, and it is exported, so
# without clearing it every get.sh below would stop at the same line.
GETSH_DRYRUN=0
export GETSH_DRYRUN

root="${TMPDIR:-/tmp}/getsh-e2e.$$"
mkdir -p "$root/serve" "$root/build/xwrt-testarch/usr/sbin"
cat > "$root/build/xwrt-testarch/usr/sbin/xwrt" <<'EOF'
#!/bin/sh
[ "$1" = "--version" ] && { echo "xwrt 9.9.9 (test)"; exit 0; }
exit 1
EOF
cat > "$root/build/xwrt-testarch/install.sh" <<'EOF'
#!/bin/sh
echo "INSTALL RAN"
EOF
chmod 755 "$root/build/xwrt-testarch/usr/sbin/xwrt" "$root/build/xwrt-testarch/install.sh"
(cd "$root/build" && tar czf "$root/serve/xwrt-testarch.tar.gz" xwrt-testarch)
(cd "$root/serve" && sha256sum xwrt-testarch.tar.gz > sha256sums)

out=$(ARCH=testarch BASE="file://$root/serve" sh ./get.sh 2>&1) && status=0 || status=$?
case "$out" in
	*"INSTALL RAN"*)
		echo "ok   a good bundle is fetched, checked, unpacked and handed over" ;;
	*)
		echo "FAIL the install never ran:"; echo "$out" | sed 's/^/     /'; fail=1 ;;
esac
case "$out" in
	*"9.9.9"*) echo "ok   and the binary was run before anything was installed" ;;
	*) echo "FAIL the binary was never checked"; fail=1 ;;
esac

# The check that earns the rest of it. A bundle that does not match what the
# release vouches for must not be unpacked, whatever the reason.
printf 'tampered' >> "$root/serve/xwrt-testarch.tar.gz"
out=$(ARCH=testarch BASE="file://$root/serve" sh ./get.sh 2>&1) && status=0 || status=$?
case "$out$status" in
	*"does not match"*) echo "ok   a tampered bundle is refused" ;;
	*) echo "FAIL a tampered bundle was accepted:"; echo "$out" | sed 's/^/     /'; fail=1 ;;
esac
case "$out" in
	*"INSTALL RAN"*) echo "FAIL it installed the tampered bundle anyway"; fail=1 ;;
	*) echo "ok   and nothing was installed from it" ;;
esac

# And a release that has no bundle for this device says so, rather than
# installing something that will not run.
rm -f "$root/serve/xwrt-testarch.tar.gz"
out=$(ARCH=testarch BASE="file://$root/serve" sh ./get.sh 2>&1) || true
case "$out" in
	*ERROR*) echo "ok   a missing bundle is an error, not a half-install" ;;
	*) echo "FAIL a missing bundle was not reported"; fail=1 ;;
esac

rm -rf "$root"

echo
if [ "$fail" = 0 ]; then
	echo "ok   the installer fetches the bundle this device can run, and proves it"
else
	echo "FAIL the installer would fetch the wrong bundle"
fi
exit "$fail"
