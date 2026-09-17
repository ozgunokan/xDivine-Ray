package ucicfg

import (
	"fmt"
	"sort"
)

// A hand-edited /etc/config/xwrt with a misspelled option used to be silently
// ignored: the value simply never arrived, and the resulting behaviour — a
// profile connecting without its uTLS fingerprint, say — looked like a bug in
// the daemon rather than a typo in the file. Naming the unknown options turns
// that into a one-line answer.
//
// The option names here are short because they mirror the share-link
// parameters (`net`, `fp`, `pbk`, `sid`), which is exactly why someone writing
// the file by hand reaches for the long spelling instead. Hence the alias table:
// the common wrong guesses get told what the right name is.

var knownOptions = map[string]map[string]bool{
	typeMain: set(
		"enabled", "active", "active_kind", "mode",
		"socks_port", "http_port", "tproxy_port", "dns_port", "stats_port", "api_port",
		"dns", "dns_mode", "log_level", "allow_lan", "auto_connect", "ipv6",
		"proxy_router", "xray_bin", "hev_bin", "run_dir",
		"tun_name", "tun_addr", "tun_mask", "tun_mtu",
		"fwmark", "route_table", "lan_device", "wan_device",
		"bypass_ip", "bypass_mac",
	),
	typeProfile: set(
		"name", "proto", "address", "port", "uuid", "password", "method", "alter_id",
		"encryption", "flow", "net", "security", "sni", "alpn", "fp",
		"pbk", "sid", "spx", "allow_insecure", "pinned_cert",
		"path", "host", "service_name", "header_type", "seed",
		"quic_security", "quic_key", "mux", "sub", "remark",
	),
	typeGroup: set("name", "strategy", "member", "probe_url", "probe_interval"),
	typeRule: set(
		"name", "enabled", "action", "domain", "ip", "source",
		"protocol", "port", "source_port", "network",
	),
	typeSub: set("name", "url", "updated", "count"),
}

// optionAliases maps a plausible wrong name to the right one.
var optionAliases = map[string]string{
	"network":          "net",
	"transport":        "net",
	"type":             "net",
	"fingerprint":      "fp",
	"public_key":       "pbk",
	"publickey":        "pbk",
	"short_id":         "sid",
	"shortid":          "sid",
	"spider_x":         "spx",
	"insecure":         "allow_insecure",
	"skip_cert":        "allow_insecure",
	"pin":              "pinned_cert",
	"pinned":           "pinned_cert",
	"cert":             "pinned_cert",
	"server":           "address",
	"host_port":        "port",
	"members":          "member",
	"domains":          "domain",
	"ips":              "ip",
	"sources":          "source",
	"protocols":        "protocol",
	"src":              "source",
	"lan":              "lan_device",
	"wan":              "wan_device",
	"mark":             "fwmark",
	"table":            "route_table",
	"loglevel":         "log_level",
	"log":              "log_level",
	"xray":             "xray_bin",
	"hev":              "hev_bin",
	"bypass":           "bypass_ip",
	"bypass_ips":       "bypass_ip",
	"bypass_macs":      "bypass_mac",
	"probe":            "probe_url",
	"probeinterval":    "probe_interval",
	"auto":             "auto_connect",
	"autoconnect":      "auto_connect",
	"proxy_the_router": "proxy_router",
}

// checkUnknownOptions reports options the loader does not read, so a typo in a
// hand-edited file is visible instead of silently doing nothing.
func checkUnknownOptions(pkg *Package) []string {
	var out []string
	for _, sec := range pkg.Sections {
		known, ok := knownOptions[sec.Type]
		if !ok {
			out = append(out, fmt.Sprintf("section type %q is not one xwrt uses "+
				"(expected one of: %s)", sec.Type, "xwrt, profile, group, rule, subscription"))
			continue
		}

		names := make([]string, 0, len(sec.Options)+len(sec.Lists))
		for opt := range sec.Options {
			names = append(names, opt)
		}
		for opt := range sec.Lists {
			if _, dup := sec.Options[opt]; !dup {
				names = append(names, opt)
			}
		}
		// Sorted so the message is stable rather than map-order dependent.
		sort.Strings(names)

		for _, opt := range names {
			if known[opt] {
				continue
			}
			msg := fmt.Sprintf("%s %q: option %q is not recognised and was ignored",
				sec.Type, sec.Name, opt)
			if alias, hit := optionAliases[opt]; hit && known[alias] {
				msg += fmt.Sprintf(" — did you mean %q?", alias)
			}
			out = append(out, msg)
		}
	}
	return out
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
