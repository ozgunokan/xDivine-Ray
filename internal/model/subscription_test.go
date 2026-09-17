package model

import "testing"

func sub(name, addr, uuid string) Profile {
	return Profile{Name: name, Proto: ProtoVLESS, Address: addr, Port: 443,
		UUID: uuid, Network: "tcp", Security: "tls"}
}

func ids() func() string {
	n := 0
	return func() string { n++; return "new" + string(rune('0'+n)) }
}

// The whole point of reconciling: a refresh must not renumber the servers that
// are still there. A group's membership is a list of ids, so renumbering them
// empties every group the subscription feeds — silently, on a schedule.
func TestRefreshKeepsIDsOfServersStillListed(t *testing.T) {
	stored := []Profile{
		{ID: "manual1", Name: "mine", Address: "home.example", Port: 443,
			Proto: ProtoVLESS, UUID: "u0"},
		func() Profile {
			p := sub("TR-1 · 20%", "a.example", "u1")
			p.ID = "s1"
			p.Subscription = "sub1"
			p.PinnedCert = "abc123"
			return p
		}(),
		func() Profile { p := sub("TR-2", "b.example", "u2"); p.ID = "s2"; p.Subscription = "sub1"; return p }(),
	}
	// The provider renamed the first node and dropped the second.
	fetched := []Profile{
		sub("TR-1 · 87%", "a.example", "u1"),
		sub("TR-3 (new)", "c.example", "u3"),
	}

	out := MergeSubscription(stored, fetched, "sub1", ids())

	if len(out) != 3 {
		t.Fatalf("profiles = %d, want 3 (one manual, two from the listing)", len(out))
	}
	if out[0].ID != "manual1" {
		t.Errorf("a profile from outside the subscription was touched: %+v", out[0])
	}

	var kept, added *Profile
	for i := range out {
		switch out[i].Address {
		case "a.example":
			kept = &out[i]
		case "c.example":
			added = &out[i]
		case "b.example":
			t.Error("a server the listing dropped is still stored")
		}
	}
	if kept == nil || added == nil {
		t.Fatalf("listing not applied: %+v", out)
	}
	if kept.ID != "s1" {
		t.Errorf("id of a server still listed = %q, want s1 kept", kept.ID)
	}
	if kept.Name != "TR-1 · 87%" {
		t.Errorf("name = %q, want the new one from the listing", kept.Name)
	}
	// A certificate is pinned by hand against one server; the listing never
	// carries one, so a refresh that drops it is a refresh that breaks the
	// connection it was pinned for.
	if kept.PinnedCert != "abc123" {
		t.Errorf("pinned certificate = %q, want it kept", kept.PinnedCert)
	}
	if added.ID == "" || added.ID == "s1" || added.ID == "s2" {
		t.Errorf("new server id = %q, want a fresh one", added.ID)
	}
	if added.Subscription != "sub1" {
		t.Errorf("new server subscription = %q", added.Subscription)
	}
}

// Renaming is what providers do all day. It must not look like a new server.
func TestRenameIsNotANewServer(t *testing.T) {
	a := sub("🇹🇷 TR | 12%", "a.example", "u1")
	b := sub("🇹🇷 TR | 98% · expires 2026-10-01", "a.example", "u1")
	if !SameServer(&a, &b) {
		t.Error("a renamed node is treated as a different server")
	}

	c := sub("TR", "a.example", "different-uuid")
	if SameServer(&a, &c) {
		t.Error("different credentials must not be merged into one server")
	}
}

// A member id that no longer names a profile is refused at connect time, so a
// dropped server has to leave the group as well.
func TestPruneMembersDropsDeadIDs(t *testing.T) {
	groups := []Group{{ID: "g1", Members: []string{"s1", "s2", "s3"}}}
	profiles := []Profile{{ID: "s1"}, {ID: "s3"}}

	out := PruneMembers(groups, profiles)
	if len(out[0].Members) != 2 ||
		out[0].Members[0] != "s1" || out[0].Members[1] != "s3" {
		t.Errorf("members = %v, want the two that still exist", out[0].Members)
	}
}
