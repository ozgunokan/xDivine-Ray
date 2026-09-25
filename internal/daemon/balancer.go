package daemon

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/xray"
)

// memberTagPrefix is the tag the config generator gives a group's outbounds,
// named here so the parser and the generator cannot drift apart.
const memberTagPrefix = xray.TagProxyPrefix

// Which member of a group the balancer is actually using.
//
// This used to be inferred from the byte counters: a member whose numbers grew
// recently was "in use". That reads correctly until you look at a real device.
// The health-aware strategies probe every member, one request each, every
// minute — and the probe travels through that member's own tunnel, so it lands
// on that member's counters. Every member then looks busy in turn, and the
// Status page named two or three servers at once while only one was carrying
// anything. The traffic numbers made it obvious — kilobytes against megabytes —
// but the indicator above them said otherwise, and an indicator that has to be
// checked against the table is not an indicator.
//
// So the question is put to the core instead. `xray api bi` returns what the
// balancer would hand the next connection, which is the thing being asked.
//
// Only the health-aware strategies are asked. Random and round-robin have no
// observatory, therefore no probe traffic, therefore nothing to confuse the
// counters — and no single answer to give either, since they spread
// connections across members on purpose.

// balancerSelection asks the core which members the balancer is choosing
// between, best first.
//
// An error here is not a fault to report: a core built without RoutingService
// answers the same way as one that is still starting, and neither is worth a
// line in the operator's log every two seconds. The caller falls back.
func balancerSelection(bin, server, tag string) ([]string, error) {
	cmd := exec.Command(bin, "api", "bi", "--server="+server, "--timeout=3", tag)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("xray api bi: %w: %s", err,
			strings.TrimSpace(string(out)))
	}
	sel := parseBalancerInfo(string(out))
	if len(sel) == 0 {
		return nil, fmt.Errorf("xray api bi returned no selection: %s",
			strings.TrimSpace(string(out)))
	}
	return sel, nil
}

// parseBalancerInfo reads the tags out of what `xray api bi` prints.
//
// The output is a plain table, two sections:
//
//   - Selecting Override:
//     1   proxy-1
//   - Selects:
//     1   proxy-0
//     2   proxy-1
//
// An override is a member pinned by hand through `xray api bo`. Nothing here
// sets one, but if something did it would beat the ranking, and reporting the
// ranking then would name a server that is not being used.
//
// Rows outside a section, and anything that is not one of the tags this
// project generates, are ignored: this is someone else's output format and it
// is better to report nothing than to report a heading as a server name.
func parseBalancerInfo(out string) []string {
	const (
		none = iota
		override
		selects
	)
	section := none
	var pinned, ranked []string

	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "- Selecting Override"):
			section = override
			continue
		case strings.HasPrefix(t, "- Selects"):
			section = selects
			continue
		case t == "":
			continue
		}
		if section == none {
			continue
		}
		// "1   proxy-0" — an index and a tag, padded. The tag is the last
		// field; the index is not used, because the rows arrive in order and
		// a printed index that disagreed with the order would be the core's
		// business, not ours.
		fields := strings.Fields(t)
		if len(fields) < 2 {
			continue
		}
		tag := fields[len(fields)-1]
		if !isMemberTag(tag) {
			continue
		}
		if section == override {
			pinned = append(pinned, tag)
		} else {
			ranked = append(ranked, tag)
		}
	}

	if len(pinned) > 0 {
		return pinned
	}
	return ranked
}

// isMemberTag reports whether a word is one of the outbound tags this project
// gives a group's members.
func isMemberTag(s string) bool {
	rest, ok := strings.CutPrefix(s, memberTagPrefix)
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// balancerInterval is how often the core is asked which member it is using.
//
// Slower than the traffic poll on purpose. This is a process launch each time,
// and the answer only changes when the observatory re-ranks — once a minute by
// default. Every two seconds would spend more on asking than on the connection
// being asked about.
const balancerInterval = 10 * time.Second

// refreshBalancer asks the core and records the answer.
func (e *Engine) refreshBalancer(bin, server string) {
	order, err := balancerSelection(bin, server, xray.TagBalancer)

	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.memberOrder = nil
		// Said once per connection, not once per poll. A core without
		// RoutingService fails this call every ten seconds forever, and a log
		// filled with one repeated line is a log nobody reads.
		if !e.balancerWarned {
			e.balancerWarned = true
			e.Log.Warnf("the core did not say which group member it is using "+
				"(%v); the interface falls back to reading the traffic "+
				"counters, which cannot tell your traffic apart from the "+
				"health check", err)
		}
		return
	}
	e.memberOrder = order
}

// refreshLatency times a handshake to each member's server.
func (e *Engine) refreshLatency(members []model.Profile) {
	got := measureMembers(members, dialLatency)

	e.mu.Lock()
	e.memberPing = got
	e.mu.Unlock()
}
