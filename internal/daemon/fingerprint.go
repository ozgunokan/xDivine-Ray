package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// The question this file answers: would the core be running a different
// configuration if it were started again right now?
//
// It exists because the honest answer used to be "we did not check". Any edit
// made while connected — a rule, a server, a group — was reported as "saved,
// but not applied yet", and the notice carried a button that reconnected the
// tunnel. Deleting a server that was not the active one, which cannot change
// what the core is doing by any path, produced that notice and that reconnect:
// a working tunnel dropped and rebuilt to apply nothing at all.
//
// So the material the core is built from is fingerprinted at connect time and
// compared afterwards. Changes that cannot reach the running core do not raise
// the notice, and the ones that can still do.

// material is everything from the stored configuration that ends up in the
// core's own config file. It is deliberately a copy of the inputs rather than
// the generated JSON: generating that resolves the server's hostname, which is
// a DNS query and is not guaranteed to give the same answer twice, so two
// identical configurations could fingerprint differently for no reason the
// operator could see or fix.
//
// The environment is left out for the same reason. LAN CIDRs and upstream
// resolvers do go into the core's config, but they change on their own when
// the WAN flaps — and "your WAN reconnected" is not a pending edit to show
// someone who just renamed a server.
type material struct {
	Kind     model.TargetKind `json:"kind"`
	Active   string           `json:"active"`
	Profile  *model.Profile   `json:"profile,omitempty"`
	Group    *model.Group     `json:"group,omitempty"`
	Members  []model.Profile  `json:"members,omitempty"`
	Rules    []model.Rule     `json:"rules"`
	Settings model.Settings   `json:"settings"`
}

// coreSettings is the settings as they were, with the few fields blanked that
// cannot reach the core or the tunnel.
//
// Blanking the exceptions rather than listing the fields that count is
// deliberate. A setting added later is then included without anyone
// remembering to add it here, and the failure mode of forgetting is a notice
// that appears when it need not — not a change that silently never applies.
func coreSettingsOf(s *model.Settings) model.Settings {
	c := *s
	// Which server is selected is carried by material.Active, and the rest are
	// about this daemon rather than about the connection: the control API's
	// port, whether it dials out at boot, and the flag that records that it
	// did.
	c.Active, c.ActiveKind = "", ""
	c.APIPort = 0
	c.AutoConnect = false
	c.Enabled = false
	return c
}

// fingerprint hashes the material. A hash rather than the material itself
// because it is kept for the life of a connection and compared often, and
// because there is nothing useful to read in it.
func fingerprint(m material) string {
	b, err := json.Marshal(m)
	if err != nil {
		// Unhashable means "cannot prove it is the same", which has to read as
		// different: the notice appearing when it need not is a nuisance, the
		// notice missing when it is needed is a rule that silently does not
		// apply.
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// materialFor gathers what a connect to the given target would build from.
// It mirrors how connectLocked resolves a target, and shares its shape: a
// profile, or a group with the members that still exist.
func materialFor(d *ucicfg.Data, targetID string) material {
	m := material{
		Active:   targetID,
		Kind:     model.TargetProfile,
		Rules:    d.Rules,
		Settings: coreSettingsOf(&d.Settings),
	}
	if m.Rules == nil {
		m.Rules = []model.Rule{}
	}
	if g := d.Group(targetID); g != nil {
		m.Kind = model.TargetGroup
		m.Group = g
		m.Members = d.GroupMembers(g)
		return m
	}
	m.Profile = d.Profile(targetID)
	return m
}

// ConfigChanged reports whether the stored configuration would now build a
// different core configuration from the one that is running.
//
// False when nothing is connected: there is no running configuration to
// differ from, so nothing is pending.
func (e *Engine) ConfigChanged() bool {
	e.mu.Lock()
	connected, live := e.connected, e.liveFP
	e.mu.Unlock()
	if !connected {
		return false
	}
	if live == "" {
		// The fingerprint could not be taken when the connection was made, so
		// there is nothing to compare against. Report a difference: see
		// fingerprint.
		return true
	}
	d, err := e.store.Load()
	if err != nil {
		return true
	}
	return fingerprint(materialFor(d, d.Settings.Active)) != live
}
