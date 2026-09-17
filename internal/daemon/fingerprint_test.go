package daemon

import (
	"testing"

	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// An edit made while connected is only "saved, but not applied yet" when it
// would actually change what the core is running.
//
// It used to be reported for every edit, because the only question asked was
// whether anything was connected. Deleting a server that was not the active one
// then produced a notice offering to reconnect — and pressing it dropped a
// working tunnel, restarted the core, and applied nothing, because nothing had
// changed. That is the case in the first row below; the rest are here so that
// fixing it does not go too far the other way and quietly stop applying edits
// that do matter.
func TestOnlyChangesTheCoreWouldSeeArePending(t *testing.T) {
	base := func() *ucicfg.Data {
		s := model.Defaults()
		s.Active = "p_active"
		s.ActiveKind = model.TargetProfile
		return &ucicfg.Data{
			Settings: s,
			Profiles: []model.Profile{
				{ID: "p_active", Name: "in use", Address: "a.example", Port: 443},
				{ID: "p_other", Name: "spare", Address: "b.example", Port: 443},
			},
			Groups: []model.Group{
				{ID: "g1", Name: "pair", Members: []string{"p_active", "p_other"}},
			},
			Rules: []model.Rule{
				{ID: "r1", Name: "bank", Action: model.ActionDirect,
					Domains: []string{"bank.example"}},
			},
		}
	}

	cases := []struct {
		name    string
		change  func(*ucicfg.Data)
		pending bool
	}{
		// The one that was wrong.
		{"deleting a server that is not the active one", func(d *ucicfg.Data) {
			d.Profiles = d.Profiles[:1]
		}, false},
		{"renaming a server that is not the active one", func(d *ucicfg.Data) {
			d.Profiles[1].Name = "spare, renamed"
		}, false},
		{"adding a server nothing points at", func(d *ucicfg.Data) {
			d.Profiles = append(d.Profiles, model.Profile{
				ID: "p_new", Name: "new", Address: "c.example", Port: 443})
		}, false},
		// A group that is not the active target is not in the core's config
		// either, whatever it contains.
		{"editing a group that is not the active target", func(d *ucicfg.Data) {
			d.Groups[0].Members = []string{"p_other"}
		}, false},
		// Settings that say nothing about the connection.
		{"turning connect-on-startup off", func(d *ucicfg.Data) {
			d.Settings.AutoConnect = !d.Settings.AutoConnect
		}, false},

		// And everything that does reach the core.
		{"editing the active server", func(d *ucicfg.Data) {
			d.Profiles[0].Port = 8443
		}, true},
		{"deleting the active server", func(d *ucicfg.Data) {
			d.Profiles = d.Profiles[1:]
		}, true},
		{"adding a rule", func(d *ucicfg.Data) {
			d.Rules = append(d.Rules, model.Rule{ID: "r2", Name: "ads",
				Action: model.ActionBlock, Domains: []string{"ads.example"}})
		}, true},
		{"editing a rule", func(d *ucicfg.Data) {
			d.Rules[0].Domains = []string{"other.example"}
		}, true},
		{"deleting every rule", func(d *ucicfg.Data) {
			d.Rules = nil
		}, true},
		{"changing the capture mode", func(d *ucicfg.Data) {
			d.Settings.Mode = model.ModeTProxy
		}, true},
		{"changing a port the core listens on", func(d *ucicfg.Data) {
			d.Settings.SocksPort = 10900
		}, true},
		{"selecting a different server", func(d *ucicfg.Data) {
			d.Settings.Active = "p_other"
		}, true},
	}

	for _, c := range cases {
		before := base()
		live := fingerprint(materialFor(before, before.Settings.Active))

		after := base()
		c.change(after)
		now := fingerprint(materialFor(after, after.Settings.Active))

		if got := now != live; got != c.pending {
			if c.pending {
				t.Errorf("%s: reported as already applied, but the core would "+
					"run a different configuration", c.name)
			} else {
				t.Errorf("%s: reported as pending; pressing Apply would drop the "+
					"tunnel and change nothing", c.name)
			}
		}
	}
}

// With a group as the active target, its members are part of what the core
// runs — so a change to one of them counts, and a change to a server outside
// it still does not.
func TestAGroupsMembersCountAndOtherServersDoNot(t *testing.T) {
	base := func() *ucicfg.Data {
		s := model.Defaults()
		s.Active = "g1"
		s.ActiveKind = model.TargetGroup
		return &ucicfg.Data{
			Settings: s,
			Profiles: []model.Profile{
				{ID: "p1", Name: "one", Address: "a.example", Port: 443},
				{ID: "p2", Name: "two", Address: "b.example", Port: 443},
				{ID: "p3", Name: "outside", Address: "c.example", Port: 443},
			},
			Groups: []model.Group{
				{ID: "g1", Name: "pair", Members: []string{"p1", "p2"}},
			},
		}
	}
	live := func() string {
		d := base()
		return fingerprint(materialFor(d, d.Settings.Active))
	}()

	changed := func(fn func(*ucicfg.Data)) bool {
		d := base()
		fn(d)
		return fingerprint(materialFor(d, d.Settings.Active)) != live
	}

	if !changed(func(d *ucicfg.Data) { d.Profiles[1].Port = 8443 }) {
		t.Error("editing a member of the active group was reported as already applied")
	}
	if !changed(func(d *ucicfg.Data) { d.Groups[0].Members = []string{"p1"} }) {
		t.Error("removing a member from the active group was reported as already applied")
	}
	if changed(func(d *ucicfg.Data) { d.Profiles = d.Profiles[:2] }) {
		t.Error("deleting a server outside the active group was reported as pending")
	}
	if changed(func(d *ucicfg.Data) { d.Profiles[2].Name = "renamed" }) {
		t.Error("renaming a server outside the active group was reported as pending")
	}
}
