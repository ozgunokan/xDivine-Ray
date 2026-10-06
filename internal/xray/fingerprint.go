package xray

import "strings"

// The handshake nobody offers you.
//
// A uTLS fingerprint makes this device's TLS handshake look like some other
// program's. The core's documentation and every client's interface list the
// same ten: chrome, firefox, safari, ios, android, edge, 360, qq, random,
// randomized. Those are one of three tables inside the core. A second holds
// specific browser versions, and a third — which the core's own source
// describes as golang, randomized, auto and fingerprints that are too old —
// holds, among the fossils, this:
//
//	"hellogolang": &utls.HelloGolang
//
// It is the one entry in all three tables that does not imitate anything.
// HelloGolang is Go's own crypto/tls ClientHello: the handshake the core would
// send if uTLS were not involved at all. It is in the map, it works, and no
// interface offers it.
//
// The reason it matters is speed, on exactly the routers this runs on.
//
// A ClientHello carries the client's cipher suites in preference order, and the
// server picks the first one it also has. Go's crypto/tls builds that order at
// startup from what the CPU can do: with hardware AES it offers AES-GCM first,
// and without it offers ChaCha20-Poly1305 first — two orderings in the standard
// library, chosen by one boolean. uTLS throws that away by design. Its whole
// job is to send the list a browser sends, and a browser's list is written for
// the desktops browsers run on, where AES is free: AES-128-GCM, AES-256-GCM,
// then ChaCha20.
//
// So on a router with no AES instructions — most MIPS, plenty of ARM — every
// browser fingerprint talks the server into AES-GCM, and the device then does
// AES in software for every byte of every connection. ChaCha20 was designed for
// precisely that CPU and is several times faster on it. Choosing this
// fingerprint hands the ordering back to Go, the server picks ChaCha20, and the
// throughput roughly doubles. The operator measured it before this was written
// down; the library source says why.
//
// Two smaller things it is also good for:
//
// Diagnosis. Every other option is a different disguise and none of them is "no
// disguise", so when a server works from one client and not from this one there
// is no way to rule the handshake out. This is the control case: failing on
// every browser fingerprint and succeeding on this one means the server is
// refusing uTLS rather than refusing this device.
//
// And the thing it is not: a better disguise. A Go handshake is uncommon on a
// home line and easier to pick out of traffic than a Chrome one. It is chosen
// for what the CPU can do, not for what an observer can see, and the interface
// says so where it is offered.
//
// The name is the operator's: he asked for it to be called helloXdivine, and
// the core has never heard of that, so it is translated on the way out.

// FingerprintXDivine is this project's name for the core's hidden
// "hellogolang": the handshake Go itself sends, with no imitation of anything.
const FingerprintXDivine = "helloxdivine"

// coreFingerprintName is what the core is given for it.
const coreFingerprintName = "hellogolang"

// coreFingerprint translates a fingerprint into the name the core knows.
//
// Only this one name is translated. Everything else is passed through exactly as
// it arrived, including names this build has never heard of: a share link can
// carry a fingerprint from a newer core, and silently dropping it would turn a
// working server into one that fails for no visible reason.
func coreFingerprint(fp string) string {
	if strings.EqualFold(strings.TrimSpace(fp), FingerprintXDivine) {
		return coreFingerprintName
	}
	return fp
}
