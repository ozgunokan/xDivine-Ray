'use strict';
'require view';
'require ui';
'require dom';
'require poll';
'require xwrt';

// Connection status and the connect/disconnect controls.
//
// The detection panel is the important part of this page: it shows which LAN
// and WAN devices the daemon found and which firewall backend it is using, so
// a device where auto-detection guessed wrong is diagnosable without SSH.

function row(label, value, id) {
	return E('div', { 'class': 'tr' }, [
		E('div', { 'class': 'td left', 'style': 'width:35%' }, label),
		E('div', { 'class': 'td left', 'id': id }, value)
	]);
}

// autoConnectCell says whether this device will bring the tunnel up by itself.
//
// A tick on its own would be terse to the point of being a riddle — a tick
// meaning what, exactly? — so the mark carries a word. Green for on, plain
// grey for off rather than red: off is a choice someone may have made on
// purpose, not a fault, and a status page that shouts at a deliberate setting
// teaches people to ignore its colours.
function autoConnectCell(status) {
	if (status.auto_connect)
		return E('span', { 'style': 'color:#2e7d32' }, '\u2713 ' + _('on'));
	return E('span', { 'style': 'color:#9e9e9e' }, [
		'\u2717 ' + _('off'),
		E('span', { 'style': 'font-size:90%' },
			' \u2014 ' + _('the tunnel will not come up on its own'))
	]);
}

// The supported floor, mirrored from the daemon. The allowance matches the one
// in internal/netenv: a nominal 128 MB board always reports somewhat less.
var MIN_SPEC_MB = 128;
var SPEC_ALLOWANCE_MB = 24;

// spec renders a hardware figure, flagged when it is under the floor.
function spec(valueMB, text) {
	if (!valueMB)
		return '-';
	if (valueMB >= MIN_SPEC_MB - SPEC_ALLOWANCE_MB)
		return text;
	return E('span', { 'style': 'color:#f44336' },
		text + ' — ' + _('below the %d MB this version targets').format(MIN_SPEC_MB));
}

// targetCell names what the daemon is connected to, and for a group says how
// it is choosing between members — the difference between a group that fails
// over and one that does not is worth seeing at a glance.
//
// It also names the member traffic is going through, which the group's own
// name never did. On a router pointed at five servers, "grup (5 sunucu, en
// hızlı)" answers none of the question anyone has. The balancer does not
// announce its choice, so this comes from the members' counters: the ones that
// moved since the last reading. Under random or in turn that is more than one
// name, and it says so rather than picking a tidier answer than the truth.
function targetCell(status) {
	if (!status.profile_name)
		return '-';
	if (status.target_kind !== 'group')
		return status.profile_name;

	var parts = [
		status.profile_name,
		E('span', { 'style': 'color:#9e9e9e' },
			'  (' + _('group of %d').format(status.group_members || 0) + ', ' +
			xwrt.strategyLabel(status.group_strategy) + ')')
	];

	var live = status.group_live || [];
	if (live.length)
		parts.push(E('div', { 'style': 'margin-top:.2em' }, [
			E('span', { 'style': 'color:#9e9e9e' }, _('through') + ' '),
			E('strong', {}, live.join(', '))
		]));
	else if (status.connected)
		// Connected, and nothing has moved between the last two readings.
		// Saying "through: nothing" would be wrong — the tunnel is up, it is
		// simply idle — and naming a server anyway would be a guess.
		parts.push(E('div', { 'style': 'margin-top:.2em;color:#9e9e9e' },
			_('no traffic yet, so no member has been used')));

	return E('span', {}, parts);
}

// memberTable lists every member of the running group with what it has carried.
//
// It is here rather than on the Servers page because it is about this
// connection: the same server in two groups has two different stories, and the
// numbers reset when the core restarts.
function memberTable(status) {
	var usage = status.group_usage || [];
	if (!usage.length)
		return [];

	var rows = usage.map(function(m) {
		return E('div', { 'class': 'tr' }, [
			E('div', { 'class': 'td' }, m.live
				? E('span', {}, [
					E('span', { 'style': 'color:#4caf50' }, '● '),
					E('strong', {}, m.name)
				])
				: E('span', { 'style': 'opacity:.75' }, m.name)),
			E('div', { 'class': 'td' }, memberState(m)),
			E('div', { 'class': 'td' }, memberPing(m)),
			E('div', { 'class': 'td' }, xwrt.formatBytes(m.uplink)),
			E('div', { 'class': 'td' }, xwrt.formatBytes(m.downlink))
		]);
	});

	return [
		E('h3', {}, _('Servers in this group')),
		xwrt.scroll(E('div', { 'class': 'table xwrt-table' }, [
			E('div', { 'class': 'tr table-titles' }, [
				E('div', { 'class': 'th' }, _('Server')),
				E('div', { 'class': 'th' }, _('Now')),
				E('div', { 'class': 'th' }, _('Latency')),
				E('div', { 'class': 'th' }, _('Sent')),
				E('div', { 'class': 'th' }, _('Received'))
			])
		].concat(rows)))
	];
}

// memberState says what the balancer is doing with this member.
//
// The three answers are different things and were all shown as "-" before: the
// one being used, one that is healthy and waiting, and one the balancer has
// dropped because it failed its last health check. The last of those is the
// one worth seeing — a group quietly running on two of its three servers looks
// exactly like a group running on three.
function memberState(m) {
	if (m.live)
		return E('strong', {}, _('in use'));
	if (m.rank > 1)
		return E('span', { 'style': 'opacity:.75' },
			_('standby (%d.)').format(m.rank));
	if (m.rank === 0 && m.unreachable)
		return E('span', { 'style': 'color:#d9534f' }, _('not answering'));
	return E('span', { 'style': 'opacity:.75' }, '-');
}

// memberPing is the handshake time to that server, measured from the router.
//
// Deliberately not called "ping": nothing sends an ICMP echo here, and the
// number is a TCP handshake to the server's own port — the part of the core's
// own measurement that differs between members.
function memberPing(m) {
	if (m.unreachable)
		return E('span', { 'style': 'color:#d9534f' }, _('no answer'));
	if (!m.latency_ms)
		return E('span', { 'style': 'opacity:.6' }, '-');
	return E('span', {}, m.latency_ms + ' ms');
}

// failureBanner shows the last failure the daemon recorded: what failed, at
// which step, and what to do about it. It answers the question a status page
// usually leaves hanging — "it says disconnected, but why?" — without making
// anyone open the log page first.
//
// It is dismissible because the failure survives the thing that caused it: a
// connect that failed at 3am should still be visible at 9am, but once it has
// been read it should stop shouting.
function failureBanner(failure) {
	if (!failure || !failure.message)
		return [];

	var head = xwrt.failureMessage(failure);
	if (failure.step)
		head = _('%s failed').format(xwrt.stepLabel(failure.step)) + ': ' + head;

	var parts = [ E('div', {}, E('strong', {}, head)) ];
	var hint = xwrt.failureHint(failure);
	if (hint)
		parts.push(E('div', { 'style': 'margin-top:.3em' }, hint));
	parts.push(E('div', { 'style': 'margin-top:.5em;display:flex;gap:.5em;align-items:center' }, [
		E('a', { 'href': L.url('admin', 'vpn', 'xdivine-ray', 'logs') }, _('Open the log')),
		E('button', {
			'class': 'cbi-button cbi-button-neutral',
			'click': ui.createHandlerFn(null, function() {
				return xwrt.clearError().then(function() {
					dom.content(document.getElementById('xwrt-error'), []);
				});
			})
		}, _('Dismiss')),
		E('span', { 'style': 'color:#9e9e9e;font-size:90%' },
			xwrt.localDateTime(failure.time))
	]));

	return E('div', { 'class': 'alert-message warning' }, parts);
}

// --- self-test ----------------------------------------------------------
//
// Three measurements answer the question people actually ask ("why is the VPN
// slow?"): the link to the server, a direct connection, and the same connection
// through the tunnel. Tunnel minus direct is what the tunnel costs; a wide
// spread with a steady server means the loss is past the server, where nothing
// on this router can help.

var TEST_ROUNDS = 8;

function ms(v) {
	if (v === undefined || v === null || v === 0)
		return '-';
	return Math.round(v) + ' ms';
}

function statRow(label, st, note) {
	// A missing leg still gets a row: "the server was not measured because
	// nothing is connected" is part of the answer, not an empty space.
	if (!st)
		return E('div', { 'class': 'tr' }, [
			E('div', { 'class': 'td left' }, label),
			E('div', { 'class': 'td left' }, '-'),
			E('div', { 'class': 'td left' }, '-'),
			E('div', { 'class': 'td left' }, '-'),
			E('div', { 'class': 'td left' },
				E('span', { 'style': 'color:#9e9e9e' }, _('not measured')))
		]);

	var failed = st.failures
		? E('span', { 'style': 'color:#f44336' },
			_('%d errors').format(st.failures))
		: '';

	return E('div', { 'class': 'tr' }, [
		E('div', { 'class': 'td left' }, [
			label,
			note ? E('div', { 'style': 'color:#9e9e9e;font-size:90%' }, note) : ''
		]),
		E('div', { 'class': 'td left' }, ms(st.median_ms)),
		E('div', { 'class': 'td left' }, ms(st.min_ms)),
		E('div', { 'class': 'td left' }, ms(st.max_ms)),
		E('div', { 'class': 'td left' }, failed)
	]);
}

function renderTest(rep) {
	if (!rep)
		return [];

	var tun = rep.tunnel || {};
	var dir = rep.direct || {};
	var overhead = (tun.median_ms && dir.median_ms)
		? Math.round(tun.median_ms - dir.median_ms) : null;

	// With the router's own traffic proxied, the two unproxied rows are not
	// measured at all — the daemon says so rather than sending numbers it
	// knows are the tunnel wearing another label.
	var rows = [
		E('div', { 'class': 'tr table-titles' }, [
			E('div', { 'class': 'th left' }, _('Measurement')),
			E('div', { 'class': 'th left' }, _('Median')),
			E('div', { 'class': 'th left' }, _('Best')),
			E('div', { 'class': 'th left' }, _('Worst')),
			E('div', { 'class': 'th left' }, '')
		])
	];
	if (!rep.skip_code)
		rows.push(
			statRow(_('Link to the server'), rep.server,
				_('router to server only')),
			statRow(_('Direct'), rep.direct,
				_('to %s, without the tunnel').format(rep.target || '')));
	rows.push(statRow(_('Through the tunnel'), rep.tunnel,
		_('to the same address, through the proxy')));

	var parts = [ xwrt.scroll(E('div', { 'class': 'table xwrt-table' }, rows)) ];

	if (rep.skip_code === 'proxy-router')
		parts.push(E('div', {
			'class': 'alert-message',
			'style': 'margin-top:.6em'
		}, [
			E('strong', {}, _('Only the tunnel was measured.')), ' ',
			_('The router\'s own traffic is proxied too, so the connection this test would use as its unproxied reference goes through the tunnel as well. Measuring it anyway would show the tunnel twice under two names. Turn that setting off for a moment to get the comparison.')
		]));

	if (overhead !== null && !rep.skip_code)
		parts.push(E('div', { 'style': 'margin-top:.6em' }, [
			E('strong', {}, _('The tunnel costs %d ms').format(overhead))
		]));

	// The verdict is the part worth reading; the numbers above are the evidence.
	parts.push(E('div', {
		'class': 'alert-message ' + (rep.verdict_code === 'healthy' ? 'info' : 'warning'),
		'style': 'margin-top:.6em'
	}, xwrt.verdictText(rep)));

	if (tun.error)
		parts.push(E('div', { 'style': 'margin-top:.4em;color:#9e9e9e;font-size:90%' },
			_('Tunnel error: %s').format(tun.error)));

	parts.push(E('div', { 'style': 'margin-top:.4em;color:#9e9e9e;font-size:90%' },
		_('Measured at %s.').format(xwrt.localDateTime(rep.taken_at))));

	return parts;
}

// actionButtons shows the action that applies right now rather than every
// action that exists: one button, Connect or Disconnect, matching the state.
//
// The one case that needs two is picking a different server while connected —
// there, switching and hanging up are genuinely different things to want, and
// the switch button only appears once the selection has actually been changed.
function actionButtons(status, select, view) {
	var connected = !!status.connected;
	var switching = connected && select.value &&
		select.value !== (status.profile_id || '');

	var connect = E('button', {
		'class': 'cbi-button cbi-button-apply',
		'click': ui.createHandlerFn(view, function() {
			return view.handleConnect(select);
		})
	}, switching ? _('Switch to this server') : _('Connect'));

	var disconnect = E('button', {
		'class': 'cbi-button cbi-button-reset',
		'click': ui.createHandlerFn(view, 'handleDisconnect')
	}, _('Disconnect'));

	if (!connected)
		return [ connect ];
	if (switching)
		return [ connect, disconnect ];
	return [ disconnect ];
}

function stateBadge(status) {
	var text, color;
	if (status.connected && status.core_running) {
		text = _('Connected');
		color = '#4caf50';
	} else if (status.connected) {
		text = _('The core is not running');
		color = '#ff9800';
	} else {
		text = _('Not connected');
		color = '#9e9e9e';
	}
	return E('span', {
		'style': 'background:' + color + ';color:#fff;padding:2px 10px;' +
			'border-radius:10px;font-weight:bold;white-space:nowrap'
	}, text);
}

return view.extend({
	load: function() {
		return Promise.all([
			xwrt.status(),
			xwrt.config(),
			xwrt.env()
		]);
	},

	handleConnect: function(select, ev) {
		var id = select.value;
		if (!id) {
			ui.addNotification(null, E('p', _('Pick a server first.')), 'warning');
			return;
		}
		ui.showModal(_('Connecting…'), [
			E('p', { 'class': 'spinning' }, _('Starting the core and applying the rules'))
		]);
		return xwrt.connect(id).then(function(res) {
			ui.hideModal();
			xwrt.checked(res);
			ui.addNotification(null, E('p', _('Connected.')), 'info');
		}).catch(function(err) {
			ui.hideModal();
			ui.addNotification(null, E('p', _('Could not connect: %s').format(err.message)), 'error');
		});
	},

	handleDisconnect: function(ev) {
		ui.showModal(_('Disconnecting…'), [
			E('p', { 'class': 'spinning' }, _('Removing the rules'))
		]);
		return xwrt.disconnect().then(function(res) {
			ui.hideModal();
			xwrt.checked(res);
			ui.addNotification(null, E('p', _('Disconnected.')), 'info');
		}).catch(function(err) {
			ui.hideModal();
			ui.addNotification(null, E('p', _('Could not disconnect: %s').format(err.message)), 'error');
		});
	},

	// handleSelfTest runs the measurement. It takes several seconds by design —
	// one sample would be meaningless, since the whole point is the spread — so
	// the modal stays up rather than leaving the page looking stuck.
	handleSelfTest: function(ev) {
		ui.showModal(_('Testing'), [
			E('p', { 'class': 'spinning' },
				_('Measuring the connections; this takes a few seconds.'))
		]);
		return xwrt.selftest(TEST_ROUNDS, '').then(function(rep) {
			ui.hideModal();
			xwrt.checked(rep);
			dom.content(document.getElementById('xwrt-selftest'), renderTest(rep));
		}).catch(function(err) {
			ui.hideModal();
			dom.content(document.getElementById('xwrt-selftest'),
				E('div', { 'class': 'alert-message warning' },
					_('Could not run the test: %s').format(err.message)));
		});
	},

	render: function(data) {
		var status = data[0] || {};
		var config = data[1] || {};
		var env = data[2] || {};
		var profiles = config.profiles || [];
		var settings = config.settings || {};

		var groups = config.groups || [];
		var current = status.profile_id || settings.active;

		// Groups are listed first: when one exists it is usually the intended
		// target, and it is the only thing that fails over.
		var options = [];
		if (groups.length) {
			options.push(E('optgroup', { 'label': _('Groups') },
				groups.map(function(g) {
					return E('option', {
						'value': g.id,
						'selected': (g.id === current) ? '' : null
					}, (g.name || g.id) + '  —  ' + xwrt.strategyLabel(g.strategy) +
						', ' + _('%d servers').format((g.members || []).length));
				})));
		}
		if (profiles.length) {
			options.push(E('optgroup', { 'label': _('Servers') },
				profiles.map(function(p) {
					return E('option', {
						'value': p.id,
						'selected': (p.id === current) ? '' : null
					}, xwrt.profileLabel(p) + '  —  ' + xwrt.transportLabel(p));
				})));
		}
		if (!options.length)
			options = [ E('option', { 'value': '' }, _('No servers defined')) ];

		var self = this;
		var lastStatus = status;

		var redrawActions = function(s) {
			lastStatus = s || lastStatus;
			var box = document.getElementById('xwrt-actions');
			if (box)
				dom.content(box, actionButtons(lastStatus, select, self));
		};

		var select = E('select', {
			'class': 'cbi-input-select',
			'id': 'xwrt-profile',
			// Choosing another server while connected turns the button into a
			// switch, so the control has to follow the selection.
			'change': function() { redrawActions(null); }
		}, options);

		var connectionTable = xwrt.scroll(E('div', { 'class': 'table xwrt-table' }, [
			row(_('Status'), stateBadge(status), 'xwrt-state'),
			row(_('Connected to'), targetCell(status), 'xwrt-server'),
			row(_('Mode'), xwrt.modeName(status.mode || settings.mode), 'xwrt-mode'),
			row(_('Uptime'), status.connected ? xwrt.formatUptime(status.uptime_seconds) : '-', 'xwrt-uptime'),
			row(_('Start on boot'), autoConnectCell(status), 'xwrt-autoconnect'),
			row(_('Upload'), xwrt.formatBytes(status.stats && status.stats.uplink) +
				' (' + xwrt.formatRate(status.stats && status.stats.uplink_rate) + ')', 'xwrt-up'),
			row(_('Download'), xwrt.formatBytes(status.stats && status.stats.downlink) +
				' (' + xwrt.formatRate(status.stats && status.stats.downlink_rate) + ')', 'xwrt-down')
		]));

		var detectionTable = xwrt.scroll(E('div', { 'class': 'table xwrt-table' }, [
			row(_('LAN devices'), (env.lan_devices || []).join(', ') || _('not detected')),
			row(_('LAN networks'), (env.lan_cidrs || []).join(', ') || '-'),
			row(_('WAN device'), env.wan_device || _('not detected')),
			row(_('WAN gateway'), env.wan_gateway || '-'),
			row(_('Firewall'), env.firewall || '-'),
			// No TPROXY row here. This table answers "what did the daemon find
			// on this box" — devices, networks, the gateway — and whether the
			// kernel has a module is a fact about a capture mode nobody on this
			// page is choosing. A mode that needs it and cannot have it is
			// refused at connect time, with a sentence that says so, which is
			// where that belongs.
			row(_('Memory'), spec(env.mem_total_mb, env.mem_total_mb + ' MB')),
			row(_('Storage'), spec(env.storage_total_mb,
				env.storage_total_mb + ' MB (' +
				_('%d MB free').format(env.storage_free_mb || 0) + ')')),
			// The running build, not the installed one. An upgrade that did not
			// restart the service leaves the old process answering, and this is
			// the only place that difference is visible.
			row(_('Version'), [
				(status.version || '?') +
					(status.core_version ? '  ·  ' + _('core %s').format(status.core_version) : ''),
				// An update nobody is told about is an update nobody installs,
				// and this is the row people already look at when they wonder
				// which version they are on.
				status.update_available
					? E('a', {
						'href': L.url('admin', 'vpn', 'xwrt', 'about'),
						'style': 'margin-left:.6em'
					}, _('%s is available').format(status.update_version || ''))
					: ''
			])
		]));

		var errorBox = E('div', { 'id': 'xwrt-error' }, failureBanner(status.last_error));

		poll.add(function() {
			return xwrt.status().then(function(s) {
				s = s || {};
				var st = s.stats || {};
				dom.content(document.getElementById('xwrt-state'), stateBadge(s));
				dom.content(document.getElementById('xwrt-server'), targetCell(s));
				dom.content(document.getElementById('xwrt-mode'), xwrt.modeName(s.mode));
				dom.content(document.getElementById('xwrt-uptime'),
					s.connected ? xwrt.formatUptime(s.uptime_seconds) : '-');
				dom.content(document.getElementById('xwrt-autoconnect'),
					autoConnectCell(s));
				dom.content(document.getElementById('xwrt-up'),
					xwrt.formatBytes(st.uplink) + ' (' + xwrt.formatRate(st.uplink_rate) + ')');
				dom.content(document.getElementById('xwrt-down'),
					xwrt.formatBytes(st.downlink) + ' (' + xwrt.formatRate(st.downlink_rate) + ')');
				dom.content(document.getElementById('xwrt-error'),
					failureBanner(s.last_error));
				redrawActions(s);
			});
		}, 3);

		return E('div', { 'class': 'cbi-map' }, [
			xwrt.style(),
			E('h2', {}, _('xDivine-Ray')),
			E('div', { 'class': 'cbi-map-descr' },
				_('A VPN manager that works out this device\'s network layout while it runs.')),

			errorBox,

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Connection')),
				connectionTable,
				E('div', { 'style': 'margin-top:1em;display:flex;gap:.5em;flex-wrap:wrap;align-items:center' }, [
					select,
					E('span', { 'id': 'xwrt-actions', 'style': 'display:flex;gap:.5em' },
						actionButtons(status, select, self))
				])
			].concat(memberTable(status))),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Speed and latency test')),
				E('div', { 'class': 'cbi-section-descr' },
					_('Connects to the same address both directly and through the tunnel, and shows the difference. This is how you see where the delay comes from.')),
				E('div', { 'style': 'margin-bottom:.7em' },
					E('button', {
						'class': 'cbi-button cbi-button-action',
						'click': ui.createHandlerFn(this, 'handleSelfTest')
					}, _('Run the test'))),
				E('div', { 'id': 'xwrt-selftest' })
			]),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Detected network')),
				E('div', { 'class': 'cbi-section-descr' },
					_('These values are detected at connect time. Fill one in from Settings only if it is wrong.')),
				detectionTable
			])
		]);
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
