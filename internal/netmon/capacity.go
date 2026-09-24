package netmon

import (
	"os"
	"strconv"
	"strings"
)

// How full the kernel's connection table is, and why anyone should care.
//
// Every connection a LAN client makes through a NAT capture mode takes a slot
// in nf_conntrack. The table has a fixed size, chosen by the distribution from
// the device's RAM, and when it is full the kernel drops new connections until
// old entries time out. From a chair that looks like this: everything works,
// then a video stalls for a few seconds, then it carries on — over and over,
// getting worse the longer the device has been busy.
//
// It is invisible from every screen anyone would think to look at. The tunnel
// is up, the core is running, the interface says connected, and the log has
// nothing in it. The kernel's complaint goes to dmesg, which nobody reads
// until someone tells them to.
//
// The modes differ here, which is what makes it worth measuring rather than
// assuming. Redirect and mixed put every client TCP connection through NAT
// REDIRECT, so each one is a conntrack entry with a NAT binding; mixed adds a
// UDP entry per QUIC flow on top. TUN mode does not: the client's connection
// is terminated in userspace by the tunnel process, and the kernel only sees
// the handful of connections the core itself opens. A device that is fine in
// TUN mode and stalls in mixed is exactly the shape this produces.

const (
	countPath = "/proc/sys/net/netfilter/nf_conntrack_count"
	maxPath   = "/proc/sys/net/netfilter/nf_conntrack_max"
)

// Capacity is how much of the connection table is in use.
type Capacity struct {
	Count int `json:"count"`
	Max   int `json:"max"`
	// Percent is Count/Max, rounded, or 0 when the limit is unknown.
	Percent int `json:"percent"`
	// Known is false on a kernel that does not expose these, which is not a
	// fault — it only means the number cannot be reported.
	Known bool `json:"known"`
}

// Tight reports whether the table is full enough to be dropping connections
// soon, or already.
//
// Eighty per cent rather than a hundred: by the time it is actually full the
// dropping has been happening for a while, and a warning that arrives only at
// the moment of failure is a warning nobody can act on in advance.
func (c Capacity) Tight() bool { return c.Known && c.Percent >= 80 }

// ReadCapacity reads the two numbers the kernel publishes.
func ReadCapacity() Capacity { return readCapacityFrom(countPath, maxPath) }

func readCapacityFrom(countFile, maxFile string) Capacity {
	count, okCount := readInt(countFile)
	max, okMax := readInt(maxFile)
	if !okCount || !okMax || max <= 0 {
		return Capacity{}
	}
	c := Capacity{Count: count, Max: max, Known: true}
	// Integer arithmetic on purpose: this runs on devices where the float
	// unit is emulated, and a percentage does not need the precision.
	c.Percent = count * 100 / max
	return c
}

func readInt(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return n, true
}
