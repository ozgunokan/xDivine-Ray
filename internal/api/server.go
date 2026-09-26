// Package api exposes the daemon over HTTP on the loopback interface.
//
// There is deliberately no authentication here: the listener is bound to
// 127.0.0.1 and the only clients are the CLI and the rpcd shim that LuCI calls,
// which means the LuCI session is already the authentication boundary. Binding
// this to a LAN address would need a token first.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"xwrt/internal/certpin"
	"xwrt/internal/daemon"
	"xwrt/internal/model"
	"xwrt/internal/netmon"
	"xwrt/internal/selftest"
	"xwrt/internal/sub"
	"xwrt/internal/ucicfg"
	"xwrt/internal/uri"
	"xwrt/internal/xray"
)

// Server serves the daemon API.
type Server struct {
	engine *daemon.Engine
	store  *ucicfg.Store
	log    *daemon.LogRing
	http   *http.Server
}

// New returns an API server bound to the loopback address on the given port.
func New(engine *daemon.Engine, store *ucicfg.Store, log *daemon.LogRing, port int) *Server {
	s := &Server{engine: engine, store: store, log: log}
	mux := http.NewServeMux()
	s.routes(mux)
	s.http = &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// ListenAndServe starts the API server.
func (s *Server) ListenAndServe() error {
	err := s.http.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops the API server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.getStatus)
	mux.HandleFunc("GET /api/env", s.getEnv)
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("GET /api/profiles", s.listProfiles)
	mux.HandleFunc("POST /api/profiles", s.createProfile)
	mux.HandleFunc("POST /api/profiles/import", s.importProfiles)
	mux.HandleFunc("PUT /api/profiles/{id}", s.updateProfile)
	mux.HandleFunc("DELETE /api/profiles/{id}", s.deleteProfile)
	mux.HandleFunc("GET /api/profiles/{id}/ping", s.pingProfile)
	mux.HandleFunc("POST /api/profiles/{id}/fetch-cert", s.fetchCert)

	mux.HandleFunc("GET /api/rules", s.listRules)
	mux.HandleFunc("POST /api/rules", s.createRule)
	mux.HandleFunc("PUT /api/rules/{id}", s.updateRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.deleteRule)
	mux.HandleFunc("PUT /api/rules", s.replaceRules)

	mux.HandleFunc("GET /api/groups", s.listGroups)
	mux.HandleFunc("POST /api/groups", s.createGroup)
	mux.HandleFunc("PUT /api/groups/{id}", s.updateGroup)
	mux.HandleFunc("DELETE /api/groups/{id}", s.deleteGroup)

	mux.HandleFunc("GET /api/subscriptions", s.listSubscriptions)
	mux.HandleFunc("POST /api/subscriptions", s.createSubscription)
	mux.HandleFunc("POST /api/subscriptions/{id}/refresh", s.refreshSubscription)
	mux.HandleFunc("DELETE /api/subscriptions/{id}", s.deleteSubscription)

	mux.HandleFunc("POST /api/connect", s.connect)
	mux.HandleFunc("POST /api/disconnect", s.disconnect)
	mux.HandleFunc("POST /api/firewall/reapply", s.reapplyFirewall)

	mux.HandleFunc("GET /api/update", s.getUpdate)
	mux.HandleFunc("POST /api/update/check", s.checkUpdate)
	mux.HandleFunc("POST /api/update/install", s.installUpdate)

	mux.HandleFunc("GET /api/traffic", s.getTraffic)
	mux.HandleFunc("GET /api/connections", s.getConnections)
	mux.HandleFunc("POST /api/connections/accounting", s.enableAccounting)

	mux.HandleFunc("GET /api/logs", s.getLogs)
	mux.HandleFunc("GET /api/logs/errors", s.getLogErrors)
	mux.HandleFunc("DELETE /api/logs/error", s.clearLastError)
	mux.HandleFunc("DELETE /api/logs/errors", s.clearErrors)
	mux.HandleFunc("DELETE /api/logs", s.clearLog)
	mux.HandleFunc("POST /api/selftest", s.selfTest)
}

// --- handlers ----------------------------------------------------------

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	st := s.engine.Status()
	// Read from the stored configuration rather than the engine's copy: the
	// engine only has one once something has connected, and this is worth
	// showing on a device that has never connected at all.
	if data, err := s.store.Load(); err == nil {
		st.AutoConnect = data.Settings.AutoConnect
	}
	// Carried here so that every page can mention an available update without
	// each of them asking a second question on every poll.
	if u := s.engine.Update(); u.Available {
		st.UpdateAvailable = true
		st.UpdateVersion = u.Latest
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) getEnv(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Env())
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	var before model.Settings
	data, err := s.store.Update(func(d *ucicfg.Data) error {
		before = d.Settings

		// Decode over the current settings rather than into an empty struct.
		// A caller that sends three fields means "change these three": with an
		// empty struct every field it left out arrives as a zero value, and
		// Normalize then turns those zeros into defaults — so setting one flag
		// silently reset the capture mode and every other boolean on the way
		// past. Unmarshalling onto a copy leaves absent fields exactly as they
		// were.
		updated := d.Settings
		if err := json.Unmarshal(body, &updated); err != nil {
			return fmt.Errorf("invalid request body: %w", err)
		}
		// Runtime state is owned by the engine, not by the caller.
		updated.Active = d.Settings.Active
		updated.ActiveKind = d.Settings.ActiveKind
		updated.Enabled = d.Settings.Enabled
		updated.Normalize()
		d.Settings = updated
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Settings describe how the connection is built, so a change to them means
	// nothing until it is built again: the capture mode, the ports, the DNS
	// handling and the TUN parameters are all baked into the running core and
	// the installed firewall rules.
	//
	// Only a real change earns a reconnect. Every reconnect drops every
	// connection on the network for a second or two, and a client saving a form
	// it has not edited — or a page that writes settings on load — would
	// otherwise take the whole LAN down for nothing.
	out := map[string]any{"settings": data.Settings}
	if !affectsTheConnection(before, data.Settings) {
		out["reconnected"] = false
		out["unchanged"] = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	if s.engine.Status().Connected {
		if err := s.engine.Connect(""); err != nil {
			// The settings are saved either way. Saying so matters: the next
			// connect will use them, and the failure is about this attempt.
			out["reconnected"] = false
			out["error"] = fmt.Sprintf(
				"settings saved, but reconnecting with them failed: %v", err)
			writeJSON(w, http.StatusOK, out)
			return
		}
		out["reconnected"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// affectsTheConnection says whether the change is one the running connection
// would notice. Everything in Settings does, with one exception: auto_connect
// decides what happens at the next boot and nothing about what is happening
// now. Reconnecting for it drops every connection on the network to apply a
// setting that will not be read until the device restarts.
func affectsTheConnection(before, after model.Settings) bool {
	before.AutoConnect = after.AutoConnect
	return !reflect.DeepEqual(before, after)
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, data.Profiles)
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	var p model.Profile
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p.ID = ucicfg.NewID("p")
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		d.Profiles = append(d.Profiles, p)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

type importRequest struct {
	URI            string `json:"uri"`
	SubscriptionID string `json:"subscription_id,omitempty"`
}

func (s *Server) importProfiles(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// A pasted blob may hold one link or many, so both are accepted here.
	parsed, errs := uri.ParseMany(req.URI)
	if len(parsed) == 0 {
		if len(errs) > 0 {
			writeError(w, http.StatusBadRequest, errs[0])
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("no share links found"))
		return
	}
	for i := range parsed {
		parsed[i].ID = ucicfg.NewID("p")
		parsed[i].Subscription = req.SubscriptionID
	}
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		d.Profiles = append(d.Profiles, parsed...)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"imported": len(parsed),
		"skipped":  len(errs),
		"profiles": parsed,
	})
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in model.Profile
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	in.ID = id
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	found := false
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		for i := range d.Profiles {
			if d.Profiles[i].ID == id {
				d.Profiles[i] = in
				found = true
				return nil
			}
		}
		return fmt.Errorf("profile %q not found", id)
	}); err != nil {
		status := http.StatusInternalServerError
		if !found {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(in))
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		out := d.Profiles[:0]
		for _, p := range d.Profiles {
			if p.ID != id {
				out = append(out, p)
			}
		}
		d.Profiles = out
		if d.Settings.Active == id {
			d.Settings.Active = ""
		}
		// A group holding a deleted profile would fail at connect time with a
		// confusing message, so the membership is cleaned up here instead.
		for i := range d.Groups {
			kept := d.Groups[i].Members[:0]
			for _, m := range d.Groups[i].Members {
				if m != id {
					kept = append(kept, m)
				}
			}
			d.Groups[i].Members = kept
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(map[string]string{"deleted": id}))
}

// pingProfile measures TCP reachability of the server itself. It says nothing
// about whether the proxy protocol works, but it is the cheap check that
// catches a dead or blocked endpoint.
func (s *Server) pingProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	p := data.Profile(id)
	if p == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("profile %q not found", id))
		return
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", p.Endpoint(), 5*time.Second)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	conn.Close()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"ms": time.Since(start).Milliseconds(),
	})
}

// fetchCert pins the server's certificate chain, which is how a profile that
// used allowInsecure keeps working on a core that has removed it.
//
// The fetch happens here, on the router, rather than anywhere else on purpose:
// a pin records whatever answered at fetch time, so it has to be taken over the
// same path the connection will use.
func (s *Server) fetchCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	p := data.Profile(id)
	if p == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("profile %q not found", id))
		return
	}
	if p.Security != "tls" {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"profile %s uses security %q; certificate pinning applies to tls only",
			p.Label(), p.Security))
		return
	}

	res, err := certpin.Fetch(p.Address, p.Port, p.SNI, 10*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		target := d.Profile(id)
		if target == nil {
			return fmt.Errorf("profile %q disappeared", id)
		}
		target.PinnedCert = res.Pin
		// The pin replaces allowInsecure rather than joining it: leaving both
		// set would mean the profile still accepts any certificate on a core
		// that still supports the flag.
		target.AllowInsecure = false
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.log.Infof("pinned the certificate chain of %s (%s, sni %s)",
		p.Label(), res.Endpoint, res.SNI)
	writeJSON(w, http.StatusOK, res)
}

// pending marks a response whose change is stored but not yet running.
//
// Rules, groups and profiles are baked into the core's configuration when a
// connection is made, so editing one while connected changes what *will*
// happen, not what is happening. Settings reconnect themselves because they
// are usually changed one at a time; rules are not — they are added three at a
// time, and reconnecting after each would take the whole network down three
// times. So the change is saved, and the answer says plainly that it is
// waiting. The caller decides when to apply it.
func (s *Server) pending(v any) any {
	// And only when the change actually reaches the core. Deleting a server
	// that is not the active one, renaming one that is not in the active
	// group — these are stored changes that the running core cannot see by any
	// path, and reporting them as pending offered a button that dropped a
	// working tunnel to apply nothing. See daemon.ConfigChanged.
	if !s.engine.ConfigChanged() {
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	m := map[string]any{}
	if json.Unmarshal(b, &m) != nil {
		// A list or a bare value: wrap it rather than lose the flag.
		return map[string]any{"result": v, "reconnect_required": true}
	}
	m["reconnect_required"] = true
	return m
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// An empty list is [], never null: every caller of this ends up iterating
	// it, and null is the one value that turns iteration into an error.
	if data.Rules == nil {
		data.Rules = []model.Rule{}
	}
	writeJSON(w, http.StatusOK, data.Rules)
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	var rule model.Rule
	if err := decodeJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if rule.Action == "" {
		rule.Action = model.ActionDirect
	}
	if err := rule.Validate(xray.AssetsAvailable()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rule.ID = ucicfg.NewID("r")
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		d.Rules = append(d.Rules, rule)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.pending(rule))
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in model.Rule
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	in.ID = id
	if err := in.Validate(xray.AssetsAvailable()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	found := false
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		for i := range d.Rules {
			if d.Rules[i].ID == id {
				d.Rules[i] = in
				found = true
				return nil
			}
		}
		return fmt.Errorf("no rule with id %q", id)
	}); err != nil {
		status := http.StatusInternalServerError
		if !found {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(in))
}

// replaceRules stores the whole list at once, which is how the UI reorders
// them: the core takes the first match, so order is part of the meaning and
// moving one rule is a change to the list, not to a single entry.
func (s *Server) replaceRules(w http.ResponseWriter, r *http.Request) {
	var in []model.Rule
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	geo := xray.AssetsAvailable()
	for i := range in {
		if in[i].Action == "" {
			in[i].Action = model.ActionDirect
		}
		if err := in[i].Validate(geo); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if in[i].ID == "" {
			in[i].ID = ucicfg.NewID("r")
		}
	}
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		d.Rules = in
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(in))
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		out := d.Rules[:0]
		for _, x := range d.Rules {
			if x.ID != id {
				out = append(out, x)
			}
		}
		d.Rules = out
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(map[string]string{"deleted": id}))
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if data.Groups == nil {
		data.Groups = []model.Group{}
	}
	writeJSON(w, http.StatusOK, data.Groups)
}

// validateGroup checks a group against the current profile list before it is
// stored, so a typo in a member ID surfaces now rather than at connect time.
func validateGroup(d *ucicfg.Data, g *model.Group) error {
	// Checked before Normalize, which would otherwise quietly replace a
	// misspelled strategy with the default and store something the caller
	// never asked for.
	if g.Strategy != "" && !g.Strategy.Valid() {
		names := make([]string, 0, len(model.Strategies()))
		for _, st := range model.Strategies() {
			names = append(names, string(st))
		}
		return fmt.Errorf("unknown strategy %q; use one of: %s",
			g.Strategy, strings.Join(names, ", "))
	}
	// Before Normalize as well: Normalize only fills an empty address, so a
	// wrong one survives it and reaches the core, where it costs the group its
	// health checks without any visible failure.
	if err := g.ValidateProbeURL(); err != nil {
		return err
	}
	g.Normalize()
	if len(g.Members) == 0 {
		return errors.New("a group needs at least one member")
	}
	seen := map[string]bool{}
	for _, id := range g.Members {
		if d.Profile(id) == nil {
			return fmt.Errorf("no profile with id %q", id)
		}
		if seen[id] {
			return fmt.Errorf("profile %q is listed twice", id)
		}
		seen[id] = true
	}
	return nil
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var g model.Group
	if err := decodeJSON(r, &g); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	g.ID = ucicfg.NewID("g")

	var bad error
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		if err := validateGroup(d, &g); err != nil {
			bad = err
			return err
		}
		d.Groups = append(d.Groups, g)
		return nil
	}); err != nil {
		status := http.StatusInternalServerError
		if bad != nil {
			status = http.StatusBadRequest
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.pending(g))
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in model.Group
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	in.ID = id

	var bad error
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		if err := validateGroup(d, &in); err != nil {
			bad = err
			return err
		}
		for i := range d.Groups {
			if d.Groups[i].ID == id {
				d.Groups[i] = in
				return nil
			}
		}
		bad = fmt.Errorf("no group with id %q", id)
		return bad
	}); err != nil {
		status := http.StatusInternalServerError
		if bad != nil {
			status = http.StatusBadRequest
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(in))
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		out := d.Groups[:0]
		for _, g := range d.Groups {
			if g.ID != id {
				out = append(out, g)
			}
		}
		d.Groups = out
		if d.Settings.Active == id {
			d.Settings.Active = ""
			d.Settings.ActiveKind = model.TargetProfile
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(map[string]string{"deleted": id}))
}

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, data.Subscriptions)
}

func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request) {
	var in model.Subscription
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.URL) == "" {
		writeError(w, http.StatusBadRequest, errors.New("url is required"))
		return
	}
	in.ID = ucicfg.NewID("s")
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		d.Subscriptions = append(d.Subscriptions, in)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Fetch immediately, both so the servers appear straight away and so that
	// a URL that cannot be read says so now rather than looking like a
	// subscription that happens to be empty.
	res, err := s.refresh(r.Context(), in.ID)
	if err != nil {
		// Nothing usable was stored, so nothing usable is kept: a record that
		// produces no servers and no explanation is worse than no record.
		if _, derr := s.store.Update(func(d *ucicfg.Data) error {
			kept := d.Subscriptions[:0]
			for _, x := range d.Subscriptions {
				if x.ID != in.ID {
					kept = append(kept, x)
				}
			}
			d.Subscriptions = kept
			return nil
		}); derr != nil {
			s.log.Warnf("subscription %s could not be rolled back: %v", in.ID, derr)
		}
		writeError(w, http.StatusBadGateway,
			fmt.Errorf("the subscription could not be read: %w", err))
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) refreshSubscription(w http.ResponseWriter, r *http.Request) {
	res, err := s.refresh(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// refresh replaces the profiles belonging to a subscription. The active profile
// is matched by endpoint so a reconnect is not needed when the server list is
// unchanged apart from ordering.
func (s *Server) refresh(ctx context.Context, id string) (map[string]any, error) {
	data, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	var target *model.Subscription
	for i := range data.Subscriptions {
		if data.Subscriptions[i].ID == id {
			target = &data.Subscriptions[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("subscription %q not found", id)
	}

	fetched, err := sub.NewFetcher().Fetch(ctx, target.URL)
	if err != nil {
		return nil, err
	}

	activeEndpoint := ""
	if a := data.Profile(data.Settings.Active); a != nil {
		activeEndpoint = a.Endpoint()
	}

	newActive := ""
	updated, err := s.store.Update(func(d *ucicfg.Data) error {
		// Reconciled, not replaced: a server that is still listed keeps its id
		// — which is what groups and the active selection are made of — and
		// its pinned certificate. See model.MergeSubscription.
		d.Profiles = model.MergeSubscription(d.Profiles, fetched.Profiles, id,
			func() string { return ucicfg.NewID("p") })
		for i := range d.Profiles {
			if d.Profiles[i].Subscription == id &&
				d.Profiles[i].Endpoint() == activeEndpoint {
				newActive = d.Profiles[i].ID
			}
		}
		// A server the listing dropped leaves its id behind in any group that
		// used it; a member id pointing at nothing is refused at connect time.
		d.Groups = model.PruneMembers(d.Groups, d.Profiles)
		for i := range d.Subscriptions {
			if d.Subscriptions[i].ID == id {
				d.Subscriptions[i].Count = len(fetched.Profiles)
				d.Subscriptions[i].Updated = time.Now().Format(time.RFC3339)
			}
		}
		if newActive != "" {
			d.Settings.Active = newActive
		} else if d.Settings.Active != "" && d.Profile(d.Settings.Active) == nil {
			d.Settings.Active = ""
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.log.Infof("subscription %s: %d servers", target.Name, len(fetched.Profiles))
	return map[string]any{
		"count":    len(fetched.Profiles),
		"skipped":  fetched.Skipped,
		"warnings": fetched.Warnings,
		"profiles": updated.Profiles,
	}, nil
}

func (s *Server) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Update(func(d *ucicfg.Data) error {
		subs := d.Subscriptions[:0]
		for _, x := range d.Subscriptions {
			if x.ID != id {
				subs = append(subs, x)
			}
		}
		d.Subscriptions = subs

		profiles := d.Profiles[:0]
		for _, p := range d.Profiles {
			if p.Subscription != id {
				profiles = append(profiles, p)
			}
		}
		d.Profiles = profiles
		if d.Profile(d.Settings.Active) == nil {
			d.Settings.Active = ""
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.pending(map[string]string{"deleted": id}))
}

// connectRequest accepts any of the three names for the same thing. Profile
// and group IDs come from the same generator, so one lookup covers both; the
// aliases exist only to make the API read naturally from either side.
type connectRequest struct {
	ID        string `json:"id"`
	ProfileID string `json:"profile_id"`
	GroupID   string `json:"group_id"`
}

func (r connectRequest) target() string {
	for _, v := range []string{r.ID, r.ProfileID, r.GroupID} {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	_ = decodeJSON(r, &req) // an empty body means "use the stored selection"
	if err := s.engine.Connect(req.target()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.engine.Status())
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Disconnect(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.engine.Status())
}

func (s *Server) reapplyFirewall(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.ReapplyFirewall(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// The update endpoints. Three, because they are three different things: what
// we already know (free), asking again (a request to GitHub), and installing
// (a download, a checksum, and a service that restarts under the caller).
func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Update())
}

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.engine.CheckUpdate(ctx))
}

func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	// The download can take minutes on a router's uplink and the deadline for
	// it belongs to the engine, not to this request: the installer stops the
	// service, which closes this connection, and a context tied to the request
	// would cancel the work it had just started.
	if err := s.engine.InstallUpdate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"installing": true,
		"log":        "/tmp/xwrt-update.log",
		"note": "the service restarts as part of this; the interface will " +
			"lose contact with it for a few seconds",
	})
}

func (s *Server) getTraffic(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Traffic())
}

func (s *Server) getConnections(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	// Reading the connection table is only useful once it is scoped to the
	// local network; the router's own connections are noise here.
	env := s.engine.Env()
	snap, err := netmon.Read(netmon.Options{
		LANCIDRs: env.LANCIDRs,
		TopFlows: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// enableAccounting turns on the kernel's byte counters. It changes a
// system-wide sysctl, so it is an explicit action rather than something the
// daemon does on its own at startup.
func (s *Server) enableAccounting(w http.ResponseWriter, r *http.Request) {
	if err := netmon.EnableAccounting(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Infof("connection byte accounting enabled (net.netfilter.nf_conntrack_acct=1)")
	writeJSON(w, http.StatusOK, map[string]bool{"accounting": true})
}

// getLogs returns log lines, filtered.
//
// The filters are the point rather than a convenience: on a busy router the
// core writes a line per connection, so an unfiltered log is useless for
// finding the one entry that explains a failure. level=error narrows hundreds
// of lines to the handful that matter.
func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := daemon.Query{Limit: queryInt(q.Get("limit"), 200)}

	if v := q.Get("level"); v != "" && v != "all" {
		level, ok := model.ParseLevel(v)
		if !ok {
			writeError(w, http.StatusBadRequest,
				fmt.Errorf("unknown log level %q: use debug, info, warning or error", v))
			return
		}
		query.MinLevel = level
	}
	if v := q.Get("source"); v != "" && v != "all" {
		query.Source = model.Source(v)
	}
	if v := q.Get("step"); v != "" && v != "all" {
		query.Step = model.Step(v)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries": s.log.Entries(query),
		// The banner-worthy failure travels with the log so a client showing
		// both does not need a second request.
		"last_error": s.log.LastError(),
	})
}

// getLogErrors returns the error journal: only failures, kept far longer than
// the main ring, each with the detail and hint that explain it.
func (s *Server) getLogErrors(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r.URL.Query().Get("limit"), 50)
	writeJSON(w, http.StatusOK, map[string]any{
		"errors":     s.log.Errors(limit),
		"last_error": s.log.LastError(),
	})
}

// clearLastError dismisses the current failure banner. The journal keeps its
// copy, so dismissing is not forgetting.
func (s *Server) clearLastError(w http.ResponseWriter, r *http.Request) {
	s.log.ClearLastError()
	writeJSON(w, http.StatusOK, map[string]bool{"cleared": true})
}

// clearErrors empties the failure journal, which is what the Clear button on
// the log page does once the failures listed there have been dealt with.
func (s *Server) clearErrors(w http.ResponseWriter, r *http.Request) {
	n := s.log.ClearErrors()
	writeJSON(w, http.StatusOK, map[string]int{"cleared": n})
}

// clearLog empties the message stream as well.
func (s *Server) clearLog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{"cleared": s.log.Clear()})
}

// selfTest measures the tunnel against a direct connection, so "is it slow, and
// whose fault is it" can be answered from the web interface rather than from a
// shell with a dozen curl invocations.
func (s *Server) selfTest(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	st := s.engine.Status()

	opts := selftest.Options{
		SocksAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(data.Settings.SocksPort)),
		Target:    strings.TrimSpace(r.URL.Query().Get("target")),
		Rounds:    queryInt(r.URL.Query().Get("rounds"), 10),
		// Read from the settings rather than guessed: with the router's own
		// traffic captured, this daemon's probes are captured too, and the
		// unproxied legs would measure the tunnel while claiming not to.
		ProxyRouter: data.Settings.ProxyRouter && st.Connected,
		// Which is what this undoes. The capture rules let the core's own
		// sockets past — they have to, or the core could not reach its server
		// — and a probe wearing the same mark is on that side of the rule, so
		// the legs that are supposed to go around the tunnel really do.
		Mark: data.Settings.MarkValue(),
	}
	// The active server is measured on its own, which is what separates "the
	// link to the server is bad" from "the server's own network is bad".
	if st.Connected && st.ProfileID != "" {
		if p := data.Profile(st.ProfileID); p != nil {
			opts.ServerAddr = p.Endpoint()
		} else if g := data.Group(st.ProfileID); g != nil {
			if members := data.GroupMembers(g); len(members) > 0 {
				opts.ServerAddr = members[0].Endpoint()
			}
		}
	}

	writeJSON(w, http.StatusOK, selftest.Run(opts))
}

func queryInt(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return def
}

// --- plumbing ----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}
