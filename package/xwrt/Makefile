#
# OpenWrt package for xwrt (xDivine-Ray).
#
# Two ways to build it.
#
# As a feed, which is what you want for a firmware image — the package then
# appears in menuconfig and is built into the image with everything it needs:
#
#   echo "src-link xwrt /path/to/xwrt" >> feeds.conf.default
#   ./scripts/feeds update xwrt
#   ./scripts/feeds install -a -p xwrt
#   make menuconfig     # Network -> xwrt, and LuCI -> 3. Applications -> luci-app-xwrt
#   make -j$(nproc)
#
# Or against an SDK, for a single .ipk/.apk without rebuilding an image:
#
#   ln -s /path/to/xwrt/package/xwrt  <sdk>/package/xwrt
#   ln -s /path/to/xwrt/luci-app-xwrt <sdk>/package/luci-app-xwrt
#   make package/xwrt/compile V=s
#
# ../../build.sh is the third way and not a replacement for either: it
# cross-compiles static binaries with nothing but Go, for testing on a device
# that already runs OpenWrt.
#
include $(TOPDIR)/rules.mk

PKG_NAME:=xwrt

# The version comes from the VERSION file at the root of the source tree, which
# is also what build.sh and release.sh stamp into the binary. Written here by
# hand it drifted immediately: the package said 0.1.0 while the daemon it
# contained reported 0.23.0, and the only place that difference showed was a
# line of the install report nobody reads twice.
XWRT_SRC:=$(realpath $(CURDIR)/../..)
PKG_VERSION:=$(strip $(shell cat $(XWRT_SRC)/VERSION 2>/dev/null || echo 0.0.0))
PKG_RELEASE:=1

PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE
PKG_MAINTAINER:=Özgün Okan

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
# MIPS16 and Go do not mix.
PKG_USE_MIPS16:=0

GO_PKG:=xwrt
GO_PKG_BUILD_PKG:=xwrt/cmd/...
GO_PKG_LDFLAGS_X:=xwrt/internal/app.Version=$(PKG_VERSION)

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

# What the daemon actually needs at runtime, all of it.
#
# These used to be a paragraph in the description instead — "install these
# yourself, the names differ between feeds" — which is a way of saying that a
# device can be built with xwrt on it and no way to run a tunnel. The names do
# not differ; they are these, in the packages feed, and every one of them is
# needed by a capture mode the interface offers:
#
#   xray-core           the proxy core. Nothing works without it.
#   ip-full             policy routing. BusyBox's ip has no `ip rule`, and
#                       every mode but plain redirect needs one.
#   kmod-tun            the tunnel device, for mixed (the default) and tun.
#   hev-socks5-tunnel   what serves that device.
#   kmod-nft-tproxy     the kernel half of tproxy mode.
#
# To build a smaller image, drop the modes you will not use — but drop them
# from here knowing that choosing one of them in the interface will then fail
# at connect time with an explanation, rather than silently doing nothing.
XWRT_DEPENDS:= \
	+xray-core \
	+ip-full \
	+kmod-tun \
	+hev-socks5-tunnel \
	+kmod-nft-tproxy

define Package/xwrt
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Web Servers/Proxies
  TITLE:=xDivine-Ray - device-independent Xray manager for OpenWrt
  URL:=https://github.com/ozgunokan/xDivine-Ray
  DEPENDS:=$(GO_ARCH_DEPENDS) $(XWRT_DEPENDS)
endef

define Package/xwrt/description
  A transparent proxy manager that works out the device's network layout while
  it runs instead of assuming interface names, so the same build works on any
  OpenWrt router.

  LAN traffic is captured either with firewall rules (nftables/fw4 or
  iptables/fw3, TPROXY where the kernel has it) or through a TUN device served
  by hev-socks5-tunnel. Servers, groups, routing rules and every setting live
  in UCI, and are edited from LuCI or the xwrt command line.

  Needs 128 MB of RAM and 128 MB of writable storage; the daemon says so at
  startup on a device that has less.
endef

define Package/xwrt/conffiles
/etc/config/xwrt
endef

# The Go sources are one directory above this package, so they are copied in
# rather than downloaded. A missing source tree here produces a build that
# fails much later with something unhelpful about a missing package, so it is
# checked first and said plainly.
define Build/Prepare
	@[ -f "$(XWRT_SRC)/go.mod" ] || { \
		echo "xwrt: no go.mod at $(XWRT_SRC)."; \
		echo "xwrt: this Makefile expects to live at <source>/package/xwrt/Makefile,"; \
		echo "xwrt: with the Go sources two directories above it."; \
		exit 1; }
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) $(XWRT_SRC)/go.mod $(PKG_BUILD_DIR)/
	$(CP) $(XWRT_SRC)/cmd $(PKG_BUILD_DIR)/
	$(CP) $(XWRT_SRC)/internal $(PKG_BUILD_DIR)/
endef

define Package/xwrt/install
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/xwrt $(1)/usr/sbin/xwrt
	# One binary, two entry points: invoked as xwrtd it runs the daemon.
	$(LN) xwrt $(1)/usr/sbin/xwrtd

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/xwrt $(1)/etc/init.d/xwrt

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/etc/config/xwrt $(1)/etc/config/xwrt

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/etc/uci-defaults/99-xwrt $(1)/etc/uci-defaults/99-xwrt

	$(INSTALL_DIR) $(1)/etc/hotplug.d/firewall
	$(INSTALL_BIN) ./files/etc/hotplug.d/firewall/99-xwrt $(1)/etc/hotplug.d/firewall/99-xwrt

	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) ./files/usr/libexec/rpcd/xwrt $(1)/usr/libexec/rpcd/xwrt

	# The teardown, which prerm below runs before this package is removed.
	$(INSTALL_DIR) $(1)/usr/libexec
	$(INSTALL_BIN) ./files/usr/libexec/xwrt-teardown $(1)/usr/libexec/xwrt-teardown
endef

define Package/xwrt/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0
# rpcd only learns about a plugin when it starts, so LuCI's calls would all
# fail until something restarted it.
/etc/init.d/rpcd restart >/dev/null 2>&1
exit 0
endef

define Package/xwrt/prerm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0
# Not just a stop. Removing this package while its capture rules are installed
# points every LAN connection at a core that is about to be deleted, and
# nothing is left on the device to explain why the internet stopped. The
# teardown disconnects first, waits for the daemon to really be gone, and takes
# out anything still in the kernel afterwards.
if [ -x /usr/libexec/xwrt-teardown ]; then
	/usr/libexec/xwrt-teardown
else
	/etc/init.d/xwrt stop >/dev/null 2>&1
	/etc/init.d/xwrt disable >/dev/null 2>&1
fi
exit 0
endef

$(eval $(call GoBinPackage,xwrt))
$(eval $(call BuildPackage,xwrt))
