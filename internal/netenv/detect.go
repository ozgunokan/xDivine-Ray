// Package netenv discovers the device's network layout at runtime.
//
// This is the piece that makes the daemon portable. Nothing here assumes a
// particular device: interface names such as br-lan, eth0, ap0 or wan are never
// hardcoded. The daemon asks ubus/UCI what the LAN and WAN actually are on this
// box, and falls back to reading the kernel routing table when neither is
// available. A vendor image with a bridge called br0 and a stock OpenWrt image
// with br-lan both work without configuration.
package netenv

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Firewall identifies which firewall stack the device uses.
type Firewall string

const (
	// FirewallNFT is OpenWrt 22.03 and newer: fw4 on top of nftables.
	FirewallNFT Firewall = "nftables"
	// FirewallIPT is OpenWrt 21.02 and older: fw3 on top of iptables.
	FirewallIPT Firewall = "iptables"
	// FirewallNone means neither tool was found.
	FirewallNone Firewall = "none"
)

// Env is a snapshot of the device's network layout.
type Env struct {
	LANDevices []string `json:"lan_devices"`
	LANCIDRs   []string `json:"lan_cidrs"`
	WANDevice  string   `json:"wan_device"`
	WANGateway string   `json:"wan_gateway"`
	WANAddress string   `json:"wan_address"`

	// WANResolvers are the DNS servers the upstream link handed this device.
	// They are what resolves a name when nothing is tunnelled, and the only
	// safe answer to "resolve this without the VPN": the system resolver is
	// dnsmasq, whose own upstream may be the proxy core.
	WANResolvers []string `json:"wan_resolvers"`

	Firewall  Firewall `json:"firewall"`
	HasFW4    bool     `json:"has_fw4"`
	HasNFT    bool     `json:"has_nft"`
	HasIPT    bool     `json:"has_iptables"`
	HasTProxy bool     `json:"has_tproxy"`

	// MemTotalMB and StorageTotalMB describe the device against the supported
	// floor. They are reported so the Status page and the logs can say plainly
	// when a device is under-specified.
	MemTotalMB     int `json:"mem_total_mb"`
	StorageTotalMB int `json:"storage_total_mb"`
	StorageFreeMB  int `json:"storage_free_mb"`

	DetectedAt time.Time `json:"detected_at"`
}

// The supported floor: 128 MB of RAM and 128 MB of writable storage.
//
// Nothing below that is in scope. Tuning the stack down for 32/64 MB devices
// would constrain the design for everyone else, and the storage floor is what
// lets the daemon, the proxy core and its optional geo data sit side by side
// without the install becoming a jigsaw puzzle.
const (
	MinMemoryMB  = 128
	MinStorageMB = 128
)

// LowMemory reports whether RAM is under the floor. The kernel reserves memory
// before it is counted, so a nominal 128 MB board reports somewhat less; the
// comparison allows for that rather than testing against 128 exactly.
func (e *Env) LowMemory() bool {
	return e.MemTotalMB > 0 && e.MemTotalMB < MinMemoryMB-24
}

// LowStorage reports whether the filesystem holding the daemon is under the
// floor. Overlay filesystems also report less than the nominal flash size, so
// the same allowance applies.
func (e *Env) LowStorage() bool {
	return e.StorageTotalMB > 0 && e.StorageTotalMB < MinStorageMB-24
}

// storageMB reports the size and free space of the filesystem the daemon is
// installed on. On OpenWrt that is the overlay, which is the number that
// actually matters: the squashfs root is read-only and its size says nothing
// about how much room a package has.
func storageMB() (total, free int) {
	path := "/"
	if exe, err := os.Executable(); err == nil {
		path = filepath.Dir(exe)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	if bs == 0 {
		return 0, 0
	}
	const mb = 1024 * 1024
	total = int(st.Blocks * bs / mb)
	free = int(st.Bavail * bs / mb)
	return total, free
}

// memTotalMB reads the device's RAM size from the kernel.
func memTotalMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return int(kb / 1024)
	}
	return 0
}

// LANDevice returns the primary LAN device, or the empty string.
func (e *Env) LANDevice() string {
	if len(e.LANDevices) == 0 {
		return ""
	}
	return e.LANDevices[0]
}

// Overrides let the operator pin devices when auto-detection guesses wrong.
type Overrides struct {
	LANDevice string
	WANDevice string
}

// Detect inspects the running system.
func Detect(ov Overrides) *Env {
	e := &Env{DetectedAt: time.Now()}

	if dump, err := ubusDump(); err == nil {
		e.fromUbus(dump)
	}

	// Routing table is both a fallback and a correction: ubus can report a WAN
	// that is administratively up but has no default route yet.
	gw, dev := defaultRoute()
	if e.WANGateway == "" {
		e.WANGateway = gw
	}
	if e.WANDevice == "" {
		e.WANDevice = dev
	}

	if len(e.LANDevices) == 0 {
		e.LANDevices = lanFallback(e.WANDevice)
	}
	if len(e.LANCIDRs) == 0 {
		for _, dev := range e.LANDevices {
			e.LANCIDRs = append(e.LANCIDRs, deviceCIDRs(dev)...)
		}
	}
	if e.WANAddress == "" && e.WANDevice != "" {
		if addrs := deviceCIDRs(e.WANDevice); len(addrs) > 0 {
			if ip, _, err := net.ParseCIDR(addrs[0]); err == nil {
				e.WANAddress = ip.String()
			}
		}
	}

	if ov.LANDevice != "" {
		e.LANDevices = []string{ov.LANDevice}
		e.LANCIDRs = deviceCIDRs(ov.LANDevice)
	}
	if ov.WANDevice != "" {
		e.WANDevice = ov.WANDevice
	}

	e.LANDevices = dedupe(e.LANDevices)
	e.LANCIDRs = dedupe(e.LANCIDRs)

	// ubus is the good answer and the file is the fallback: a modem managed
	// outside netifd, or a proto ubus reports thinly, still writes its
	// resolvers where the resolver library will find them.
	if len(e.WANResolvers) == 0 {
		e.WANResolvers = resolversFromFiles()
	}
	e.WANResolvers = usableResolvers(dedupe(e.WANResolvers))

	e.detectFirewall()
	e.MemTotalMB = memTotalMB()
	e.StorageTotalMB, e.StorageFreeMB = storageMB()
	return e
}

// ubusInterface mirrors the fields this package needs from
// `ubus call network.interface dump`.
type ubusInterface struct {
	Interface string `json:"interface"`
	Up        bool   `json:"up"`
	Device    string `json:"device"`
	L3Device  string `json:"l3_device"`
	Proto     string `json:"proto"`
	IPv4      []struct {
		Address string `json:"address"`
		Mask    int    `json:"mask"`
	} `json:"ipv4-address"`
	Route []struct {
		Target  string `json:"target"`
		Mask    int    `json:"mask"`
		Nexthop string `json:"nexthop"`
	} `json:"route"`
	DNSServer []string `json:"dns-server"`
}

type ubusDumpResult struct {
	Interface []ubusInterface `json:"interface"`
}

func ubusDump() (*ubusDumpResult, error) {
	out, err := run("ubus", "-S", "call", "network.interface", "dump")
	if err != nil {
		return nil, err
	}
	var res ubusDumpResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return nil, err
	}
	if len(res.Interface) == 0 {
		return nil, fmt.Errorf("ubus returned no interfaces")
	}
	return &res, nil
}

func (e *Env) fromUbus(d *ubusDumpResult) {
	for _, iface := range d.Interface {
		dev := iface.L3Device
		if dev == "" {
			dev = iface.Device
		}
		if dev == "" || dev == "lo" {
			continue
		}
		name := iface.Interface

		// A default route marks the upstream interface, whatever it is called.
		for _, r := range iface.Route {
			if r.Mask == 0 && (r.Target == "0.0.0.0" || r.Target == "::") && r.Nexthop != "" {
				if isIPv4(r.Nexthop) && e.WANGateway == "" {
					e.WANGateway = r.Nexthop
					e.WANDevice = dev
				}
			}
		}
		if e.WANDevice == "" && isWANName(name) && iface.Up {
			e.WANDevice = dev
		}
		// Resolvers are collected from whichever interface is upstream, which
		// is the one that has them on a router: the LAN side hands out the
		// router's own address, not a resolver it can use itself.
		if dev == e.WANDevice || isWANName(name) {
			for _, r := range iface.DNSServer {
				if isIPv4(r) {
					e.WANResolvers = append(e.WANResolvers, r)
				}
			}
		}

		if !iface.Up || isWANName(name) {
			continue
		}
		// Anything up, addressed and not upstream is treated as a client-facing
		// network: lan, lan2, guest, iot, and so on.
		if len(iface.IPv4) == 0 {
			continue
		}
		e.LANDevices = append(e.LANDevices, dev)
		for _, a := range iface.IPv4 {
			if a.Address == "" {
				continue
			}
			if cidr := normalizeCIDR(a.Address, a.Mask); cidr != "" {
				e.LANCIDRs = append(e.LANCIDRs, cidr)
			}
		}
		if e.WANAddress == "" && isWANName(name) && len(iface.IPv4) > 0 {
			e.WANAddress = iface.IPv4[0].Address
		}
	}

	// WAN address, second pass: the loop above skips WAN interfaces early.
	if e.WANAddress == "" {
		for _, iface := range d.Interface {
			dev := iface.L3Device
			if dev == "" {
				dev = iface.Device
			}
			if dev == e.WANDevice && len(iface.IPv4) > 0 {
				e.WANAddress = iface.IPv4[0].Address
				break
			}
		}
	}
}

// isWANName reports whether a UCI interface name looks like an upstream.
// It is only a hint: the default route is what actually decides.
func isWANName(name string) bool {
	n := strings.ToLower(name)
	return n == "wan" || n == "wan6" || strings.HasPrefix(n, "wan") ||
		strings.HasPrefix(n, "wwan") || n == "modem" || n == "usb0"
}

// defaultRoute reads the IPv4 default route straight from the kernel, which
// works on every Linux box regardless of what tooling is installed.
func defaultRoute() (gateway, device string) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return "", ""
	}
	defer f.Close()

	bestMetric := int64(1<<63 - 1)
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false // header
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 {
			continue
		}
		if fields[1] != "00000000" || fields[7] != "00000000" {
			continue // not a default route
		}
		metric, _ := strconv.ParseInt(fields[6], 10, 64)
		if metric > bestMetric {
			continue
		}
		gw := hexToIPv4(fields[2])
		if gw == "" {
			continue
		}
		bestMetric = metric
		gateway = gw
		device = fields[0]
	}
	return gateway, device
}

// hexToIPv4 converts the little-endian hex form used by /proc/net/route.
func hexToIPv4(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return ""
	}
	v := binary.LittleEndian.Uint32(b)
	if v == 0 {
		return ""
	}
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], v)
	return net.IP(out[:]).String()
}

// lanFallback picks client-facing devices without ubus, by looking at which
// interfaces carry a private address.
func lanFallback(wanDev string) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var bridges, others []string
	for _, iface := range ifaces {
		if iface.Name == "lo" || iface.Name == wanDev {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			if !ipnet.IP.IsPrivate() {
				continue
			}
			// Prefer bridges: on OpenWrt the LAN is virtually always a bridge,
			// whatever the vendor named it.
			if strings.HasPrefix(iface.Name, "br") {
				bridges = append(bridges, iface.Name)
			} else {
				others = append(others, iface.Name)
			}
			break
		}
	}
	sort.Strings(bridges)
	sort.Strings(others)
	if len(bridges) > 0 {
		return bridges
	}
	return others
}

// deviceCIDRs returns the IPv4 networks configured on a device.
func deviceCIDRs(dev string) []string {
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return nil
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.To4() == nil {
			continue
		}
		out = append(out, ipnet.String())
	}
	return out
}

func (e *Env) detectFirewall() {
	e.HasFW4 = haveBinary("fw4")
	e.HasNFT = haveBinary("nft")
	e.HasIPT = haveBinary("iptables")

	switch {
	case e.HasNFT:
		e.Firewall = FirewallNFT
	case e.HasIPT:
		e.Firewall = FirewallIPT
	default:
		e.Firewall = FirewallNone
	}
	e.HasTProxy = detectTProxy(e.Firewall)
}

// detectTProxy checks whether transparent UDP is possible. With nftables the
// check is a dry run, so nothing is written to the ruleset.
func detectTProxy(fw Firewall) bool {
	switch fw {
	case FirewallNFT:
		probe := `table inet xwrt_probe {
	chain prerouting {
		type filter hook prerouting priority mangle;
		meta l4proto udp tproxy to :12345 accept
	}
}`
		cmd := exec.Command("nft", "-c", "-f", "-")
		cmd.Stdin = strings.NewReader(probe)
		return cmd.Run() == nil
	case FirewallIPT:
		data, err := os.ReadFile("/proc/net/ip_tables_targets")
		if err == nil && strings.Contains(string(data), "TPROXY") {
			return true
		}
		// The target module may simply not be loaded yet.
		if err := exec.Command("modprobe", "xt_TPROXY").Run(); err == nil {
			data, err := os.ReadFile("/proc/net/ip_tables_targets")
			return err == nil && strings.Contains(string(data), "TPROXY")
		}
		return false
	default:
		return false
	}
}

func normalizeCIDR(addr string, mask int) string {
	if mask <= 0 || mask > 32 {
		mask = 32
	}
	ip := net.ParseIP(addr)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	n := &net.IPNet{IP: ip.Mask(net.CIDRMask(mask, 32)), Mask: net.CIDRMask(mask, 32)}
	return n.String()
}

func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

func haveBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// resolvConfPaths are where OpenWrt keeps the resolvers it learned upstream.
// /etc/resolv.conf is deliberately absent: it points at dnsmasq, which is this
// device itself.
var resolvConfPaths = []string{
	"/tmp/resolv.conf.d/resolv.conf.auto",
	"/tmp/resolv.conf.auto",
}

func resolversFromFiles() []string {
	var out []string
	for _, p := range resolvConfPaths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && f[0] == "nameserver" {
				out = append(out, f[1])
			}
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}

// usableResolvers drops anything that would send a query back into this device.
func usableResolvers(in []string) []string {
	out := []string{}
	for _, r := range in {
		ip := net.ParseIP(r)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		out = append(out, r)
	}
	return out
}
