'use strict';
'require view';
'require ui';
'require dom';
'require xwrt';

// The whole configuration, as one document.
//
// Every other page here edits one thing through a form that knows what it is
// editing. This one shows the lot — servers, groups, rules, subscriptions and
// settings — and lets it be typed over. That is useful for three things no
// form does well: reading what is actually stored, changing twenty servers at
// once, and carrying a configuration to another router by copying text.
//
// It is also the one page that can empty a device, so it is arranged to make
// that hard to do by accident. The document is never saved without being
// checked first; the check runs on the daemon, which validates it exactly as
// the save would; and the reply is a list of everything wrong rather than the
// first thing wrong, because someone fixing a hand-written file one error per
// attempt gives up on the fourth attempt.
//
// Saving does not reconnect. The running tunnel keeps running on the
// configuration it was built from, and the banner the other pages already show
// — saved, but not applied — is what says so.

// Anything this page writes into the box is formatted the same way, so a
// reload after a save does not look like a change.
function pretty(obj) {
	return JSON.stringify(obj, null, 2);
}

function box() {
	return document.getElementById('xwrt-json-text');
}

// report renders what came back from a check or a save.
function report(result) {
	var el = document.getElementById('xwrt-json-report');
	if (!el)
		return;

	var problems = (result && result.problems) || [];
	if (problems.length) {
		dom.content(el, E('div', { 'class': 'alert-message warning' }, [
			E('div', {}, E('strong', {},
				_('This document was not saved. %d thing(s) to fix:')
					.format(problems.length))),
			E('ul', { 'style': 'margin:.5em 0 0 1.2em' },
				problems.map(function(p) { return E('li', {}, p); }))
		]));
		return;
	}

	dom.content(el, E('div', { 'class': 'alert-message success' },
		result && result.saved
			? _('Saved. The running connection is unchanged until you connect again.')
			: _('This document is valid. Nothing has been saved yet.')));
}

// A failed RPC is not the same as a rejected document, and saying so matters:
// one means the daemon is not answering, the other means the text is wrong.
function rpcProblem(e) {
	var el = document.getElementById('xwrt-json-report');
	if (!el)
		return;
	dom.content(el, E('div', { 'class': 'alert-message warning' }, [
		E('div', {}, E('strong', {}, _('The daemon did not answer.'))),
		E('div', { 'style': 'margin-top:.3em' }, (e && e.message) || String(e))
	]));
}

// send parses the box and hands the result to the daemon.
//
// Parsing here as well as on the daemon is not duplication for its own sake:
// a syntax error caught in the browser is reported without a round trip, and
// the daemon still refuses anything the browser would have let through.
function send(view, check) {
	var text = box() ? box().value : '';
	var parsed;
	try {
		parsed = JSON.parse(text);
	} catch (e) {
		report({ problems: [ _('This is not valid JSON: %s').format(e.message) ] });
		return Promise.resolve();
	}

	if (!check && !confirm(_('Replace the whole configuration with this document?\n\nServers, groups, rules and settings are all replaced. The running tunnel is not touched until you connect again.')))
		return Promise.resolve();

	return xwrt.putConfig(parsed, !!check).then(function(res) {
		res = res || {};
		report(res);
		// After a save, show what the device actually stored rather than what
		// was typed: the daemon normalises as it writes, and leaving the
		// operator's text on screen would show a document the device does not
		// have.
		if (res.saved && res.config && box())
			box().value = pretty(res.config);
	}).catch(rpcProblem);
}

// --- backup and restore -----------------------------------------------------
//
// The file is the document, unchanged. No wrapper, no header, no format of its
// own — so a backup can be restored by any path that already exists: this page,
// the command line, the API. A wrapper would have to be stripped by every one
// of them, and the first version of it that somebody hand-edited would be the
// version nothing could read.
//
// What the file does not carry is anything that is not this app's: the
// firewall, dnsmasq, the rest of the router. Those belong to OpenWrt's own
// backup.

// backupName is what the browser saves it as. The date and the version are in
// the name because they are not in the file, and a folder of backups nobody can
// tell apart is a folder of one backup.
function backupName(config) {
	var v = (config && config.settings && config.settings.version) || '';
	var d = new Date();
	function two(n) { return (n < 10 ? '0' : '') + n; }
	var stamp = d.getFullYear() + '-' + two(d.getMonth() + 1) + '-' + two(d.getDate()) +
		'-' + two(d.getHours()) + two(d.getMinutes());
	return 'xwrt-backup-' + (v ? v + '-' : '') + stamp + '.json';
}

// io is where files leave the page, replaceable so a test can see what would
// have been downloaded without a browser.
var io = {
	download: function(text, name) { return download(text, name); }
};

// download hands the document to the browser as a file.
function download(text, name) {
	var blob = new Blob([text], { type: 'application/json' });
	var url = URL.createObjectURL(blob);
	var a = E('a', { 'href': url, 'download': name });
	document.body.appendChild(a);
	a.click();
	document.body.removeChild(a);
	// Not immediately: Safari has been known to cancel a download whose object
	// URL was revoked in the same tick.
	window.setTimeout(function() { URL.revokeObjectURL(url); }, 30000);
}

// --- back to a fresh install -------------------------------------------
//
// The one button on this page that cannot be undone by pressing another, so it
// is kept apart from the rest and it asks properly. The window says what goes,
// in numbers read from the device rather than in general terms; it offers the
// backup that would undo it before anything happens; and the button that does
// it stays dead until a box saying "yes, all of it" is ticked.
//
// It disconnects, because a fresh install is not connected. On a line whose
// only way out is the tunnel that means no internet until a server is added
// again — which the window says in so many words, along with the fact that
// adding one from a share link needs no internet at all.

function count(list) {
	return (list && list.length) || 0;
}

function resetWindow(view) {
	return xwrt.config().then(function(c) {
		c = c || {};
		var backedUp = E('span', { 'style': 'margin-left:.6em;color:#2e7d32' });
		var go = E('button', {
			'class': 'cbi-button cbi-button-negative',
			'disabled': 'disabled',
			'click': ui.createHandlerFn(view, function() {
				if (go.disabled)
					return;
				return xwrt.resetConfig().then(function(r) {
					xwrt.checked(r);
					ui.hideModal();
					if (box() && r && r.config)
						box().value = pretty(r.config);
					dom.content(document.getElementById('xwrt-json-report'), []);
					ui.addNotification(null, E('p',
						_('Reset to factory settings. The tunnel is down; add a server to connect again.')), 'info');
				}).catch(function(e) {
					ui.addNotification(null, E('p', (e && e.message) || String(e)), 'error');
				});
			})
		}, _('Reset everything'));

		var agree = E('input', {
			'type': 'checkbox',
			'change': function(ev) {
				go.disabled = !ev.target.checked;
			}
		});
		go.disabled = true;

		ui.showModal(_('Reset to factory settings'), [
			E('p', {}, _('xDivine-Ray on this router goes back to the state it was installed in:')),
			E('ul', { 'style': 'margin:.4em 0 .8em 1.2em' }, [
				E('li', {}, _('%d server(s), %d group(s), %d rule(s) and %d subscription(s) are deleted')
					.format(count(c.profiles), count(c.groups), count(c.rules), count(c.subscriptions))),
				E('li', {}, _('every setting goes back to its default')),
				E('li', {}, E('strong', {}, _('the tunnel is disconnected now')))
			]),
			E('div', { 'class': 'alert-message warning' },
				_('If this router reaches the internet only through the tunnel, there is no internet until a server is added again. This page stays reachable from the local network, and adding a server from a share link does not need the internet.')),
			E('div', { 'style': 'margin:.9em 0' }, [
				E('button', {
					'class': 'cbi-button cbi-button-action',
					'click': function() {
						io.download(pretty(c), backupName(c));
						dom.content(backedUp, '\u2713 ' + _('downloaded'));
					}
				}, _('Download a backup first')),
				backedUp
			]),
			E('label', { 'style': 'display:flex;gap:.5em;align-items:center;margin:.6em 0' }, [
				agree,
				E('span', {}, _('I understand: delete everything and disconnect'))
			]),
			E('div', { 'class': 'right', 'style': 'margin-top:1em' }, [
				E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')),
				' ',
				go
			])
		]);
	}).catch(rpcProblem);
}

return view.extend({
	load: function() {
		return xwrt.config().catch(function() { return null; });
	},

	render: function(config) {
		var self = this;

		return E('div', { 'class': 'cbi-map' }, [
			xwrt.style(),
			E('h2', {}, _('Configuration as JSON')),
			E('div', { 'class': 'cbi-map-descr' },
				_('Everything this app stores, in one document: servers, groups, rules, subscriptions and settings. Edit it here, or copy it to move a configuration to another router.')),

			E('div', { 'class': 'cbi-section' }, [
				// Said before the box rather than after it, because after it is
				// below the fold on a phone and this is the part that stops a
				// configuration being pasted into a chat window.
				E('div', { 'class': 'alert-message warning' },
					_('This document contains your server credentials in full. Treat a copy of it the way you would treat the share links themselves.')),

				E('div', { 'id': 'xwrt-json-report' }, []),

				E('textarea', {
					'id': 'xwrt-json-text',
					'rows': 24,
					'spellcheck': 'false',
					'autocomplete': 'off',
					'autocapitalize': 'off',
					'style': 'width:100%;box-sizing:border-box;' +
						'font-family:monospace;font-size:92%;white-space:pre'
				}, config ? pretty(config) : ''),

				E('div', {
					'style': 'margin-top:.8em;display:flex;gap:.5em;flex-wrap:wrap;align-items:center'
				}, [
					E('button', {
						'class': 'cbi-button cbi-button-action',
						'click': ui.createHandlerFn(self, function() {
							return send(self, true);
						})
					}, _('Check')),
					E('button', {
						'class': 'cbi-button cbi-button-positive important',
						'click': ui.createHandlerFn(self, function() {
							return send(self, false);
						})
					}, _('Save')),
					// Named for what it does, with the rest on hover. It used to
					// be called "Reload from the device", which reads as
					// "restore the device" — nearly the opposite.
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'title': _('Puts the box back to what is saved on the device. Nothing is saved or reset.'),
						'click': ui.createHandlerFn(self, function() {
							return xwrt.config().then(function(c) {
								if (box())
									box().value = pretty(c);
								dom.content(document.getElementById('xwrt-json-report'), []);
							}).catch(rpcProblem);
						})
					}, _('Discard edits')),

					// Straight from the device rather than from the box: what
					// gets backed up should be what is stored, not what someone
					// has half-typed over it.
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							return xwrt.config().then(function(c) {
								io.download(pretty(c), backupName(c));
							}).catch(rpcProblem);
						})
					}, _('Download a backup')),

					// Loaded into the box rather than saved. The operator sees
					// what is about to replace their device and presses Check
					// or Save themselves — a file picker that writes straight
					// to the router is one misclick from an empty device.
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							var picker = document.getElementById('xwrt-json-file');
							if (picker)
								picker.click();
						})
					}, _('Load a backup file…')),

					E('input', {
						'type': 'file',
						'id': 'xwrt-json-file',
						'accept': '.json,application/json',
						'style': 'display:none',
						'change': function(ev) {
							var f = ev.target.files && ev.target.files[0];
							if (!f)
								return;
							var reader = new FileReader();
							reader.onload = function() {
								if (box())
									box().value = String(reader.result);
								report({ problems: [], loaded: true });
							};
							reader.readAsText(f);
							// So picking the same file twice in a row fires.
							ev.target.value = '';
						}
					})
				]),

				E('div', { 'style': 'margin-top:.6em;opacity:.75;font-size:92%' },
					_('Check says whether the document would be accepted, without saving it. A section left out of the document is refused rather than obeyed — to empty one, give it an empty list.')),

				E('div', { 'style': 'margin-top:.4em;opacity:.75;font-size:92%' },
					_('A backup file is this document and nothing else, so it can be restored from here or with "xwrt restore". Loading a file only fills the box — press Check, then Save. It does not carry the firewall or anything else outside this app.'))
			]),

			// Apart from the buttons above, on purpose: those change the box,
			// this one changes the device and cannot be taken back by the next
			// button along.
			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Factory settings')),
				E('div', { 'class': 'cbi-section-descr' },
					_('Takes this app back to a fresh install: no servers, groups, rules or subscriptions, every setting at its default, disconnected.')),
				E('button', {
					'class': 'cbi-button cbi-button-negative',
					'click': ui.createHandlerFn(self, function() { return resetWindow(self); })
				}, _('Reset to factory settings'))
			])
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null,

	// For the tests.
	_io: io,
	_resetWindow: resetWindow
});
