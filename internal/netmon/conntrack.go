// Package netmon reports what is flowing through the router right now.
//
// The source is the kernel's own connection tracking table, which is the only
// place that knows about every flow regardless of whether it is being proxied.
// The proxy core can report its own counters, but it cannot see traffic that
// bypassed it, and it has no notion of which LAN client a connection came
// from — which is usually the question being asked.
package netmon

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// conntrackPath is the kernel's connection table. The nf_conntrack file is
// preferred over the older ip_conntrack name, which no current kernel has.
const conntrackPath = "/proc/net/nf_conntrack"

// acctPath toggles byte and packet accounting. Without it the kernel tracks
// connections but counts nothing, so flows are visible and volumes are not.
const acctPath = "/proc/sys/net/netfilter/nf_conntrack_acct"

// leasesPath is where OpenWrt's dnsmasq records DHCP leases, which is how a
// client address becomes a name someone recognises.
var leasesPaths = []string{
	"/tmp/dhcp.leases",
	"/var/dhcp.leases",
}

// Flow is one tracked connection.
type Flow struct {
	Protocol string `json:"protocol"`
	State    string `json:"state,omitempty"`

	Src   string `json:"src"`
	Dst   string `json:"dst"`
	SPort int    `json:"sport"`
	DPort int    `json:"dport"`

	Hostname string `json:"hostname,omitempty"`

	BytesUp     int64 `json:"bytes_up"`
	BytesDown   int64 `json:"bytes_down"`
	PacketsUp   int64 `json:"packets_up"`
	PacketsDown int64 `json:"packets_down"`

	// Mark is the firewall mark on the flow, which says how it is being
	// handled: the tunnel mark means this connection is going through the TUN
	// device rather than out of the WAN directly.
	Mark string `json:"mark,omitempty"`
}

// Total returns the flow's combined volume.
func (f *Flow) Total() int64 { return f.BytesUp + f.BytesDown }

// Client aggregates every flow belonging to one LAN address.
type Client struct {
	IP        string `json:"ip"`
	Hostname  string `json:"hostname,omitempty"`
	Flows     int    `json:"flows"`
	BytesUp   int64  `json:"bytes_up"`
	BytesDown int64  `json:"bytes_down"`
}

// Snapshot is one reading of the connection table.
type Snapshot struct {
	// Accounting says whether the kernel is counting bytes. When it is off
	// the flow and client lists are still accurate; only the volumes are zero.
	Accounting bool   `json:"accounting"`
	Available  bool   `json:"available"`
	Note       string `json:"note,omitempty"`

	TotalFlows int `json:"total_flows"`
	LANFlows   int `json:"lan_flows"`

	Clients []Client  `json:"clients"`
	Flows   []Flow    `json:"flows"`
	TakenAt time.Time `json:"taken_at"`
}

// Options control a reading.
type Options struct {
	// LANCIDRs limits the report to flows originating on the local network.
	// Without it the router's own connections dominate the list.
	LANCIDRs []string
	// TopFlows caps how many individual flows are returned. A busy router can
	// hold tens of thousands of entries and nobody reads past the first page.
	TopFlows int
}

// Read takes a snapshot of the connection table.
func Read(o Options) (*Snapshot, error) {
	if o.TopFlows <= 0 {
		o.TopFlows = 100
	}
	snap := &Snapshot{TakenAt: time.Now(), Accounting: accountingEnabled()}

	f, err := os.Open(conntrackPath)
	if err != nil {
		snap.Note = "connection tracking is not available on this device " +
			"(" + conntrackPath + " could not be read): " + err.Error()
		return snap, nil
	}
	defer f.Close()
	snap.Available = true

	nets := parseCIDRs(o.LANCIDRs)
	names := readLeases()

	var flows []Flow
	byClient := map[string]*Client{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		snap.TotalFlows++
		flow, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		// With no LAN networks known, everything is reported rather than
		// nothing: an empty screen would look like a broken feature.
		if len(nets) > 0 && !inNets(flow.Src, nets) {
			continue
		}
		snap.LANFlows++
		flow.Hostname = names[flow.Src]

		c := byClient[flow.Src]
		if c == nil {
			c = &Client{IP: flow.Src, Hostname: flow.Hostname}
			byClient[flow.Src] = c
		}
		c.Flows++
		c.BytesUp += flow.BytesUp
		c.BytesDown += flow.BytesDown

		flows = append(flows, flow)
	}

	// Heaviest first when volumes are known; otherwise most recently seen
	// order is meaningless, so fall back to a stable sort by address and port
	// to keep the list from jumping around between polls.
	if snap.Accounting {
		sort.SliceStable(flows, func(i, j int) bool {
			return flows[i].Total() > flows[j].Total()
		})
	} else {
		sort.SliceStable(flows, func(i, j int) bool {
			if flows[i].Src != flows[j].Src {
				return flows[i].Src < flows[j].Src
			}
			return flows[i].SPort < flows[j].SPort
		})
	}
	if len(flows) > o.TopFlows {
		flows = flows[:o.TopFlows]
	}
	snap.Flows = flows

	snap.Clients = make([]Client, 0, len(byClient))
	for _, c := range byClient {
		snap.Clients = append(snap.Clients, *c)
	}
	sort.SliceStable(snap.Clients, func(i, j int) bool {
		a, b := snap.Clients[i], snap.Clients[j]
		if at, bt := a.BytesUp+a.BytesDown, b.BytesUp+b.BytesDown; at != bt {
			return at > bt
		}
		if a.Flows != b.Flows {
			return a.Flows > b.Flows
		}
		return a.IP < b.IP
	})

	if !snap.Accounting {
		snap.Note = "the kernel is tracking connections but not counting bytes, " +
			"so volumes read zero; enable it with " +
			"sysctl -w net.netfilter.nf_conntrack_acct=1"
	}
	return snap, nil
}

// parseLine reads one conntrack entry.
//
// The format repeats the tuple twice, once per direction, and the accounting
// fields follow each tuple when they are present at all. The parser therefore
// tracks which direction it is in rather than matching fixed positions, which
// also keeps it working across the format's small differences between kernels.
func parseLine(line string) (Flow, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return Flow{}, false
	}

	var f Flow
	dir := 0 // 0 = original, 1 = reply
	sawSrc := false

	for _, field := range fields {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			// Bare tokens: the protocol name and the connection state.
			switch field {
			case "tcp", "udp", "icmp", "udplite", "sctp", "dccp", "gre":
				if f.Protocol == "" {
					f.Protocol = field
				}
			case "ESTABLISHED", "SYN_SENT", "SYN_RECV", "FIN_WAIT", "TIME_WAIT",
				"CLOSE", "CLOSE_WAIT", "LAST_ACK", "UNREPLIED", "ASSURED":
				if f.State == "" && field != "UNREPLIED" && field != "ASSURED" {
					f.State = field
				}
			}
			continue
		}

		switch key {
		case "src":
			if sawSrc {
				dir = 1
			} else {
				sawSrc = true
				f.Src = val
			}
		case "dst":
			if dir == 0 {
				f.Dst = val
			}
		case "sport":
			if dir == 0 {
				f.SPort, _ = strconv.Atoi(val)
			}
		case "dport":
			if dir == 0 {
				f.DPort, _ = strconv.Atoi(val)
			}
		case "packets":
			n, _ := strconv.ParseInt(val, 10, 64)
			if dir == 0 {
				f.PacketsUp = n
			} else {
				f.PacketsDown = n
			}
		case "bytes":
			n, _ := strconv.ParseInt(val, 10, 64)
			if dir == 0 {
				f.BytesUp = n
			} else {
				f.BytesDown = n
			}
		case "mark":
			if val != "0" {
				f.Mark = val
			}
		}
	}

	if f.Src == "" || f.Dst == "" {
		return Flow{}, false
	}
	return f, true
}

// EnableAccounting turns on the kernel's byte counters. It is a system-wide
// setting, so the daemon only does this when explicitly asked.
func EnableAccounting() error {
	if err := os.WriteFile(acctPath, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("enable connection accounting: %w", err)
	}
	return nil
}

func accountingEnabled() bool {
	b, err := os.ReadFile(acctPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

// readLeases maps addresses to the names clients announced over DHCP.
func readLeases() map[string]string {
	out := map[string]string{}
	for _, path := range leasesPaths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// <expiry> <mac> <ip> <hostname> <client-id>
			fields := strings.Fields(sc.Text())
			if len(fields) < 4 {
				continue
			}
			name := fields[3]
			if name == "*" || name == "" {
				continue
			}
			out[fields[2]] = name
		}
		f.Close()
		if len(out) > 0 {
			return out
		}
	}
	return out
}

func parseCIDRs(in []string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range in {
		if _, n, err := net.ParseCIDR(strings.TrimSpace(c)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func inNets(addr string, nets []*net.IPNet) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
