package ucicfg

import (
	"fmt"
	"sort"
	"strings"
)

// Checking a whole configuration before it replaces the one on the device.
//
// Everything else that writes to the store changes one thing: a profile, a
// rule, one setting. Each of those is validated where it is edited, by a form
// that knows what it is editing. A document someone typed by hand is different
// — it can be wrong in ways no single-field check would ever see. A group
// pointing at a server that is not in the file. Two servers sharing an id. A
// settings block missing entirely. None of those are rejected by a JSON parser,
// and all of them produce a device that loads its configuration, reports no
// error, and cannot connect.
//
// So the whole document is read before any of it is written, and every fault
// is collected rather than only the first: someone fixing a hand-written file
// one error per attempt is someone who gives up on the fourth attempt.

// Check returns every problem with this configuration, in the order a reader
// would work through them. An empty result means it is safe to save.
func (d *Data) Check() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// --- servers ---------------------------------------------------------
	seen := map[string]int{}
	for i := range d.Profiles {
		p := &d.Profiles[i]
		where := p.Label()
		if strings.TrimSpace(p.ID) == "" {
			add("server %d (%s) has no id; every server needs one, and it is "+
				"what groups and the active selection refer to", i+1, where)
			continue
		}
		if first, dup := seen[p.ID]; dup {
			add("servers %d and %d share the id %q; ids have to be unique or "+
				"a group cannot say which one it means", first+1, i+1, p.ID)
		}
		seen[p.ID] = i
		if err := p.Validate(); err != nil {
			add("server %q: %v", where, err)
		}
	}

	// --- groups ----------------------------------------------------------
	groupIDs := map[string]bool{}
	for i := range d.Groups {
		g := &d.Groups[i]
		where := g.Label()
		if strings.TrimSpace(g.ID) == "" {
			add("group %d (%s) has no id", i+1, where)
			continue
		}
		if groupIDs[g.ID] {
			add("two groups share the id %q", g.ID)
		}
		if _, clash := seen[g.ID]; clash {
			add("group %q uses the same id as a server; the two share one "+
				"namespace, because connecting takes an id and has to know "+
				"which it was given", g.ID)
		}
		groupIDs[g.ID] = true
		if err := g.Validate(); err != nil {
			add("group %q: %v", where, err)
		}
		// A member that is not in this file is the fault that produces a
		// group which looks right and connects to fewer servers than it says.
		var missing []string
		for _, m := range g.Members {
			if _, ok := seen[m]; !ok {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			add("group %q lists %d member(s) that are not in this file: %s",
				where, len(missing), strings.Join(missing, ", "))
		}
	}

	// --- rules -----------------------------------------------------------
	ruleIDs := map[string]bool{}
	for i := range d.Rules {
		r := &d.Rules[i]
		if strings.TrimSpace(r.ID) == "" {
			add("rule %d has no id", i+1)
			continue
		}
		if ruleIDs[r.ID] {
			add("two rules share the id %q", r.ID)
		}
		ruleIDs[r.ID] = true
		if strings.TrimSpace(string(r.Action)) == "" {
			add("rule %q has no action; it can only be one of direct, proxy "+
				"or block", r.ID)
		}
	}

	// --- what the daemon will connect to ---------------------------------
	//
	// An active id naming something that is gone is how a device comes back
	// from a reboot, tries to connect, and files an error nobody was there to
	// see. It is a warning rather than a refusal: a file can legitimately be
	// saved in that state on the way to adding the server back.
	if a := strings.TrimSpace(d.Settings.Active); a != "" {
		_, isProfile := seen[a]
		if !isProfile && !groupIDs[a] {
			add("the active selection is %q, which is neither a server nor a "+
				"group in this file; nothing would connect after a reboot", a)
		}
	}

	return problems
}
