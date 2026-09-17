package netmon

import (
	"testing"
)

// Real lines, as the kernel writes them. The format repeats the tuple once per
// direction and only carries counters when accounting is on, so both shapes
// have to parse.
const (
	lineWithAcct = "ipv4     2 tcp      6 431999 ESTABLISHED " +
		"src=192.168.1.100 dst=93.184.216.34 sport=54321 dport=443 packets=42 bytes=5120 " +
		"src=93.184.216.34 dst=192.168.1.1 sport=443 dport=54321 packets=38 bytes=91000 " +
		"[ASSURED] mark=482 use=1"

	lineNoAcct = "ipv4     2 udp      17 29 " +
		"src=192.168.1.55 dst=1.1.1.1 sport=41234 dport=53 " +
		"src=1.1.1.1 dst=192.168.1.55 sport=53 dport=41234 " +
		"mark=0 use=1"

	lineUnreplied = "ipv4     2 tcp      6 118 SYN_SENT " +
		"src=192.168.1.77 dst=10.20.30.40 sport=33445 dport=8080 packets=3 bytes=180 " +
		"[UNREPLIED] src=10.20.30.40 dst=192.168.1.77 sport=8080 dport=33445 packets=0 bytes=0 " +
		"mark=0 use=1"
)

func TestParseLineWithAccounting(t *testing.T) {
	f, ok := parseLine(lineWithAcct)
	if !ok {
		t.Fatal("line did not parse")
	}
	if f.Protocol != "tcp" || f.State != "ESTABLISHED" {
		t.Errorf("protocol/state = %q/%q", f.Protocol, f.State)
	}
	if f.Src != "192.168.1.100" || f.Dst != "93.184.216.34" {
		t.Errorf("addresses = %s -> %s", f.Src, f.Dst)
	}
	if f.SPort != 54321 || f.DPort != 443 {
		t.Errorf("ports = %d -> %d", f.SPort, f.DPort)
	}
	// Upload comes from the original direction, download from the reply. Mixing
	// them up would make every graph read backwards.
	if f.BytesUp != 5120 || f.BytesDown != 91000 {
		t.Errorf("bytes up/down = %d/%d, want 5120/91000", f.BytesUp, f.BytesDown)
	}
	if f.PacketsUp != 42 || f.PacketsDown != 38 {
		t.Errorf("packets up/down = %d/%d", f.PacketsUp, f.PacketsDown)
	}
	if f.Mark != "482" {
		t.Errorf("mark = %q, want the tunnel mark", f.Mark)
	}
}

func TestParseLineWithoutAccounting(t *testing.T) {
	f, ok := parseLine(lineNoAcct)
	if !ok {
		t.Fatal("line did not parse")
	}
	if f.Protocol != "udp" || f.DPort != 53 {
		t.Errorf("parsed = %+v", f)
	}
	if f.BytesUp != 0 || f.BytesDown != 0 {
		t.Errorf("counters should be zero without accounting: %+v", f)
	}
	// A zero mark is noise, not information.
	if f.Mark != "" {
		t.Errorf("mark = %q, want empty for mark=0", f.Mark)
	}
}

// TestParseLineWithUnrepliedTag guards the tricky case: the [UNREPLIED] marker
// sits between the two tuples, so a parser keying on field position rather
// than on the second src= would attribute the reply counters wrongly.
func TestParseLineWithUnrepliedTag(t *testing.T) {
	f, ok := parseLine(lineUnreplied)
	if !ok {
		t.Fatal("line did not parse")
	}
	if f.Src != "192.168.1.77" || f.Dst != "10.20.30.40" {
		t.Errorf("addresses = %s -> %s", f.Src, f.Dst)
	}
	if f.BytesUp != 180 || f.BytesDown != 0 {
		t.Errorf("bytes up/down = %d/%d, want 180/0", f.BytesUp, f.BytesDown)
	}
	if f.State != "SYN_SENT" {
		t.Errorf("state = %q", f.State)
	}
}

func TestParseLineRejectsJunk(t *testing.T) {
	for _, line := range []string{"", "ipv4 2", "nonsense without tuples"} {
		if _, ok := parseLine(line); ok {
			t.Errorf("junk line parsed: %q", line)
		}
	}
}

func TestInNetsFiltersToTheLAN(t *testing.T) {
	nets := parseCIDRs([]string{"192.168.1.0/24", "10.0.0.0/8"})
	if len(nets) != 2 {
		t.Fatalf("parsed %d networks, want 2", len(nets))
	}
	if !inNets("192.168.1.100", nets) {
		t.Error("a LAN address was not recognised")
	}
	if !inNets("10.5.5.5", nets) {
		t.Error("a second LAN network was not recognised")
	}
	if inNets("93.184.216.34", nets) {
		t.Error("a public address was treated as LAN")
	}
	if inNets("not-an-address", nets) {
		t.Error("junk was treated as an address")
	}
}

func TestParseCIDRsSkipsInvalidEntries(t *testing.T) {
	nets := parseCIDRs([]string{"192.168.1.0/24", "", "garbage", "10.0.0.0/99"})
	if len(nets) != 1 {
		t.Errorf("parsed %d networks, want only the valid one", len(nets))
	}
}
