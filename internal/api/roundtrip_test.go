package api

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
	"xwrt/internal/uri"
	"xwrt/internal/xray"
)

// A whole configuration, out through the JSON page and back in.
//
// The unit tests next door check what the editor refuses. This one checks the
// thing that would be worse than a refusal: a document it accepts which does
// not mean what it said. Every field has to survive being written to UCI, read
// back, rendered as JSON, parsed again and written again — and the proof is
// not that the structs match but that the core would be handed the same
// configuration at the end of it.
//
// The link below is shaped like the ones people actually paste: websocket
// under TLS, with an ALPN list, a fingerprint, an SNI that is not the host,
// and verification skipped. Those are the fields a hand-rolled serialiser
// loses one at a time, and losing one of them silently is how a tunnel comes
// up and carries nothing.
//
// This test was written the wrong way round first, and the mistake is worth
// recording: it compared two values that had both been through the store, so a
// field the store never wrote was missing from both and the comparison passed.
// It only caught anything once it compared what went in with what came out.
const roundTripLink = "vless://00000000-1111-2222-3333-444444444444" +
	"@peer.example.com:8443?type=ws&encryption=none&path=%2F&host=" +
	"&security=tls&fp=chrome&alpn=h2%2Chttp%2F1.1&allowInsecure=1" +
	"&sni=cdn.example.net#round-trip"

func TestAWholeConfigurationSurvivesTheEditor(t *testing.T) {
	p, err := uri.Parse(roundTripLink)
	if err != nil {
		t.Fatalf("the importer could not read the link: %v", err)
	}
	if p.ID == "" {
		p.ID = "p1"
	}
	// The importer's own reading of the link, so a change there shows up here
	// as a named field rather than as a mysterious difference later.
	if p.Network != "ws" || p.Security != "tls" || p.SNI == "" ||
		p.ALPN == "" || p.Fingerprint == "" || !p.AllowInsecure {
		t.Fatalf("the link did not import as expected: %#v", *p)
	}

	dir := t.TempDir()
	t.Setenv("XWRT_CONFDIR", dir)
	store := ucicfg.NewStore(ucicfg.New())

	// A second server carrying the fields the first one does not: a pinned
	// certificate, a flow, a bypass. If any of these is lost between JSON and
	// UCI, the editor silently changes a connection rather than failing.
	second := *p
	second.ID = "p2"
	second.Name = "pinned-and-flowing"
	second.Flow = "xtls-rprx-vision"
	second.Network = "tcp"
	second.PinnedCert = "9b2f1c4e5a6d7889aabbccddeeff00112233445566778899aabbccddeeff0011"
	second.AllowInsecure = false
	second.ALPN = "h2"

	s := model.Defaults()
	s.Active = "g1"
	s.BypassIP = []string{"10.0.0.0/8", "172.16.0.0/12"}
	s.BypassMAC = []string{"aa:bb:cc:dd:ee:ff"}

	full := &ucicfg.Data{
		Settings: s,
		Profiles: []model.Profile{*p, second},
		Groups: []model.Group{{
			ID: "g1", Name: "avrupa", Strategy: model.StrategyLeastPing,
			Members: []string{p.ID, "p2"},
		}},
		Rules: []model.Rule{{
			ID: "r1", Name: "banka", Action: model.ActionDirect,
			Domains: []string{"bank.com.tr", "*.vakifbank.com.tr"},
			IPs:     []string{"81.212.0.0/16"},
			Sources: []string{"192.168.2.50"},
			Port:    "443",
		}, {
			ID: "r2", Name: "reklam", Action: model.ActionBlock,
			Domains: []string{"ads.example"},
		}},
		Subscriptions: []model.Subscription{{
			ID: "s1", Name: "abone", URL: "https://example.com/sub/xyz", Count: 2,
		}},
	}
	if err := store.Save(full); err != nil {
		t.Fatalf("first save: %v", err)
	}

	d1, err := store.Load()
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(d1.Profiles) != 2 || len(d1.Groups) != 1 || len(d1.Rules) != 2 ||
		len(d1.Subscriptions) != 1 {
		t.Fatalf("the store lost something before the round trip even started: "+
			"%d profiles, %d groups, %d rules, %d subscriptions",
			len(d1.Profiles), len(d1.Groups), len(d1.Rules), len(d1.Subscriptions))
	}

	// This is what the JSON page shows.
	doc, err := json.MarshalIndent(d1, "", "  ")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// And this is what pressing Save does with it.
	d2, problems := parseConfig(doc, d1)
	if len(problems) > 0 {
		t.Fatalf("the exported document was refused by the editor: %v", problems)
	}
	if err := store.Save(d2); err != nil {
		t.Fatalf("second save: %v", err)
	}
	d3, err := store.Load()
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	diff := func(what string, a, b any) {
		t.Helper()
		if reflect.DeepEqual(a, b) {
			return
		}
		t.Errorf("%s changed on the way round", what)
		va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
		if va.Kind() == reflect.Struct {
			for i := 0; i < va.NumField(); i++ {
				if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
					t.Errorf("  %s.%s: %#v -> %#v", what, va.Type().Field(i).Name,
						va.Field(i).Interface(), vb.Field(i).Interface())
				}
			}
			return
		}
		t.Errorf("  before %#v\n  after  %#v", a, b)
	}

	// Two round trips, not one, and the first is the one this test originally
	// missed: comparing d1 with d3 compares two things that both came out of
	// the store, so a field the store never writes is absent from both and the
	// comparison passes. What went in has to be compared with what came out.
	compare := func(label string, want, got *ucicfg.Data) {
		t.Helper()
		if len(got.Profiles) != len(want.Profiles) {
			t.Fatalf("%s: %d profiles went in and %d came out",
				label, len(want.Profiles), len(got.Profiles))
		}
		for i := range want.Profiles {
			diff(label+" profile "+want.Profiles[i].ID, want.Profiles[i], got.Profiles[i])
		}
		for i := range want.Groups {
			diff(label+" group "+want.Groups[i].ID, want.Groups[i], got.Groups[i])
		}
		for i := range want.Rules {
			diff(label+" rule "+want.Rules[i].ID, want.Rules[i], got.Rules[i])
		}
		for i := range want.Subscriptions {
			diff(label+" subscription "+want.Subscriptions[i].ID,
				want.Subscriptions[i], got.Subscriptions[i])
		}
	}

	// The store itself: what was handed to Save, against what Load gives back.
	wanted := *full
	wanted.Settings.Normalize()
	compare("store:", &wanted, d1)
	diff("store: settings", wanted.Settings, d1.Settings)

	// And the editor: what the page showed, against what saving it produced.
	compare("editor:", d1, d3)

	diff("editor: settings", d1.Settings, d3.Settings)

	// The part that actually matters: would the core be given the same thing?
	build := func(d *ucicfg.Data) []byte {
		t.Helper()
		st := d.Settings
		members := d.Profiles
		cfg, err := xray.Build(xray.Options{
			Group:       &d.Groups[0],
			Members:     members,
			Rules:       d.Rules,
			Settings:    &st,
			Caps:        xray.Capabilities{},
			DirectCIDRs: []string{"192.168.2.0/24"},
		})
		if err != nil {
			t.Fatalf("building the core config: %v", err)
		}
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	before, after := build(d1), build(d3)
	if string(before) != string(after) {
		t.Errorf("the core configuration differs after the round trip")
		os.WriteFile(dir+"/before.json", before, 0o600)
		os.WriteFile(dir+"/after.json", after, 0o600)
		t.Logf("written to %s", dir)
	} else {
		t.Logf("core config identical after the round trip (%d bytes)", len(before))
	}
}
