#!/usr/bin/env node
// The switch that stops a profile authenticating its server.
//
// It exists because a server reached by address alone has no name, so no
// authority can have issued a certificate for it, so the pin is the only thing
// standing anywhere — and a pin on a server that reissues its certificate
// breaks on that server's schedule. Where nothing can be verified, the operator
// may reasonably choose uptime.
//
// Where something CAN be verified, offering it is a bug: it turns off a check
// that works. So the rule this page applies has to be the same rule the daemon
// applies, or the interface offers a setting that is then refused on save.
//
//   node luci-app-xwrt/test/pinauto.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

function editor(profile) {
	global.MODALS = [];
	var view = stub.load('view/xwrt/profiles.js');
	view.reload = function() { return Promise.resolve(); };
	view.handleEditProfile(profile);
	var modal = global.MODALS[global.MODALS.length - 1];
	return {
		text: modal.title + ' ' + modal.body.textContent,
		boxes: modal.body.querySelectorAll('input[type=checkbox]')
	};
}

function base(extra) {
	var p = {
		id: 'p1', name: 'divine', proto: 'vless', address: '198.51.100.20',
		port: 443, network: 'ws', security: 'tls',
		uuid: 'b831381d-6324-4d53-ad4f-8cda48b30811',
		pinned_cert: 'aaaa000000000000000000000000000000000000000000000000000000000000'
	};
	Object.keys(extra || {}).forEach(function(k) { p[k] = extra[k]; });
	return p;
}

// --- where it is offered ----------------------------------------------------

var bare = editor(base());
check('a bare address is offered the switch',
	bare.text.indexOf('Sertifika değişimi') >= 0 &&
	bare.text.indexOf('sormadan kabul et') >= 0);

// The label has to say what it does, not what it is for. "Accept changes
// automatically" reads as convenience; this is the setting that stops the
// profile authenticating its server, and somebody deciding deserves to be told.
check('and told plainly what it gives up',
	bare.text.indexOf('doğrulamayı bırakır') >= 0);
check('and that the change is still recorded',
	bare.text.indexOf('günlüğe yazılır') >= 0);

// --- where it is not --------------------------------------------------------

var named = editor(base({ sni: 'vpn.example.com' }));
check('a profile sending a real name is not offered it',
	named.text.indexOf('sormadan kabul et') < 0);

// A borrowed name is a real hostname belonging to somebody else, which is how
// a great many of these servers are reached. It reads as verifiable until the
// certificate has been looked at — so before the look it is withheld, and after
// a look that found nothing vouching for it, it is offered.
var borrowedUnseen = editor(base({ sni: 'cdn.whatsapp.net' }));
check('a borrowed name is withheld until the certificate has been looked at',
	borrowedUnseen.text.indexOf('sormadan kabul et') < 0);

var borrowedSeen = editor(base({ sni: 'cdn.whatsapp.net', pin_unverifiable: true }));
check('and offered once the certificate turned out not to verify for it',
	borrowedSeen.text.indexOf('sormadan kabul et') >= 0);

// And it says why, with the better answer, rather than silently omitting a
// control the operator was looking for.
check('and is told why, and what to do instead',
	named.text.indexOf('vpn.example.com') >= 0 &&
	named.text.indexOf('sabitlemeyi kaldırmaktır') >= 0);

var domain = editor(base({ address: 'vpn.example.com' }));
check('a profile dialled by name is not offered it',
	domain.text.indexOf('sormadan kabul et') < 0);

var reality = editor(base({ security: 'reality', pbk: 'x', public_key: 'x' }));
check('a profile with no certificate at all is not offered it',
	reality.text.indexOf('sormadan kabul et') < 0);

// --- and the box reflects and writes the value ------------------------------

var on = editor(base({ pin_auto: true }));
var ticked = null;
on.boxes.forEach(function(b) { if (b.checked && ticked === null) ticked = b; });
check('a profile that has it on shows it on', ticked !== null);

console.log('\n' + (fail
	? 'the switch is offered where a certificate could have been checked'
	: 'the switch is offered only where nothing could be verified, and says what it costs'));
process.exit(fail);
