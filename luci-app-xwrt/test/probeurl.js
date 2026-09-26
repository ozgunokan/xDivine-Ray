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
	// The dropdown is the one whose options are addresses; the dialog has
	// three selects and picking by position would pass on the wrong one.
	var field = null, custom = null;
	modal.body.querySelectorAll('select').forEach(function(el) {
		el.querySelectorAll('option').forEach(function(o) {
			if (o.value.indexOf('http') === 0) field = el;
		});
	});
	modal.body.querySelectorAll('input[type=text]').forEach(function(el) {
		if (String(el.getAttribute('placeholder') || '').indexOf('generate_204') >= 0)
			custom = el;
	});

	return {
		body: modal.body,
		field: field,
		custom: custom,
		pickOther: function() {
			field.value = '__other__';
			field.listeners['change'].forEach(function(fn) { fn(); });
		},
		save: function() {
			sent = null;
			var buttons = modal.body.querySelectorAll('button');
			var save = buttons[buttons.length - 1];
			save.click();
			return sent;
		}
	};
}

// 1. The field is there at all, it is a real dropdown, and Cloudflare is what
//    a new group gets. A datalist on a text box was the first attempt: the
//    themes style the input, the arrow belongs to the browser, and clicking it
//    showed a grey tooltip of the current value rather than the choices.
var d = dialog(null);
check('the group dialog has a health check address dropdown', !!d.field);
check('and a box for an address that is not on the list', !!d.custom);
if (d.field) {
	var options = d.field.querySelectorAll('option').map(function(o) { return o.value; });
	check('Cloudflare is the first choice, so a new group gets it',
		options[0] === 'https://cp.cloudflare.com/generate_204');
	check('Google is still offered',
		options.indexOf('https://www.gstatic.com/generate_204') >= 0);
	check('and so is an address of your own',
		options.indexOf('__other__') >= 0);
	check('the choices say what they are, not just a URL',
		(d.field.textContent || '').indexOf('Cloudflare') >= 0);
}

// 2. A new group saves the address the dropdown is showing. The field exists
//    to be read: a dropdown the Save button ignores looks identical on screen.
d = dialog(null);
d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
var sent = d.save();
check('a new group is saved with Cloudflare',
	sent && sent.probe_url === 'https://cp.cloudflare.com/generate_204');
check('and still sends the rest of the group',
	sent && sent.members.length === 1 && sent.strategy === 'leastPing' &&
	sent.probe_interval === '60s');

// 3. Choosing another one from the list sends that one.
d = dialog(null);
d.field.value = 'https://www.gstatic.com/generate_204';
d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
sent = d.save();
check('picking Google from the list saves Google',
	sent && sent.probe_url === 'https://www.gstatic.com/generate_204');

// 4. "Other address…" reveals the box, and what is typed there is what is sent.
d = dialog(null);
check('the box is hidden until it is needed',
	(d.custom.getAttribute('style') || '').indexOf('display:none') >= 0);
d.pickOther();
check('choosing an address of your own shows the box',
	(d.custom.getAttribute('style') || '').indexOf('display:block') >= 0);
d.custom.value = 'https://example.net/204';
d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
sent = d.save();
check('and what is typed there is what gets saved',
	sent && sent.probe_url === 'https://example.net/204');

// 5. Editing an existing group shows the address it is using, including one
//    that is not on the list — otherwise opening the dialog to change the
//    group's name silently replaces the address.
d = dialog({
	id: 'g1', name: 'avrupa', strategy: 'leastLoad', members: [ 'p1' ],
	probe_interval: '2m', probe_url: 'https://www.gstatic.com/generate_204'
});
check('editing a group shows the address it is using',
	d.field && d.field.value === 'https://www.gstatic.com/generate_204');
sent = d.save();
check('and saving without touching it keeps that address',
	sent && sent.probe_url === 'https://www.gstatic.com/generate_204');

d = dialog({
	id: 'g1', name: 'avrupa', strategy: 'leastPing', members: [ 'p1' ],
	probe_url: 'https://my.own.example/204'
});
check('an address that is not on the list is kept and shown in the box',
	d.field.value === '__other__' && d.custom.value === 'https://my.own.example/204');
check('and the box is open, so it can be seen without clicking anything',
	(d.custom.getAttribute('style') || '').indexOf('display:block') >= 0);
sent = d.save();
check('and it survives a save',
	sent && sent.probe_url === 'https://my.own.example/204');

// 6. Something that is not a URL is refused here, where the person can see the
//    field. Saved, it costs the group its health checks and reports nothing:
//    the tunnel comes up, traffic flows, and the ranking is made of failures.
[ 'cp.cloudflare.com', 'ftp://example.com/x', 'tcp://1.1.1.1:53' ].forEach(function(bad) {
	d = dialog(null);
	d.pickOther();
	d.custom.value = bad;
	d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
	var out = d.save();
	check('"' + bad + '" is refused rather than stored', out === null);
	check('and the refusal says so on screen',
		global.NOTIFICATIONS.length > 0);
});

// 7. "Other address…" with an empty box would save nothing at all, and the
//    daemon would fill in the default — so the group would use an address the
//    operator did not choose while the dialog said "Other".
d = dialog(null);
d.pickOther();
d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
check('an empty box is refused rather than turned into the default',
	d.save() === null);

// 8. The warning is not a wall: a group with no members is still the thing
//    being complained about there, and the address check must not have taken
//    that message over.
d = dialog(null);
sent = d.save();
check('a group with no members is still refused for having no members',
	sent === null && (global.NOTIFICATIONS[0].node.textContent || '')
		.indexOf('sunucu') >= 0);

// 9. Sharing the load is a strategy in the same list as the others, not a
//    number hidden somewhere. It is the core's leastLoad with every member as
//    a candidate, and the daemon does that translation — from here it has to
//    be saved under its own name or the daemon cannot tell it from "steadiest
//    server", which is the same core strategy narrowed to one.
d = dialog(null);
var strategies = null;
d.body.querySelectorAll('select').forEach(function(el) {
	el.querySelectorAll('option').forEach(function(o) {
		if (o.value === 'leastPing') strategies = el;
	});
});
check('the strategy list is there', !!strategies);
if (strategies) {
	var names = strategies.querySelectorAll('option').map(function(o) { return o.value; });
	check('sharing the load is one of the choices', names.indexOf('balance') >= 0);
	check('and the others are still there',
		names.indexOf('leastLoad') >= 0 && names.indexOf('random') >= 0 &&
		names.indexOf('roundRobin') >= 0);
	check('the choice says what it does, not just its name',
		(strategies.textContent || '').indexOf('paylaştır') >= 0);
}

d.field.value = 'https://cp.cloudflare.com/generate_204';
strategies.value = 'balance';
d.body.querySelectorAll('input[type=checkbox]')[0].checked = true;
sent = d.save();
check('and it is saved under its own name',
	sent && sent.strategy === 'balance');

// The warning that comes with it. Spreading connections means consecutive
// requests leave from different addresses, and the sites that tie a session to
// an address treat that as a hijack — which is the one thing someone turning
// this on should know before they turn it on, not after.
check('the dialog warns what sharing the load costs',
	(d.body.textContent || '').indexOf('oturum kaçırma') >= 0);

process.exit(fail);
