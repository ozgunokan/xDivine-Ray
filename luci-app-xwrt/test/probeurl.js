#!/usr/bin/env node
// The health check address, in the dialog that sets it.
//
// The field existed in the configuration and in the core, and nowhere on any
// screen: the only way to change it was to hand-edit the JSON. That is the
// worst possible place for a setting whose wrong value is invisible — a group
// whose probes all fail looks exactly like a group that is working, because
// every member scores the same and the balancer just uses the first one.
//
// This exercises the dialog the way a person does: open it, type, press Save,
// and see what was sent. Checking only that the input exists would pass on an
// input wired to nothing, which is how this kind of field usually breaks.
//
//   node luci-app-xwrt/test/probeurl.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

var PROFILES = [
	{ id: 'p1', name: 'amsterdam-1', address: '192.0.2.10', port: 443, protocol: 'vless' },
	{ id: 'p2', name: 'frankfurt-2', address: '192.0.2.11', port: 443, protocol: 'vless' }
];

// Opens the group dialog and hands back the pieces a person touches: the field
// itself, and a press of Save that reports what would have been stored.
function dialog(existing) {
	global.MODALS = [];
	global.NOTIFICATIONS = [];

	var sent = null;
	var view = stub.load('view/xwrt/profiles.js');
	view.reload = function() { return Promise.resolve(); };
	global.xwrt.addGroup = function(g) { sent = g; return Promise.resolve({}); };
	global.xwrt.updateGroup = function(id, g) { sent = g; return Promise.resolve({}); };
	global.xwrt.checked = function() {};

	view.handleAddGroup(PROFILES, existing);

	var modal = global.MODALS[global.MODALS.length - 1];
	var inputs = modal.body.querySelectorAll('input');
	var field = null;
	inputs.forEach(function(el) {
		if (el.getAttribute('list') === 'xwrt-probe-urls') field = el;
	});

	return {
		body: modal.body,
		field: field,
		save: function() {
			sent = null;
			var buttons = modal.body.querySelectorAll('button');
			var save = buttons[buttons.length - 1];
			save.click();
			return sent;
		}
	};
}

// 1. The field is there at all, and it is a field — not a line of text saying
//    which address is used.
var d = dialog(null);
check('the group dialog has a health check address field', !!d.field);
check('and it offers addresses to choose from',
	d.body.querySelectorAll('datalist').length === 1);
if (d.field) {
	var options = d.body.querySelectorAll('datalist')[0]
		.querySelectorAll('option').map(function(o) { return o.value; });
	check('Cloudflare is one of them',
		options.indexOf('https://cp.cloudflare.com/generate_204') >= 0);
	check('and so is the default, so the current value is reachable again ' +
		'after changing it',
		options.indexOf('https://www.gstatic.com/generate_204') >= 0);
}

// 2. What is typed is what gets sent. This is the check the field exists for:
//    an input the Save button never reads looks identical on screen.
d = dialog(null);
d.field.value = 'https://cp.cloudflare.com/generate_204';
d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
var sent = d.save();
check('Save sends the address that was typed',
	sent && sent.probe_url === 'https://cp.cloudflare.com/generate_204');
check('and still sends the rest of the group',
	sent && sent.members.length === 1 && sent.strategy === 'leastPing' &&
	sent.probe_interval === '60s');

// 3. Left alone, nothing is forced: an empty value means "use the default",
//    which the daemon fills in. Sending a hard-coded address from here would
//    pin every group to whatever this file happened to say.
d = dialog(null);
d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
sent = d.save();
check('an untouched field sends an empty address, not a copy of the default',
	sent && sent.probe_url === '');

// 4. Editing an existing group shows what that group is actually using, rather
//    than a blank box that silently resets the value on the next save.
d = dialog({
	id: 'g1', name: 'avrupa', strategy: 'leastLoad', members: [ 'p1' ],
	probe_interval: '2m', probe_url: 'https://cp.cloudflare.com/generate_204'
});
check('editing a group shows the address it is using',
	d.field && d.field.value === 'https://cp.cloudflare.com/generate_204');
sent = d.save();
check('and saving without touching it keeps that address',
	sent && sent.probe_url === 'https://cp.cloudflare.com/generate_204');

// 5. Something that is not a URL is refused here, where the person can see the
//    field. Saved, it costs the group its health checks and reports nothing:
//    the tunnel comes up, traffic flows, and the ranking is made of failures.
[ 'cp.cloudflare.com', 'ftp://example.com/x', 'tcp://1.1.1.1:53' ].forEach(function(bad) {
	d = dialog(null);
	d.field.value = bad;
	d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
	var out = d.save();
	check('"' + bad + '" is refused rather than stored', out === null);
	check('and the refusal says so on screen',
		global.NOTIFICATIONS.length > 0);
});

// 6. The warning is not a wall: a group with no members is still the thing
//    being complained about there, and the address check must not have taken
//    that message over.
d = dialog(null);
sent = d.save();
check('a group with no members is still refused for having no members',
	sent === null && (global.NOTIFICATIONS[0].node.textContent || '')
		.indexOf('sunucu') >= 0);

process.exit(fail);
