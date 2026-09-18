'use strict';
'require view';
'require dom';
'require ui';
'require xwrt';

// Who wrote this, what it is built on, and where to say thank you.
//
// The version is read from the daemon rather than written here, so the page
// reports the daemon that is actually running — which is the number worth
// having when something is reported from a phone screenshot.

var LINKS = [
	{
		url: 'https://github.com/ozgunokan/xDivine-Ray',
		label: 'github.com/ozgunokan/xDivine-Ray',
		note: _('Source, releases and the issue tracker.')
	},
	{
		url: 'https://t.me/DivineWRT',
		// A public group has a readable name, so it is shown rather than
		// translated: it is an address, and an address people retype.
		label: 't.me/DivineWRT',
		note: _('Questions, new releases and the people who use this.')
	},
	{
		url: 'https://buymeacoffee.com/ozgunokan',
		label: 'buymeacoffee.com/ozgunokan',
		note: _('Support the work on this project.')
	}
];

// The parts this app drives. Naming them is not decoration: when the core or
// the tunnel misbehaves, knowing what they are is the first step to reading
// their own documentation.
var BUILT_ON = [
	[ 'Xray-core', _('the proxy core: the protocols, the routing and the TLS.') ],
	[ 'hev-socks5-tunnel', _('the TUN device, in mixed and TUN modes.') ],
	[ 'OpenWrt / LuCI', _('the system this runs on, and the interface it lives in.') ]
];

// updateBox says one of four things, and offers a button only for the one that
// has an action: a newer version exists, and it can be verified and installed
// on this architecture.
//
// The install button is deliberately not the first thing on the page and
// deliberately says what it is about to do. It replaces the running binary and
// restarts the service — on a router whose only way out is this tunnel, that is
// a minute of no internet, and someone should press it knowing that rather
// than discovering it.
function updateBox(u, view) {
	u = u || {};
	var rows = [];

	if (u.installing) {
		rows.push(E('div', {}, _('Installing %s. The service restarts as part of this, so this page will lose contact with it for a moment.').format(u.latest || '')));
		rows.push(E('div', { 'style': 'opacity:.75;font-size:92%;margin-top:.3em' },
			_('Progress is written to %s.').format(u.log_path || '/tmp/xwrt-update.log')));
		return rows;
	}

	if (u.available) {
		rows.push(E('div', { 'style': 'font-size:105%' }, [
			E('strong', {}, _('Version %s is available.').format(u.latest || '?')),
			' ',
			E('span', { 'style': 'opacity:.75' },
				_('You are running %s.').format(u.current || '?'))
		]));
	} else if (u.latest) {
		rows.push(E('div', {},
			_('%s is the newest version, and it is the one running.').format(u.current || '?')));
	} else if (u.check_error) {
		rows.push(E('div', {}, _('The release page could not be reached.')));
	} else {
		rows.push(E('div', {}, _('No check has been made yet.')));
	}

	var why = u.error_code
		? xwrt.failureMessage({ code: u.error_code, args: u.error_args,
			message: u.check_error })
		: u.check_error;
	if (why)
		rows.push(E('div', { 'style': 'margin-top:.4em;opacity:.8;font-size:92%' }, why));
	if (u.checked_at)
		rows.push(E('div', { 'style': 'margin-top:.3em;opacity:.65;font-size:92%' },
			_('Last checked: %s').format(u.checked_at.replace('T', ' ').replace('Z', ' UTC'))));

	var buttons = [
		E('button', {
			'class': 'cbi-button',
			'click': ui.createHandlerFn(view, function() {
				return xwrt.updateCheck().then(function(r) {
					dom.content(document.getElementById('xwrt-update'),
						updateBox(r, view));
				}).catch(function(e) {
					ui.addNotification(null, E('p', e.message), 'error');
				});
			})
		}, _('Check now'))
	];

	if (u.available && u.installable) {
		buttons.push(E('button', {
			'class': 'cbi-button cbi-button-action important',
			'click': ui.createHandlerFn(view, function() {
				if (!confirm(_('Install %s now?\n\nThe tunnel goes down while the service restarts, and comes back by itself. If the new version cannot start, the previous one is put back automatically.').format(u.latest)))
					return;
				return xwrt.updateInstall().then(function(r) {
					xwrt.checked(r);
					ui.addNotification(null, E('p',
						_('Installing. This page will lose contact with the daemon for a moment; reload it in a minute.')), 'info');
					dom.content(document.getElementById('xwrt-update'),
						updateBox({ installing: true, latest: u.latest }, view));
				}).catch(function(e) {
					ui.addNotification(null, E('p', e.message), 'error');
				});
			})
		}, _('Install %s').format(u.latest || '')));
	}

	if (u.url)
		buttons.push(link(u.url, _('Release notes')));

	rows.push(E('div', {
		'style': 'margin-top:.8em;display:flex;gap:.5em;flex-wrap:wrap;align-items:center'
	}, buttons));
	return rows;
}

function link(url, text) {
	return E('a', {
		'href': url,
		'target': '_blank',
		'rel': 'noopener noreferrer'
	}, text);
}

return view.extend({
	load: function() {
		// Two questions, neither of which may keep the page from rendering: a
		// daemon that is not answering is exactly when someone opens this page.
		return Promise.all([
			xwrt.status().catch(function() { return {}; }),
			xwrt.update().catch(function() { return {}; })
		]);
	},

	render: function(data) {
		data = data || [];
		var status = data[0] || {};
		var upd = data[1] || {};

		var facts = [
			[ _('Version'), status.version || '-' ],
			[ _('Proxy core'), status.core_version || '-' ],
			[ _('Author'), 'Özgün Okan' ]
		];

		return E('div', { 'class': 'cbi-map' }, [
			xwrt.style(),
			E('h2', {}, _('About')),
			E('div', { 'class': 'cbi-map-descr' },
				_('xDivine-Ray is a transparent proxy manager for OpenWrt: it detects the router\'s own network layout at run time, so the same package works on a device it has never seen before.')),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('This installation')),
				xwrt.scroll(E('div', { 'class': 'table xwrt-table' },
					facts.map(function(f) {
						return E('div', { 'class': 'tr' }, [
							E('div', { 'class': 'td left', 'style': 'width:35%' }, f[0]),
							E('div', { 'class': 'td left' }, f[1])
						]);
					})))
			]),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Updates')),
				E('div', { 'id': 'xwrt-update' }, updateBox(upd, this))
			]),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Project')),
				E('div', {}, LINKS.map(function(l) {
					return E('div', { 'style': 'margin-bottom:.7em' }, [
						E('div', { 'style': 'word-break:break-all' }, link(l.url, l.label)),
						E('div', { 'style': 'opacity:.75;font-size:92%' }, l.note)
					]);
				})),
				E('div', { 'style': 'margin-top:1em' },
					E('a', {
						'class': 'cbi-button cbi-button-action',
						'href': 'https://buymeacoffee.com/ozgunokan',
						'target': '_blank',
						'rel': 'noopener noreferrer'
					}, _('Buy me a coffee'))),
				E('div', { 'style': 'margin-top:.8em;font-size:105%' },
					_('Thank you for your donations.')),
				E('div', { 'style': 'margin-top:.3em;opacity:.75;font-size:92%' },
					_('They pay for the hardware this is tested on and the time that goes into it.'))
			]),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Built on')),
				E('div', {}, BUILT_ON.map(function(b) {
					return E('div', { 'style': 'margin-bottom:.45em' }, [
						E('strong', {}, b[0]),
						' — ',
						E('span', { 'style': 'opacity:.8' }, b[1])
					]);
				}))
			])
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
