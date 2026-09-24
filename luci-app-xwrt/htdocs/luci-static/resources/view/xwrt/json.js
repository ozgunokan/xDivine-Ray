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
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							return xwrt.config().then(function(c) {
								if (box())
									box().value = pretty(c);
								dom.content(document.getElementById('xwrt-json-report'), []);
							}).catch(rpcProblem);
						})
					}, _('Reload from the device'))
				]),

				E('div', { 'style': 'margin-top:.6em;opacity:.75;font-size:92%' },
					_('Check says whether the document would be accepted, without saving it. A section left out of the document is refused rather than obeyed — to empty one, give it an empty list.'))
			])
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
