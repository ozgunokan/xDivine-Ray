'use strict';
'require view';
'require form';
'require uci';
// The device lists come from LuCI's own view of the system rather than from
// this app: it already knows every interface on the box, including the ones
// that appeared after xwrt last looked.
'require network';
// This page talks to UCI rather than the daemon: LuCI renders, validates and
// commits the form, and nothing here calls the API. It still needs xwrt.js for
// the one thing every page needs — the stylesheet. Leaving it out is what made
// this the only page where the tab strips still scrolled sideways on a phone:
// the rules that stop that are in that stylesheet, and no view inserts it for
// another.
'require xwrt';
'require xwrt.i18n as i18n';

// Settings are edited straight in UCI rather than through the daemon's API.
// LuCI already knows how to render, validate and commit a UCI form, and the
// procd reload trigger on the xwrt config tells the daemon to pick the changes
// up, so an active connection is re-established with the new settings.

return view.extend({
	load: function() {
		// The detected devices come from the daemon, and a failure to reach it
		// must not stop the settings page from rendering: someone whose daemon
		// will not start is exactly the person who needs to change a setting.
		return Promise.all([
			uci.load('xwrt'),
			network.getDevices(),
			xwrt.env().catch(function() { return {}; })
		]);
	},

	render: function(data) {
		var m, s, o;
		var devices = (data && data[1]) || [];
		var env = (data && data[2]) || {};

		// What the daemon detected, so the list can say which entry is the one
		// in use right now. Typing a device name from memory is how the wrong
		// one gets set, and the wrong one here means traffic that is never
		// captured on a connection that otherwise looks healthy.
		var detectedLAN = (env.lan_devices || []).join(', ');
		var detectedWAN = env.wan_device || '';

		// Names only, without the loopback and without our own tunnel: neither
		// is ever the answer, and offering them invites the mistake.
		var names = devices.map(function(d) {
			return d.getName ? d.getName() : String(d);
		}).filter(function(n) {
			return n && n !== 'lo' && n.indexOf('xwrt') !== 0;
		}).sort();

		// A device the daemon named but the list does not have — a tunnel
		// interface, something brought up outside LuCI's model — still belongs
		// in the list, because it is demonstrably real.
		[ detectedWAN ].concat((env.lan_devices || [])).forEach(function(n) {
			if (n && names.indexOf(n) < 0) names.push(n);
		});

		// deviceOption turns a text field into a list of what this device
		// actually has, with automatic first and selected by default. It stays
		// a combobox rather than a plain dropdown so an interface that does not
		// exist yet can still be typed in.
		var deviceOption = function(o, detected) {
			o.value('', _('automatic'));
			names.forEach(function(n) {
				o.value(n, n === detected || (detected || '').split(', ').indexOf(n) >= 0
					? _('%s — detected now').format(n) : n);
			});
			o.placeholder = _('automatic');
			return o;
		};

		m = new form.Map('xwrt', _('xDivine-Ray settings'),
			_('Changing these settings reconnects the active session.'));

		s = m.section(form.NamedSection, 'main', 'xwrt');
		s.anonymous = true;
		s.addremove = false;

		s.tab('general', _('General'));
		s.tab('dns', _('DNS'));
		s.tab('routing', _('Routing'));
		s.tab('advanced', _('Advanced'));

		// --- general ---

		o = s.taboption('general', form.ListValue, 'mode', _('Capture mode'),
			_('How client traffic reaches the proxy core. NAT redirection cannot carry UDP, so every mode that proxies UDP uses either TPROXY or the TUN device.'));
		o.value('redirect', _('Redirect — TCP only (UDP is not proxied, QUIC leaks)'));
		o.value('mixed', _('Mixed — TCP by redirect, UDP through the TUN device (recommended)'));
		o.value('tproxy', _('TPROXY — TCP and UDP in the kernel, needs the tproxy module'));
		o.value('tun', _('TUN — everything through the tunnel'));
		o.default = 'mixed';

		// Only redirect mode can act on this, and the field says so rather
		// than being hidden: someone comparing modes should be able to see
		// that the sharp edge of redirect has a switch attached to it.
		o = s.taboption('general', form.Flag, 'block_quic', _('Refuse QUIC in redirect mode'),
			_('Redirect mode proxies TCP and lets UDP go straight out, so QUIC — which is what video sites use — bypasses the tunnel entirely. Where that direct path is filtered or slowed, the result is a video that stalls rather than an error. Refusing QUIC makes the browser fall back to TCP at once, which is proxied. This does nothing in the other three modes: they carry UDP themselves.'));
		o.default = '1';
		o.depends('mode', 'redirect');

		o = s.taboption('general', form.Flag, 'proxy_router', _('Proxy the router\'s own traffic as well'),
			_('Capture not only forwarded LAN traffic but what the router itself produces. With this on, the device\'s own business — package updates, the DDNS client, NTP — goes through the tunnel too: sometimes that is the point, and sometimes it is how you lose remote access.'));
		o.default = '0';

		o = s.taboption('general', form.Flag, 'allow_lan', _('Open SOCKS/HTTP to the LAN'),
			_('Besides transparent capture, let LAN clients use the SOCKS and HTTP inbounds directly.'));
		o.default = '1';

		o = s.taboption('general', form.Flag, 'auto_connect', _('Connect on startup'),
			_('Connect to the last used server once the upstream link comes up. With this off the service never dials out on its own — not after a reboot, not after a restart — and you connect when you want to. On a line that has no internet except through the tunnel, leave it on.'));
		o.default = '1';

		o = s.taboption('general', form.Flag, 'update_check', _('Check for updates'),
			_('Ask once a day whether a newer version has been released, and say so on the About page. Nothing is ever installed without you pressing the button; this only decides whether the question is asked at all.'));
		o.default = '1';

		o = s.taboption('general', form.Value, 'update_repo', _('Update source'),
			_('The GitHub repository whose releases are offered, as owner/name. Change it only if you install from a fork.'));
		o.placeholder = 'ozgunokan/xDivine-Ray';
		o.depends('update_check', '1');

		o = s.taboption('general', form.ListValue, 'log_level', _('Core log level'));
		o.value('none', _('none'));
		o.value('error', _('error'));
		o.value('warning', _('warning'));
		o.value('info', _('info'));
		o.value('debug', _('debug'));
		o.default = 'warning';

		// --- dns ---

		o = s.taboption('dns', form.ListValue, 'dns_mode', _('DNS handling'),
			_('Resolving a name takes two steps: the client asks the router, and the router asks its own upstream DNS server. This setting decides the second step — the one that matters. If you are unsure, pick the first; it is the only option that works in every capture mode.'));
		o.value('dnsmasq', _('Send dnsmasq to the core — works in every mode (recommended)'));
		o.value('redirect', _('Capture port 53 only — for clients that set their own DNS'));
		o.value('off', _('Leave DNS alone — queries go to your ISP'));
		o.default = 'dnsmasq';

		// The choice looks like a preference and is not: one of the three stops
		// name resolution completely in TUN mode, and the way it fails gives no
		// hint that DNS is involved. Whatever it costs in screen space, it costs
		// less than the evening it takes to work out from the symptom.
		o = s.taboption('dns', form.DummyValue, '_dns_help');
		o.rawhtml = true;
		o.cfgvalue = function() {
			return '<div style="line-height:1.6">' +
				'<p><strong>' + _('Send dnsmasq to the core') + '</strong> — ' +
				_('dnsmasq hands the queries to the core, and the core sends them through the tunnel. Local names (DHCP names, .lan) keep working and no query leaks to your ISP. <strong>Required in TUN mode.</strong>') +
				'</p><p><strong>' + _('Capture port 53 only') + '</strong> — ' +
				_('This is not a variant of the one above, it does something else entirely: it sends only the clients that have typed in a DNS server of their own (8.8.8.8 and the like) to the core. Clients that ask the router never meet this rule — the router\'s own address is on the exempt list — so dnsmasq carries on using your ISP\'s DNS. The result: in redirect and mixed mode your queries go out in the open; <strong>in TUN mode no name resolves at all</strong>, because those queries leave through the tunnel and your ISP\'s server will not answer a stranger.') +
				'</p><p><strong>' + _('Leave DNS alone') + '</strong> — ' +
				_('The router\'s DNS configuration is left exactly as it is. This is the right choice if you have set up your own resolver (AdGuard, Unbound, DoH) and you are sure it leaves through the tunnel.') +
				'</p></div>';
		};

		o = s.taboption('dns', form.Value, 'dns', _('Upstream DNS server'),
			_('Queries are sent here over TCP through the proxy.'));
		o.default = '1.1.1.1';
		o.datatype = 'host';

		o = s.taboption('dns', form.Value, 'dns_port', _('DNS inbound port'));
		o.datatype = 'port';
		o.default = '15353';

		// --- routing ---

		o = s.taboption('routing', form.DynamicList, 'bypass_ip', _('Exempt networks'),
			_('Destinations that never go through the proxy. LAN networks and private ranges are added on their own.'));
		o.datatype = 'ipmask4';

		o = s.taboption('routing', form.DynamicList, 'bypass_mac', _('Exempt clients'),
			_('LAN clients to leave outside the transparent proxy, by MAC address.'));
		o.datatype = 'macaddr';

		o = s.taboption('routing', form.Value, 'lan_device', _('LAN device'),
			_('Leave on automatic unless the Status page shows the wrong device. This is the interface LAN clients arrive on — usually the bridge.'));
		deviceOption(o, detectedLAN);

		o = s.taboption('routing', form.Value, 'wan_device', _('WAN device'),
			_('Leave on automatic unless the Status page shows the wrong device. This is the interface that reaches the internet.'));
		deviceOption(o, detectedWAN);

		o = s.taboption('routing', form.Flag, 'ipv6', _('Resolve IPv6 addresses'),
			_('Leaving this off keeps name resolution on IPv4 only, which avoids the broken IPv6 paths many providers have.'));
		o.default = '0';

		// --- advanced ---

		o = s.taboption('advanced', form.Value, 'xray_bin', _('Xray binary'));
		o.default = '/usr/bin/xray';

		o = s.taboption('advanced', form.Value, 'hev_bin', _('hev-socks5-tunnel binary'),
			_('Required in mixed and TUN modes.'));
		o.default = '/usr/bin/hev-socks5-tunnel';

		o = s.taboption('advanced', form.Value, 'socks_port', _('SOCKS port'));
		o.datatype = 'port';
		o.default = '10808';

		o = s.taboption('advanced', form.Value, 'http_port', _('HTTP proxy port'));
		o.datatype = 'port';
		o.default = '10809';

		o = s.taboption('advanced', form.Value, 'tproxy_port', _('Transparent port'));
		o.datatype = 'port';
		o.default = '12345';

		o = s.taboption('advanced', form.Value, 'stats_port', _('Core API port'),
			_('Local only (loopback); used to read the traffic counters.'));
		o.datatype = 'port';
		o.default = '10085';

		o = s.taboption('advanced', form.Value, 'api_port', _('Service API port'),
			_('Local only (loopback); used by this page and the command line tool.'));
		o.datatype = 'port';
		o.default = '8787';

		o = s.taboption('advanced', form.Value, 'tun_name', _('TUN device name'),
			_('If you change this, update the xwrt firewall zone to match, or forwarded traffic is dropped.'));
		o.default = 'xwrt0';
		o.depends('mode', 'tun');
		o.depends('mode', 'mixed');

		o = s.taboption('advanced', form.Value, 'tun_addr', _('TUN address'));
		o.datatype = 'ip4addr';
		o.default = '198.18.0.1';
		o.depends('mode', 'tun');
		o.depends('mode', 'mixed');

		o = s.taboption('advanced', form.Value, 'tun_mtu', _('TUN MTU'));
		o.datatype = 'uinteger';
		o.default = '8500';
		o.depends('mode', 'tun');
		o.depends('mode', 'mixed');

		o = s.taboption('advanced', form.Value, 'fwmark', _('Socket mark base'),
			_('Three marks are derived from this: the core\'s own socket mark, the TPROXY mark, and the mark that steers mixed-mode UDP into the tunnel.'));
		o.default = '0x1e0';

		o = s.taboption('advanced', form.Value, 'route_table', _('Routing table'),
			_('The table that holds the TUN default route.'));
		o.datatype = 'uinteger';
		o.default = '180';

		// The form is LuCI's to build, so the stylesheet goes in beside it
		// once it exists rather than around the call.
		return m.render().then(function(form) {
			return E([], [ xwrt.style(), form ]);
		});
	}
});
