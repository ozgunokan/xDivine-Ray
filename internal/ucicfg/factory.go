package ucicfg

import "xwrt/internal/model"

// Factory is the configuration a fresh install starts from: no servers, no
// groups, no rules, no subscriptions, and every setting at its default.
//
// It has to agree with the file the package actually ships, because that file
// is what "a fresh install" means to anyone who has done one. A test loads the
// shipped file and compares, so the two cannot drift apart without a failure.
func Factory() *Data {
	d := &Data{
		Settings:      model.Defaults(),
		Profiles:      []model.Profile{},
		Groups:        []model.Group{},
		Rules:         []model.Rule{},
		Subscriptions: []model.Subscription{},
	}
	// The shipped file names the binaries by their full path, where the
	// defaults name them only by the name to look up. The defaults stay as they
	// are — they fill in for installs that never wrote these options, and
	// changing them would move where those installs look — but a reset writes
	// what a fresh install has, and a fresh install has the full path.
	d.Settings.XrayBin = "/usr/bin/xray"
	d.Settings.HevBin = "/usr/bin/hev-socks5-tunnel"
	d.Settings.Normalize()
	return d
}
