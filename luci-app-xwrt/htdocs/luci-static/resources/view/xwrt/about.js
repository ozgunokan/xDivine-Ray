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
		var target = u.target || u.latest || '';
		rows.push(E('div', {}, _('Installing %s. The service restarts as part of this, so this page will lose contact with it for a moment.').format(target)));
		rows.push(E('div', { 'style': 'opacity:.75;font-size:92%;margin-top:.3em' },
			_('Progress is written to %s.').format(u.log_path || '/tmp/xwrt-update.log')));
		// A page opened, or reloaded, in the middle of an install can pick the
		// window up again rather than guess.
		if (target)
			rows.push(E('div', { 'style': 'margin-top:.6em' },
				E('button', {
					'class': 'cbi-button cbi-button-action',
					'click': function() { installWindow(target); }
				}, _('Show progress'))));
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
					installWindow(u.latest);
				}).catch(function(e) {
					// Already running — from another tab, or a press that did
					// not seem to take. That is the install that was wanted;
					// watch it rather than refuse it.
					if (/already being installed/.test((e && e.message) || ''))
						return installWindow(u.latest);
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

// --- the install window -------------------------------------------------
//
// An update used to be a button followed by silence. Nothing on the page could
// tell a download that was progressing from one that had stalled, or either
// from an install that had quietly failed — and on a router whose only way out
// is this tunnel, "wait a minute and reload" is advice somebody follows with
// their stomach.
//
// So the window shows each step as it happens. The first five are reported by
// the daemon itself. The sixth cannot be: the installer stops the service to
// replace it, so the thing being asked is the thing that is gone. That step is
// read from the daemon going quiet, and it ends when the daemon answers again
// — on the new version, which is success, or on the old one, which means the
// installer put it back, and the window says so and shows the installer's log.
//
// Finished, it reloads the page itself. Not before fetching this app's own
// files past the browser's cache, though: a plain reload keeps the old
// JavaScript, and the result is a page that is half one version and half the
// next — which looks like a broken release and is only a cache.

var STEPS = [
	[ 'checking',    _('Checking the release') ],
	[ 'downloading', _('Downloading') ],
	[ 'verifying',   _('Verifying the checksum') ],
	[ 'unpacking',   _('Unpacking') ],
	[ 'installing',  _('Installing') ],
	[ 'restarting',  _('Restarting the service') ],
	[ 'done',        _('Reloading the page') ]
];

// This app's own interface files, fetched fresh before the page reloads. A
// test compares this list with the files that ship, so a new page cannot be
// added and left behind on the old version.
var OWN_FILES = [
	'xwrt.js',
	'xwrt/i18n.js',
	'view/xwrt/about.js',
	'view/xwrt/json.js',
	'view/xwrt/logs.js',
	'view/xwrt/profiles.js',
	'view/xwrt/rules.js',
	'view/xwrt/settings.js',
	'view/xwrt/status.js',
	'view/xwrt/traffic.js'
];

var POLL_MS = 1000;
// How long the service may stay silent after the installer takes it down
// before the window stops waiting. The installer's own ceiling on waiting for
// the new daemon is well inside this.
var DOWN_LIMIT_MS = 180000;
// How long an install may show no sign of having started at all.
var START_LIMIT_MS = 30000;

// The clock, replaceable so a test can walk the whole sequence without waiting
// for it.
var clock = {
	later: function(fn, ms) { return setTimeout(fn, ms); },
	now: function() { return Date.now(); }
};

function stepIndex(stage) {
	for (var i = 0; i < STEPS.length; i++)
		if (STEPS[i][0] === stage)
			return i;
	return 0;
}

function megabytes(n) {
	return (n / 1048576).toFixed(1);
}

function renderSteps(st) {
	var active = stepIndex(st.stage);
	var rows = STEPS.map(function(step, i) {
		var mark, colour, weight = 'normal';
		if (st.failed && i === active) {
			mark = '✗'; colour = '#c62828'; weight = 'bold';
		} else if (i < active || (st.finished && i === active)) {
			mark = '✓'; colour = '#2e7d32';
		} else if (i === active) {
			mark = '▸'; colour = 'inherit'; weight = 'bold';
		} else {
			mark = '○'; colour = '#9e9e9e';
		}

		var line = [
			E('span', { 'style': 'display:inline-block;width:1.4em;color:' + colour }, mark),
			E('span', { 'style': 'font-weight:' + weight + (i > active ? ';color:#9e9e9e' : '') }, step[1])
		];

		// The download is the one step long enough to need a bar: a few
		// megabytes over a mobile uplink can take a while, and a number that
		// moves is the difference between waiting and worrying.
		if (step[0] === 'downloading' && i === active && !st.failed) {
			var pct = st.total > 0 ? Math.min(100, Math.round(st.done * 100 / st.total)) : 0;
			line.push(E('span', { 'style': 'margin-left:.6em;opacity:.8' },
				st.total > 0
					? '%s / %s MB'.format(megabytes(st.done), megabytes(st.total))
					: '%s MB'.format(megabytes(st.done))));
			line.push(E('div', {
				'style': 'margin:.35em 0 0 1.4em;height:.45em;border-radius:.25em;background:rgba(127,127,127,.25);overflow:hidden'
			}, E('div', {
				'class': 'xwrt-update-bar',
				'style': 'height:100%;width:' + pct + '%;background:#1e88e5;transition:width .4s'
			})));
		}
		return E('div', { 'style': 'margin:.35em 0' }, line);
	});

	if (st.message)
		rows.push(E('div', {
			'class': st.failed ? 'alert-message warning' : '',
			'style': 'margin-top:.9em'
		}, st.message));
	if (st.log)
		rows.push(E('pre', {
			'style': 'margin-top:.6em;max-height:14em;overflow:auto;font-size:85%;white-space:pre-wrap'
		}, st.log));
	if (st.failed)
		rows.push(E('div', { 'class': 'right', 'style': 'margin-top:.9em' },
			E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Close'))));
	return rows;
}

// refreshOwnFiles fetches every file of this app past the browser's cache, so
// the reload that follows gets the new version of all of them. Both the plain
// address and LuCI's versioned one are refreshed, since which one the page
// loaded depends on the LuCI build. Bounded: a reload a few seconds late is
// better than a window that never closes.
function refreshOwnFiles() {
	var base = function(f) {
		return L.resource ? L.resource(f) : '/luci-static/resources/' + f;
	};
	var v = (L.env && L.env.resource_version) || '';
	var urls = [];
	OWN_FILES.forEach(function(f) {
		urls.push(base(f));
		if (v)
			urls.push(base(f) + '?v=' + v);
	});
	var all = Promise.all(urls.map(function(u) {
		return fetch(u, { cache: 'reload', credentials: 'same-origin' })
			.catch(function() {});
	}));
	var bounded = new Promise(function(resolve) { clock.later(resolve, 5000); });
	return Promise.race([ all, bounded ]);
}

function installWindow(target) {
	var body = E('div', { 'style': 'min-width:18em' });
	var st = {
		stage: 'checking', done: 0, total: 0,
		sawDown: false, downSince: 0, startedAt: clock.now(),
		failed: false, finished: false, message: '', log: ''
	};

	ui.showModal(_('Updating to %s').format(target), [ body ]);
	draw();
	clock.later(tick, POLL_MS);
	return st;

	function draw() {
		dom.content(body, renderSteps(st));
	}

	function showLog() {
		return xwrt.updateLog().then(function(r) {
			if (r && r.log) {
				st.log = r.log;
				draw();
			}
		}).catch(function() {});
	}

	function fail(message) {
		st.failed = true;
		st.message = message;
		draw();
		return showLog();
	}

	function succeed() {
		st.stage = 'done';
		st.finished = true;
		st.message = _('%s is installed. Reloading…').format(target);
		draw();
		return refreshOwnFiles().then(function() {
			location.reload();
		});
	}

	function tick() {
		if (st.failed || st.finished)
			return;

		return xwrt.update().then(function(u) {
			// An answer with no version in it is not an answer from the daemon:
			// it is the RPC layer saying it could not reach it.
			if (!u || !u.current)
				throw new Error('down');

			if (u.current === target)
				return succeed();

			if (u.stage === 'failed') {
				var why = u.error_code
					? xwrt.failureMessage({ code: u.error_code, args: u.error_args,
						message: u.check_error })
					: (u.check_error || _('The update failed.'));
				return fail(why);
			}

			if (u.installing) {
				st.stage = u.stage || st.stage;
				st.done = u.done || 0;
				if (u.total)
					st.total = u.total;
				st.downSince = 0;
				draw();
			} else if (st.sawDown) {
				// Back, and on the version it was on before. The installer
				// could not start the new one and put the old one back.
				return fail(_('%s could not be started, so %s was put back. Nothing else was changed; the installer\'s own account is below.').format(target, u.current));
			} else if (clock.now() - st.startedAt > START_LIMIT_MS) {
				return fail(_('The install did not start.'));
			}
			clock.later(tick, POLL_MS);
		}).catch(function() {
			// Not answering. Once the installer has been started this is the
			// service being replaced, which is the plan, not a fault.
			st.sawDown = true;
			if (!st.downSince)
				st.downSince = clock.now();
			st.stage = 'restarting';
			draw();
			if (clock.now() - st.downSince > DOWN_LIMIT_MS)
				return fail(_('The service has not come back after three minutes. The installer writes its progress to %s — read it over SSH.').format('/tmp/xwrt-update.log'));
			clock.later(tick, POLL_MS);
		});
	}
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
	handleReset: null,

	// For the tests, which walk the install window through a whole update
	// without a router or a minute to spare.
	_installWindow: installWindow,
	_clock: clock,
	_ownFiles: OWN_FILES
});
