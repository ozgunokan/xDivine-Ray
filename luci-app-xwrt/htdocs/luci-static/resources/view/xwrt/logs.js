'use strict';
'require view';
'require dom';
'require poll';
'require ui';
'require xwrt';

// Two views of the same events, because reading a log and diagnosing a failure
// are different jobs.
//
// The stream is everything, newest at the bottom, filtered by level, component
// and connect step — on a busy router the core writes a line per connection, so
// an unfiltered stream is not something anyone can read.
//
// The failures panel is the error journal: only things that went wrong, kept
// far longer than the stream, each with the underlying output that explains it
// and a suggested fix. A failure from twenty minutes ago is still there after
// the stream has turned over several times, which is the case someone opening
// this page is usually in.

var LIMIT = 300;

// The page opens on info and above, not on everything.
//
// The core writes a debug line for every UDP packet it forwards, so on a
// network with anything streaming, "everything" is a wall of packet accounting
// scrolling past faster than it can be read — and the lines someone opened
// this page for are already above the top of it. Debug is one selection away
// and stays there for when it is the thing being hunted.
var filters = { level: 'info', source: '', step: '' };

function selector(id, label, options, onChange) {
	return E('label', { 'style': 'display:flex;align-items:center;gap:.4em' }, [
		label,
		E('select', {
			'id': id,
			'class': 'cbi-input-select',
			'change': onChange
		}, options.map(function(o) {
			return E('option', { 'value': o[0] }, o[1]);
		}))
	]);
}

function entryLine(e) {
	var origin = xwrt.sourceLabel(e.source);
	if (e.step)
		origin += ' / ' + xwrt.stepLabel(e.step);

	return E('div', {
		'style': 'white-space:pre-wrap;font-family:monospace;font-size:12px;line-height:1.45'
	}, [
		E('span', { 'style': 'color:#9e9e9e' }, xwrt.localTime(e.time) + '  '),
		E('span', { 'style': 'color:#9e9e9e' }, origin + '  '),
		E('span', {
			'style': 'color:' + xwrt.levelColor(e.level) +
				(e.level === 'error' ? ';font-weight:bold' : '')
		}, e.message || '')
	]);
}

function renderEntries(entries) {
	if (!entries || !entries.length)
		return [ E('em', {}, _('No entries match these filters.')) ];
	return entries.map(entryLine);
}

// renderFailure lays out one journal entry as message, then the underlying
// output, then the suggested fix. The order matters: what failed, why, what to
// do — anything else makes the reader hunt.
function renderFailure(e) {
	var meta = xwrt.localDateTime(e.time) +
		(e.step ? '  ·  ' + xwrt.stepLabel(e.step) : '') +
		'  ·  ' + xwrt.sourceLabel(e.source);
	// A repeat count is the difference between a one-off and something stuck in
	// a loop, which changes what the reader should do about it.
	if (e.repeats)
		meta += '  ·  ' + _('repeated %d more times').format(e.repeats);

	var parts = [
		E('div', { 'style': 'display:flex;gap:.6em;flex-wrap:wrap;align-items:baseline' }, [
			E('strong', { 'style': 'color:#f44336' }, xwrt.failureMessage(e)),
			E('span', { 'style': 'color:#9e9e9e;font-size:90%' }, meta)
		])
	];

	if (e.detail)
		parts.push(E('pre', {
			'style': 'margin:.4em 0 0 0;padding:.5em;font-size:12px;overflow-x:auto;' +
				'background:rgba(127,127,127,.18);color:inherit;border-radius:3px'
		}, e.detail));

	var hint = xwrt.failureHint(e);
	if (hint)
		parts.push(E('div', { 'style': 'margin-top:.4em' }, [
			E('span', { 'style': 'color:#2a78d6;font-weight:bold' }, '→ '),
			hint
		]));

	return E('div', {
		'style': 'padding:.7em .8em;margin-bottom:.6em;border-left:3px solid #f44336;' +
			'background:rgba(127,127,127,.12);border-radius:0 3px 3px 0'
	}, parts);
}

function renderFailures(errors) {
	if (!errors || !errors.length)
		return [ E('em', {}, _('No errors have occurred since the service started.')) ];
	// Newest first here, the opposite of the stream: in a list of failures the
	// one that just happened is the one being looked for.
	return errors.slice().reverse().map(renderFailure);
}

function refresh() {
	return Promise.all([
		xwrt.logs(LIMIT, filters.level, filters.source, filters.step),
		xwrt.errors(50)
	]).then(function(res) {
		var logs = res[0] || {};
		var errs = res[1] || {};

		var el = document.getElementById('xwrt-log');
		if (el) {
			var atBottom = (el.scrollHeight - el.scrollTop - el.clientHeight) < 40;
			dom.content(el, renderEntries(logs.entries));
			if (atBottom)
				el.scrollTop = el.scrollHeight;
		}
		var fx = document.getElementById('xwrt-failures');
		if (fx)
			dom.content(fx, renderFailures(errs.errors));
	});
}

// clearAndReport says what a clear actually did, and says so out loud when it
// did nothing. Without the count a cleared stream is indistinguishable from a
// button that does not work: the core writes a line per connection, so the box
// is full again within a second. And a denied call — an ACL the running LuCI
// session predates, most often — would otherwise fail in silence.
function clearAndReport(promise, template) {
	return promise.then(function(res) {
		xwrt.checked(res);
		return refresh().then(function() {
			ui.addNotification(null,
				E('p', template.format((res && res.cleared) || 0)), 'info');
		});
	}).catch(function(err) {
		ui.addNotification(null,
			E('p', _('Could not clear: %s').format(err.message)), 'error');
	});
}

return view.extend({
	load: function() {
		return Promise.all([
			xwrt.logs(LIMIT, filters.level, filters.source, filters.step),
			xwrt.errors(50)
		]);
	},

	render: function(data) {
		var logs = data[0] || {};
		var errs = data[1] || {};

		var box = E('div', {
			'id': 'xwrt-log',
			'style': 'max-height:30em;overflow:auto;background:rgba(127,127,127,.12);' +
				'padding:.6em;border-radius:4px'
		}, renderEntries(logs.entries));

		var onChange = function(ev) {
			filters[ev.target.getAttribute('data-filter')] = ev.target.value;
			refresh();
		};
		var mark = function(sel, name) {
			sel.querySelector('select').setAttribute('data-filter', name);
			return sel;
		};

		var levelSel = mark(selector('xwrt-log-level', _('Level'), [
			[ 'info', _('info and above') ],
			[ '', _('everything, including debug') ],
			[ 'warning', _('warnings and errors') ],
			[ 'error', _('errors only') ]
		], onChange), 'level');

		var sourceSel = mark(selector('xwrt-log-source', _('Component'), [
			[ '', _('all components') ],
			[ 'xwrt', _('service') ],
			[ 'xray', _('proxy core') ],
			[ 'tunnel', _('tunnel') ]
		], onChange), 'source');

		var stepSel = mark(selector('xwrt-log-step', _('Step'), [
			[ '', _('all steps') ],
			[ 'config', _('configuration') ],
			[ 'core', _('proxy core') ],
			[ 'tunnel', _('TUN device') ],
			[ 'firewall', _('firewall rules') ],
			[ 'dns', _('DNS') ],
			[ 'teardown', _('disconnect') ]
		], onChange), 'step');

		poll.add(refresh, 3);

		return E('div', { 'class': 'cbi-map' }, [
			xwrt.style(),
			E('h2', {}, _('Log')),
			E('div', { 'class': 'cbi-map-descr' },
				_('Kept in memory only; nothing is written to flash. Warnings and errors are also copied to the system log, where they survive a restart of the service — read them with <code>logread -e xwrt</code>.')),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Errors')),
				E('div', { 'class': 'cbi-section-descr' },
					_('Kept longer than the stream below; each one carries the output underneath it and what to do about it.')),
				// Clearing is deliberate rather than automatic: a failure that
				// scrolled away on its own would be a failure nobody read.
				E('div', { 'style': 'margin-bottom:.7em' },
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'click': ui.createHandlerFn(this, function() {
							return clearAndReport(xwrt.clearErrors(),
								_('%d error records deleted.'));
						})
					}, _('Clear'))),
				E('div', { 'id': 'xwrt-failures' }, renderFailures(errs.errors))
			]),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Stream')),
				E('div', {
					'style': 'display:flex;gap:1.2em;flex-wrap:wrap;align-items:center;margin-bottom:.7em'
				}, [
					levelSel,
					sourceSel,
					stepSel,
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': function() {
							var el = document.getElementById('xwrt-log');
							if (el)
								el.scrollTop = el.scrollHeight;
						}
					}, _('Jump to the end')),
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'click': ui.createHandlerFn(this, function() {
							return clearAndReport(xwrt.clearLog(),
								_('%d lines deleted. The core writes a line for every connection, so the stream fills up again immediately.'));
						})
					}, _('Clear the stream'))
				]),
				box
			])
		]);
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
