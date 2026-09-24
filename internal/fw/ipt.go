package fw

import (
	"fmt"
	"strconv"
)

// Chain names owned by this daemon. Everything lives in dedicated chains so
// reverting never has to touch a rule fw3 or the user created.
const (
	chainPre  = "XWRT_PRE"
	chainMark = "XWRT_MARK"
	chainOut  = "XWRT_OUT"
	chainQUIC = "XWRT_QUIC"
)

// iptBackend targets OpenWrt 21.02 and older, where fw3 drives iptables.
type iptBackend struct{}

func (b *iptBackend) Name() string { return "iptables" }

func (b *iptBackend) Apply(p Plan) error {
	// Start from a clean slate: a previous connect may have left rules behind.
	b.Revert()

	if !p.Mode.NeedsFirewallCapture() {
		// TUN mode needs no capture rules; routing carries the traffic and
		// forwarding is handled by the firewall zone the TUN device is in.
		// DNS is the exception: hijacking port 53 is a firewall rule, and
		// without it clients keep resolving through whatever resolver they
		// were handed, which for a router means the ISP's servers reached
		// from the far end of the tunnel, where they do not answer.
		if p.RedirectDNS {
			return b.applyDNSOnly(p)
		}
		return nil
	}

	if p.Mode.CapturesTCPViaRedirect() {
		if err := b.applyNAT(p); err != nil {
			return err
		}
	}
	if p.Mode.MarksUDPForTun() {
		if err := b.applyUDPMark(p); err != nil {
			return err
		}
	}
	if p.Mode.NeedsTProxy() {
		if err := requireIP(); err != nil {
			return err
		}
		if err := b.applyTProxy(p); err != nil {
			return err
		}
		if err := addTProxyRouting(p.MarkTProxy); err != nil {
			b.Revert()
			return err
		}
	}
	if p.ProxyRouter {
		if err := b.applyOutput(p); err != nil {
			return err
		}
	}
	if p.BlockQUIC && !p.Mode.CarriesUDP() {
		if err := b.applyQUICBlock(p); err != nil {
			return err
		}
	}
	return nil
}

// applyQUICBlock refuses QUIC from the LAN in the one mode that cannot carry
// it. See the nftables backend's writeQUICChain for why this exists and why it
// is a refusal rather than a drop.
func (b *iptBackend) applyQUICBlock(p Plan) error {
	if err := b.newChain("filter", chainQUIC, p); err != nil {
		return err
	}
	if err := runCmd("iptables", "-t", "filter", "-A", chainQUIC,
		"-p", "udp", "--dport", "443",
		"-j", "REJECT", "--reject-with", "icmp-port-unreachable"); err != nil {
		return err
	}
	return b.hook("filter", "FORWARD", chainQUIC, p)
}

// applyNAT redirects TCP, and DNS for both protocols, into local ports.
func (b *iptBackend) applyNAT(p Plan) error {
	tp := strconv.Itoa(p.TProxyPort)
	dnsPort := strconv.Itoa(p.DNSPort)

	if err := b.newChain("nat", chainPre, p); err != nil {
		return err
	}
	if p.RedirectDNS {
		for _, proto := range []string{"udp", "tcp"} {
			if err := runCmd("iptables", "-t", "nat", "-A", chainPre,
				"-p", proto, "--dport", "53", "-j", "REDIRECT", "--to-ports", dnsPort); err != nil {
				return err
			}
		}
	}
	if err := runCmd("iptables", "-t", "nat", "-A", chainPre,
		"-p", "tcp", "-j", "REDIRECT", "--to-ports", tp); err != nil {
		return err
	}
	return b.hook("nat", "PREROUTING", chainPre, p)
}

// applyDNSOnly installs TUN mode's single firewall rule: send port 53 to the
// core's resolver. Everything else is carried by routing.
func (b *iptBackend) applyDNSOnly(p Plan) error {
	dnsPort := strconv.Itoa(p.DNSPort)

	if err := b.newChain("nat", chainPre, p); err != nil {
		return err
	}
	for _, proto := range []string{"udp", "tcp"} {
		if err := runCmd("iptables", "-t", "nat", "-A", chainPre,
			"-p", proto, "--dport", "53", "-j", "REDIRECT", "--to-ports", dnsPort); err != nil {
			return err
		}
	}
	return b.hook("nat", "PREROUTING", chainPre, p)
}

// applyUDPMark is mixed mode's UDP half: mark it so policy routing sends it
// into the TUN device. Nothing is rewritten here.
func (b *iptBackend) applyUDPMark(p Plan) error {
	mark := fmt.Sprintf("0x%x", p.MarkTunUDP)

	if err := b.newChain("mangle", chainMark, p); err != nil {
		return err
	}
	if p.RedirectDNS {
		// DNS is redirected to a local port by the nat chain; marking it would
		// send it out of the TUN instead of to the local socket.
		for _, proto := range []string{"udp", "tcp"} {
			if err := runCmd("iptables", "-t", "mangle", "-A", chainMark,
				"-p", proto, "--dport", "53", "-j", "RETURN"); err != nil {
				return err
			}
		}
	}
	if err := runCmd("iptables", "-t", "mangle", "-A", chainMark,
		"-p", "udp", "-j", "MARK", "--set-mark", mark); err != nil {
		return err
	}
	return b.hook("mangle", "PREROUTING", chainMark, p)
}

// applyTProxy implements tproxy mode for both protocols.
func (b *iptBackend) applyTProxy(p Plan) error {
	tp := strconv.Itoa(p.TProxyPort)
	dnsPort := strconv.Itoa(p.DNSPort)
	tmark := fmt.Sprintf("0x%x", p.MarkTProxy)

	if err := b.newChain("mangle", chainPre, p); err != nil {
		return err
	}
	if p.RedirectDNS {
		for _, proto := range []string{"udp", "tcp"} {
			if err := runCmd("iptables", "-t", "mangle", "-A", chainPre,
				"-p", proto, "--dport", "53", "-j", "TPROXY",
				"--on-port", dnsPort, "--tproxy-mark", tmark); err != nil {
				return err
			}
		}
	}
	for _, proto := range []string{"tcp", "udp"} {
		if err := runCmd("iptables", "-t", "mangle", "-A", chainPre,
			"-p", proto, "-j", "TPROXY", "--on-port", tp, "--tproxy-mark", tmark); err != nil {
			return err
		}
	}
	return b.hook("mangle", "PREROUTING", chainPre, p)
}

// applyOutput captures traffic the router itself originates. The core's own
// mark is checked first, or its upstream connections would loop.
func (b *iptBackend) applyOutput(p Plan) error {
	mark := fmt.Sprintf("0x%x", p.Mark)

	if p.Mode.CapturesTCPViaRedirect() || p.Mode.NeedsTProxy() {
		if err := runCmd("iptables", "-t", "nat", "-N", chainOut); err != nil {
			return err
		}
		if err := runCmd("iptables", "-t", "nat", "-A", chainOut,
			"-m", "mark", "--mark", mark, "-j", "RETURN"); err != nil {
			return err
		}
		for _, cidr := range p.AllBypass() {
			if err := runCmd("iptables", "-t", "nat", "-A", chainOut,
				"-d", cidr, "-j", "RETURN"); err != nil {
				return err
			}
		}
		if err := runCmd("iptables", "-t", "nat", "-A", chainOut,
			"-p", "tcp", "-j", "REDIRECT", "--to-ports", strconv.Itoa(p.TProxyPort)); err != nil {
			return err
		}
		if err := runCmd("iptables", "-t", "nat", "-I", "OUTPUT", "1", "-j", chainOut); err != nil {
			return err
		}
	}

	if p.Mode.MarksUDPForTun() {
		if err := runCmd("iptables", "-t", "mangle", "-N", chainOut); err != nil {
			return err
		}
		if err := runCmd("iptables", "-t", "mangle", "-A", chainOut,
			"-m", "mark", "--mark", mark, "-j", "RETURN"); err != nil {
			return err
		}
		for _, cidr := range p.AllBypass() {
			if err := runCmd("iptables", "-t", "mangle", "-A", chainOut,
				"-d", cidr, "-j", "RETURN"); err != nil {
				return err
			}
		}
		if p.RedirectDNS {
			if err := runCmd("iptables", "-t", "mangle", "-A", chainOut,
				"-p", "udp", "--dport", "53", "-j", "RETURN"); err != nil {
				return err
			}
		}
		if err := runCmd("iptables", "-t", "mangle", "-A", chainOut,
			"-p", "udp", "-j", "MARK", "--set-mark",
			fmt.Sprintf("0x%x", p.MarkTunUDP)); err != nil {
			return err
		}
		if err := runCmd("iptables", "-t", "mangle", "-I", "OUTPUT", "1", "-j", chainOut); err != nil {
			return err
		}
	}
	return nil
}

// newChain creates a chain and installs the bypass guards every capture chain
// shares.
func (b *iptBackend) newChain(table, chain string, p Plan) error {
	if err := runCmd("iptables", "-t", table, "-N", chain); err != nil {
		return err
	}
	for _, mac := range p.BypassMACs {
		if err := runCmd("iptables", "-t", table, "-A", chain,
			"-m", "mac", "--mac-source", mac, "-j", "RETURN"); err != nil {
			return err
		}
	}
	for _, cidr := range p.AllBypass() {
		if err := runCmd("iptables", "-t", table, "-A", chain,
			"-d", cidr, "-j", "RETURN"); err != nil {
			return err
		}
	}
	return nil
}

// hook jumps into the chain once per client-facing device. Inserting at the top
// keeps the capture ahead of whatever fw3 put in the hook.
func (b *iptBackend) hook(table, hookChain, chain string, p Plan) error {
	if len(p.LANDevices) > 0 {
		for _, dev := range p.LANDevices {
			if err := runCmd("iptables", "-t", table, "-I", hookChain, "1",
				"-i", dev, "-j", chain); err != nil {
				return err
			}
		}
		return nil
	}
	if p.WANDevice != "" {
		return runCmd("iptables", "-t", table, "-I", hookChain, "1",
			"!", "-i", p.WANDevice, "-j", chain)
	}
	return runCmd("iptables", "-t", table, "-I", hookChain, "1", "-j", chain)
}

func (b *iptBackend) Revert() error {
	// The filter table is in this list because of the QUIC block. Leaving it
	// behind would be the worst kind of leftover: the tunnel is off, nothing
	// is running, and every client on the network silently loses QUIC with no
	// process left to blame.
	for _, table := range []string{"nat", "mangle", "filter"} {
		// A jump may have been installed once per LAN device, so delete
		// repeatedly until iptables reports there is nothing left.
		for _, hook := range []string{"PREROUTING", "OUTPUT", "FORWARD"} {
			for _, chain := range []string{chainPre, chainMark, chainOut, chainQUIC} {
				for i := 0; i < 8; i++ {
					if err := deleteJump(table, hook, chain); err != nil {
						break
					}
				}
			}
		}
		for _, chain := range []string{chainPre, chainMark, chainOut, chainQUIC} {
			runCmdQuiet("iptables", "-t", table, "-F", chain)
			runCmdQuiet("iptables", "-t", table, "-X", chain)
		}
	}
	delTProxyRouting()
	return nil
}

// deleteJump removes one jump rule, whatever match options it carried. The
// rule number is resolved first because the original -i/! -i options are not
// known here.
func deleteJump(table, hook, chain string) error {
	out, err := runCmdOutput("iptables", "-t", table, "-L", hook, "--line-numbers", "-n")
	if err != nil {
		return err
	}
	num := findRuleNumber(out, chain)
	if num == "" {
		return fmt.Errorf("no jump to %s in %s/%s", chain, table, hook)
	}
	return runCmd("iptables", "-t", table, "-D", hook, num)
}

func findRuleNumber(listing, chain string) string {
	for _, line := range splitLines(listing) {
		fields := fieldsOf(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == chain {
			if _, err := strconv.Atoi(fields[0]); err == nil {
				return fields[0]
			}
		}
	}
	return ""
}

func (b *iptBackend) Installed() bool {
	for _, table := range []string{"nat", "mangle"} {
		for _, chain := range []string{chainPre, chainMark} {
			if err := runCmd("iptables", "-t", table, "-L", chain, "-n"); err == nil {
				return true
			}
		}
	}
	return false
}
