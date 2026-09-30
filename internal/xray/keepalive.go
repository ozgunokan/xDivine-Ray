package xray

import "xwrt/internal/model"

// Keeping the tunnel's own connections visibly alive.
//
// Everything between this device and the server — the mobile operator's NAT,
// its DPI box, a CDN if there is one — keeps a table of connections and drops
// the ones it has not seen traffic on for a while. None of them says so. The
// connection is simply gone, in both directions, and the phone whose push
// connection was riding in it finds out at its next heartbeat, up to half an
// hour later.
//
// The idle timeouts this project already raised (the core's connIdle, the
// tunnel process's own) are timeouts on this device. They do nothing about the
// ones out on the line. What does is traffic: a connection that carries
// something every thirty seconds is never idle to anything in the path.
//
// Two kinds of traffic are available, and they are not equally good:
//
//   - A WebSocket ping. A real message inside the encrypted stream, answered by
//     the server with a pong. Every box on the path sees data going both ways.
//     This is the strong one, and the core has it switched off by default.
//
//   - A TCP keepalive probe. An empty segment, answered by the server's
//     kernel. The core already sends these every 45 seconds; some middleboxes
//     count them as activity and some do not. It is the only thing a plain TCP
//     transport can have, because a plain VLESS stream is the application's
//     own bytes end to end and there is nowhere to put a ping that the far end
//     would not deliver as data.
//
// gRPC also has a ping, and it is deliberately not switched on. A gRPC server
// enforces a minimum interval between client pings — five minutes by default
// — and answers anything more frequent by closing the connection with
// too_many_pings. Whether the core's gRPC server relaxes that is not
// documented, and a keepalive that makes the server hang up every thirty
// seconds is the exact opposite of the point. gRPC connections get the TCP
// keepalive, which is below that layer and cannot trip it.
//
// XHTTP is left alone: it sends HTTP/2 or HTTP/3 pings of its own by default,
// and it can run over QUIC, where a TCP socket option has nothing to apply to.

// keepAliveProtocols are the outbounds that carry the tunnel to a server. The
// direct, block and DNS outbounds are not the tunnel and are left alone.
var keepAliveProtocols = map[string]bool{
	string(model.ProtoVLESS):       true,
	string(model.ProtoVMess):       true,
	string(model.ProtoTrojan):      true,
	string(model.ProtoShadowsocks): true,
}

// keepAliveNetworks are the transports that run over a single TCP socket the
// keepalive option can be set on. mKCP and QUIC are UDP; XHTTP has its own and
// may be UDP.
var keepAliveNetworks = map[string]bool{
	"":            true, // unset means tcp
	"tcp":         true,
	"raw":         true,
	"ws":          true,
	"httpupgrade": true,
	"grpc":        true,
	"h2":          true,
}

// applyKeepAlive sets the pings and probes on every outbound that carries the
// tunnel. seconds <= 0 leaves the core's own behaviour untouched.
func applyKeepAlive(cfg *Config, seconds int) {
	if cfg == nil || seconds <= 0 {
		return
	}
	for i := range cfg.Outbounds {
		out := &cfg.Outbounds[i]
		if !keepAliveProtocols[out.Protocol] || out.StreamSettings == nil {
			continue
		}
		ss := out.StreamSettings
		if !keepAliveNetworks[ss.Network] {
			continue
		}

		if ss.Network == "ws" && ss.WSSettings != nil {
			ss.WSSettings.HeartbeatPeriod = seconds
		}

		if ss.Sockopt == nil {
			ss.Sockopt = &Sockopt{}
		}
		ss.Sockopt.TCPKeepAliveIdle = seconds
		ss.Sockopt.TCPKeepAliveInterval = seconds
	}
}
