package xray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Xray has removed several transport features over time: the mKCP header and
// seed options, the standalone HTTP/2 and QUIC transports, and allowInsecure.
// A share link written for an older core can still name any of them, and a
// config that names one is rejected outright by a newer core.
//
// Rather than guess from a version number — the cutoffs are not documented in
// one place and vendors ship their own builds — this package asks the core
// what it accepts, by handing it a minimal config for each feature and seeing
// whether it parses. That is the same principle the rest of the daemon uses
// for the device: detect, do not assume.

// Capabilities records which optional features the installed core accepts.
type Capabilities struct {
	Version string `json:"version"`

	KCPHeaderSeed bool `json:"kcp_header_seed"`
	H2Transport   bool `json:"h2_transport"`
	QUICTransport bool `json:"quic_transport"`
	AllowInsecure bool `json:"allow_insecure"`
	// PinnedCert reports support for pinnedPeerCertSha256, the replacement for
	// allowInsecure. Probed rather than assumed: it is the only way a profile
	// with a self-signed certificate can work on a core that removed the flag,
	// so a core that has neither has to be named plainly.
	PinnedCert bool `json:"pinned_cert"`

	// Probed is false when detection could not run, for instance because the
	// binary is missing. Callers then fall back to AllFeatures.
	Probed bool `json:"probed"`
}

// AllFeatures assumes an older core that supports everything. It is the
// fallback when probing is impossible, chosen so that a detection failure
// never turns into a refusal to connect.
func AllFeatures() Capabilities {
	return Capabilities{
		KCPHeaderSeed: true,
		H2Transport:   true,
		QUICTransport: true,
		AllowInsecure: true,
		PinnedCert:    true,
	}
}

var (
	capsMu    sync.Mutex
	capsCache = map[string]Capabilities{}
)

// Probe asks the core which features it accepts. Results are cached per binary
// version, so a reconnect does not re-run the probes.
func Probe(bin string) Capabilities {
	version := coreVersion(bin)

	capsMu.Lock()
	if c, ok := capsCache[bin+"|"+version]; ok {
		capsMu.Unlock()
		return c
	}
	capsMu.Unlock()

	c := Capabilities{Version: version, Probed: true}
	dir, err := os.MkdirTemp("", "xwrt-probe")
	if err != nil {
		return AllFeatures()
	}
	defer os.RemoveAll(dir)

	c.KCPHeaderSeed = accepts(bin, dir,
		`{"network":"kcp","kcpSettings":{"header":{"type":"none"},"seed":"s"}}`)
	c.H2Transport = accepts(bin, dir,
		`{"network":"h2","security":"tls","tlsSettings":{"serverName":"a.com"},"httpSettings":{"path":"/"}}`)
	c.QUICTransport = accepts(bin, dir,
		`{"network":"quic","security":"tls","tlsSettings":{"serverName":"a.com"},"quicSettings":{"security":"none"}}`)
	c.AllowInsecure = accepts(bin, dir,
		`{"network":"tcp","security":"tls","tlsSettings":{"serverName":"a.com","allowInsecure":true}}`)
	c.PinnedCert = accepts(bin, dir,
		`{"network":"tcp","security":"tls","tlsSettings":{"serverName":"a.com",`+
			`"pinnedPeerCertSha256":"`+strings.Repeat("0", 64)+`"}}`)

	capsMu.Lock()
	capsCache[bin+"|"+version] = c
	capsMu.Unlock()
	return c
}

// accepts reports whether the core parses a config carrying the given stream
// settings. Everything else in the probe config is the minimum the core needs.
func accepts(bin, dir, streamSettings string) bool {
	cfg := `{"inbounds":[],"outbounds":[{"protocol":"vless","settings":{"vnext":[{` +
		`"address":"probe.invalid","port":443,"users":[{` +
		`"id":"b831381d-6324-4d53-ad4f-8cda48b30811","encryption":"none"}]}]},` +
		`"streamSettings":` + streamSettings + `}]}`

	path := filepath.Join(dir, "probe.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		return true // cannot tell; assume supported rather than block a connect
	}
	cmd := exec.Command(bin, "run", "-c", path, "-test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Configuration OK")
}

func coreVersion(bin string) string {
	cmd := exec.Command(bin, "version")
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	// "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 ...)"
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		return fields[1]
	}
	return strings.TrimSpace(line)
}
