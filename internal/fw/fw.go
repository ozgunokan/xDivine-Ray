// Package fw applies and reverts the packet capture rules.
//
// Two backends are implemented because OpenWrt spans two firewall generations:
// fw4/nftables on 22.03 and newer, fw3/iptables on 21.02 and older. Both
// backends keep every rule inside a namespace of their own (an `xwrt` nft table,
// or `XWRT_*` iptables chains), so teardown is a single delete and nothing the
// device's own firewall owns is ever modified.
package fw

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"xwrt/internal/fault"
	"xwrt/internal/model"
	"xwrt/internal/netenv"
)

// Namespace is the table/chain prefix owned by this daemon.
const Namespace = "xwrt"

// Plan describes the rules to install.
type Plan struct {
	Mode model.Mode

	LANDevices []string
	LANCIDRs   []string
	WANDevice  string
	TunDevice  string

	TProxyPort int
	DNSPort    int

	// The three marks, all derived from one configured base. Mark is what the
	// core sets on its own sockets; MarkTProxy steers TPROXY'd packets into
	// the local table; MarkTunUDP steers mixed-mode UDP into the TUN table.
	Mark       int
	MarkTProxy int
	MarkTunUDP int

	ProxyRouter bool
	RedirectDNS bool

	// BypassCIDRs are destinations that must never be captured. Private ranges
	// are always added on top of whatever the operator configured.
	BypassCIDRs []string
	BypassMACs  []string
}

// privateCIDRs are always excluded so LAN-to-LAN and link-local traffic never
// enters the proxy.
var privateCIDRs = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"224.0.0.0/4",
	"240.0.0.0/4",
}

// AllBypass returns the effective bypass list.
func (p *Plan) AllBypass() []string {
	out := append([]string{}, privateCIDRs...)
	out = append(out, p.LANCIDRs...)
	out = append(out, p.BypassCIDRs...)
	return dedupe(out)
}

// Backend applies a plan to the running system.
type Backend interface {
	// Name identifies the backend in logs and status output.
	Name() string
	// Apply installs the rules, replacing anything this daemon installed before.
	Apply(p Plan) error
	// Revert removes every rule this daemon owns. It is safe to call when
	// nothing is installed.
	Revert() error
	// Installed reports whether this daemon's rules are currently present,
	// which is how a firewall restart that wiped them is noticed.
	Installed() bool
}

// New returns the backend matching the detected firewall stack.
func New(env *netenv.Env) (Backend, error) {
	switch env.Firewall {
	case netenv.FirewallNFT:
		return &nftBackend{}, nil
	case netenv.FirewallIPT:
		return &iptBackend{}, nil
	default:
		return nil, fault.Tagf("fw.none_found", nil,
			"no supported firewall found: install nftables or iptables")
	}
}

// requireIP checks for a usable `ip` before any mode that depends on policy
// routing starts installing rules. BusyBox images often ship without it, and
// "executable file not found" halfway through a connect is a worse message
// than naming the package up front.
func requireIP() error {
	if _, err := exec.LookPath("ip"); err != nil {
		return fault.Tagf("fw.needs_ip", nil,
			"this mode needs the `ip` command for policy routing, "+
				"which is not installed: opkg install ip-full (or apk add ip-full)")
	}
	return nil
}

// runCmd executes a command and returns combined output on failure, which is
// what makes nft and iptables errors readable in the daemon log.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

// runCmdQuiet ignores failures, for teardown steps that are expected to fail
// when the rule was already gone.
func runCmdQuiet(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}

func runCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return string(out), err
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func quoteList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, s := range items {
		quoted = append(quoted, `"`+s+`"`)
	}
	return strings.Join(quoted, ", ")
}

func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

func fieldsOf(s string) []string {
	return strings.Fields(s)
}
