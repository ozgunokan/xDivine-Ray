package daemon

// Every failure the daemon reports, in one place.
//
// The reason for a catalog rather than sentences written where they are raised
// is that these sentences have to exist twice: once in English, which is what
// goes to syslog and into a bug report, and once in the reader's language,
// which is what appears on the screen. Keeping them in one map means the
// English text and the key the interface translates by cannot drift apart —
// there is only one of each, and a test refuses to build a release where a key
// here has no sentence on the other side.
//
// The keys are the stable part. Rewording a sentence below is free; renaming a
// key is a translation that silently falls back to English, so keys are chosen
// to describe the failure rather than its current wording.
//
// Verbs stay in the present and sentences stay short, because most of these
// are read in a red box next to a hint, not in prose.

// messages are the failures themselves: what went wrong.
//
// A `%w` in a format is an error being wrapped, and it must stay `%w` — it is
// what keeps errors.Is working through the failure. Everything else is `%s` or
// `%d` and is interpolated into the translation the same way.
var messages = map[string]string{
	// --- configuration ---------------------------------------------------
	"config.read":             "read configuration: %w",
	"config.read_startup":     "read configuration at startup: %w",
	"config.nothing_selected": "nothing selected to connect to",
	"config.no_target":        "no profile or group with id %q",
	"config.group_invalid":    "group %s: %w",
	"config.group_empty":      "group %s has no usable members",
	"config.profile_invalid":  "profile %s: %w",
	"config.rundir":           "create run directory %s: %w",
	"config.mode_unknown":     "unknown capture mode %q",
	"config.tproxy_missing":   "mode %q needs TPROXY but this kernel has no tproxy support",
	"update.failed":           "could not install the update: %w",
	// The update check's own failures. They are not logged as faults — a check
	// that could not reach GitHub is not a fault of this device — but they end
	// up on a page, so they are named and translated like everything else.
	"update.bad_source":        "the update source %s is not an owner/name repository",
	"update.unreachable":       "could not reach the release page: %s",
	"update.no_releases":       "%s has no releases, or is not the right repository",
	"update.rate_limited":      "the release page refused the request; too many have been made from this address",
	"update.http":              "the release page answered %s",
	"update.unreadable":        "the release page sent something that could not be read",
	"update.draft":             "the newest release is still a draft",
	"update.no_bundle":         "this release has no package for %s",
	"update.no_checksums":      "this release has no %s file, so a download from it cannot be verified",
	"update.not_listed":        "%s is not listed in %s",
	"update.checksum_mismatch": "%s does not match its checksum; it was deleted rather than installed",
	"update.download_failed":   "could not download %s: %s",
	"update.not_installable":   "%s is out, but it has no verifiable package for %s",
	"config.build":             "build core config: %w",
	"config.render":            "render core config: %w",
	"config.write":             "write core config: %w",
	"config.rejected":          "the core rejected the generated configuration",
	"config.port_in_use":       "port %d is already in use, so the %s cannot start",

	// --- core ------------------------------------------------------------
	"core.exited":             "%w",
	"core.start":              "start core (%s): %w",
	"core.cannot_run":         "cannot run the proxy core at %s: %w",
	"core.exited_immediately": "the core exited immediately after starting",
	"core.not_listening":      "the core did not start listening on port %d within %s",
	"core.no_data":            "the core started but no data passes through the tunnel: %w",

	// --- tunnel ----------------------------------------------------------
	"tunnel.start":          "%w",
	"tunnel.firewall_drops": "the tunnel is up but the firewall would drop everything through it",

	// --- firewall --------------------------------------------------------
	"fw.no_backend": "%w",
	"fw.none_found": "no supported firewall found: install nftables or iptables",
	"fw.needs_ip":   "this mode needs the `ip` command for policy routing, which is not installed: opkg install ip-full (or apk add ip-full)",
	"fw.apply":      "apply capture rules with %s: %w",
	"fw.reapply":    "reapply capture rules after a firewall reload: %w",

	// --- dns and detection -----------------------------------------------
	//
	// The two dns.* names below are not raised here. They are tagged where the
	// failure is decided (internal/mode/dns.go) and adopted by the dns.apply
	// step, which has nothing of its own to say; the same goes for the two
	// fw.* names from internal/fw. They live in this catalog because this is
	// where the interface's translations are checked against, and a name with
	// no sentence here would reach a reader as the name itself.
	"dns.no_dnsmasq":         "this device does not run dnsmasq, which is what dnsmasq DNS mode configures",
	"dns.core_not_answering": "the core is not answering DNS on port %d, so pointing dnsmasq at it would have stopped name resolution for the whole network; the previous resolver configuration was put back: %w",

	"dns.apply":     "%w",
	"detect.no_wan": "no upstream route after 90s, so no auto-connect",
}

// hints are what to do about it. They are separate from the messages because
// they are answers to a different question and are often shared: several
// distinct failures end in "check /etc/config/xwrt for a syntax error".
var hints = map[string]string{
	"hint.config_syntax":  "check /etc/config/xwrt for a syntax error",
	"hint.pick_target":    "pick a server or a group first",
	"hint.group_members":  "add servers to the group, or connect to a single server",
	"hint.mode_values":    "set mode to one of: redirect, mixed, tproxy, tun",
	"hint.tproxy_install": "install kmod-nft-tproxy (or kmod-ipt-tproxy), or switch to mixed mode, which carries UDP through the TUN device instead",

	"hint.core_restarting": "the core is restarted automatically; if it keeps exiting, check the lines above for the setting it rejected",
	"hint.install_xray":    "install the xray package, or set xray_bin to its path",
	"hint.install_xray_at": "install the xray-core package, or point xray_bin at the binary if it is somewhere else",
	"hint.core_last_lines": "the core's own last lines above say why; it is being restarted in the background, so a fix takes effect on reconnect",
	"hint.core_no_port":    "the core started but never opened its port; the lines above are its own output",
	"hint.rejected_config": "the rejected config is at %s; the core's own message above names the setting it would not accept",
	"hint.port_change":     "change %s in the settings, or stop whatever is listening on %s",

	// The pinned-certificate hint is the one that costs an evening when it is
	// missing: nothing else about this failure looks different from any other.
	"hint.pinned_cert":       "this profile pins the server's certificate; if the server has renewed it, the pin no longer matches and every connection is refused. Run `xwrt fetch-cert %s` to read the current certificate, then connect again. Otherwise check that the account is still valid and that the server is up",
	"hint.group_no_data":     "no member of this group could carry data; check that the accounts are still valid and the servers are up",
	"hint.check_credentials": "check that the credentials are still valid and the server is up; the core's own last lines above usually name the reason",

	"hint.tun_kmod":         "install kmod-tun",
	"hint.tun_hev":          "install hev-socks5-tunnel, or use redirect mode, which needs no tunnel",
	"hint.tun_no_device":    "hev-socks5-tunnel started but never created the device; check the lines above and that /dev/net/tun is usable",
	"hint.tun_ip_full":      "install the ip-full package",
	"hint.tunnel_firewall":  "fix the firewall as above, or switch the capture mode to mixed, where TCP does not depend on this",
	"hint.fw_install_tools": "install nftables (fw4) or iptables (fw3) command-line tools",
	"hint.fw_rolled_back":   "the rules were rolled back, so the device keeps working unproxied; the message above names the rule the kernel refused",
	"hint.fw_reconnect":     "traffic is no longer being captured; reconnect from the web interface or run `xwrt connect`",
	"hint.dns_modes":        "set dns_mode to 'redirect' to hijack port 53 in the firewall instead, or to 'off' to leave DNS alone",
	"hint.wan_keeps_trying": "the WAN interface has no gateway yet; xwrt keeps trying and connects as soon as the upstream is up",
}
