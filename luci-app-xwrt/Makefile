#
# LuCI front-end for xwrt.
#
include $(TOPDIR)/rules.mk

LUCI_TITLE:=xDivine-Ray (LuCI interface)
LUCI_DESCRIPTION:=xDivine-Ray: manage servers, capture mode and routing for the xwrt daemon.
# +xwrt pulls in the daemon and, through it, everything the daemon needs. The
# pages are useless without it: every one of them is a view onto that daemon's
# API, and with nothing answering they show the same "not running" banner.
LUCI_DEPENDS:=+xwrt
LUCI_PKGARCH:=all

PKG_NAME:=luci-app-xwrt
# From the VERSION file at the root of the source tree, the same one the daemon
# is stamped with — so the interface and the daemon cannot claim to be
# different versions of the same thing.
PKG_VERSION:=$(strip $(shell cat $(realpath $(CURDIR)/..)/VERSION 2>/dev/null || echo 0.0.0))
PKG_RELEASE:=1
PKG_LICENSE:=MIT
PKG_MAINTAINER:=Özgün Okan

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
$(eval $(call BuildPackage,$(PKG_NAME)))
