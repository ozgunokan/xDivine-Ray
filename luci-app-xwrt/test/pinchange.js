#!/usr/bin/env node
// Re-pinning a certificate that changed under a server.
//
// The situation is the whole reason this is guarded. A pin stops matching,
// every connection is refused, and the obvious move is to read the certificate
// again — which, if what changed is something sitting on the path, pins that
// instead, and the tunnel comes back up through it looking perfectly healthy.
// The first pin is taken from a server nobody has reason to doubt; the second
// is taken at the one moment there is a reason.
//
// So the daemon refuses and hands back what it found, and the page has to put
// that in front of the operator rather than flattening it into "could not read
// the certificate". This exercises it the way a person meets it: press the
// button, read the dialog, decide.
//
//   node luci-app-xwrt/test/pinchange.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

var CONFLICT = {
	error: 'this profile already pins a different certificate',
	needs_replace: true,
	current_pin: 'aaaa000000000000000000000000000000000000000000000000000000000000',
	found: {
		pin: 'bbbb111111111111111111111111111111111111111111111111111111111111',
		endpoint: '87.121.104.212:443',
		sni: '87.121.104.212',
		trusted: false,
		chain: [{
			subject: 'CN=87.121.104.212',
			issuer: 'CN=Some Middlebox CA',
			not_before: '2026-09-27T22:10:00Z',
			not_after: '2027-09-27T22:10:00Z',
			sha256: 'bbbb111111111111111111111111111111111111111111111111111111111111'
		}]
	},
	nature: {
		self_signed: false,
		note: 'no public authority vouches for this certificate and it was not ' +
			'issued by the server itself, so look at the issuer above before ' +
			'trusting it: this is the case a pin exists to catch'
	}
};

// press opens the page, presses Pin on a server, and reports what happened:
// the calls that were made and the dialog that came up.
function press(answers) {
	global.MODALS = [];
	global.NOTIFICATIONS = [];
	var calls = [];
	var view = stub.load('view/xwrt/profiles.js');
	view.reload = function() { return Promise.resolve(); };
	global.xwrt.checked = function(res) {
		if (res && res.error) throw new Error(res.error);
	};
	global.xwrt.fetchCert = function(id, replace) {
		calls.push({ id: id, replace: replace });
		return Promise.resolve(answers.shift());
	};
	var done = view.handlePinCert('p1', 'divine-okan');
	return { calls: calls, done: done, view: view };
}

function lastModal() { return global.MODALS[global.MODALS.length - 1]; }
function modalText() {
	var m = lastModal();
	return m ? (m.title + ' ' + m.body.textContent) : '';
}

// --- the refusal is shown, not swallowed ------------------------------------

var first = press([CONFLICT]);
first.done.then(function() {
	check('the first press does not ask to replace anything',
		first.calls.length === 1 && first.calls[0].replace === false);

	var text = modalText();
	check('a dialog comes up rather than an error notification',
		global.NOTIFICATIONS.length === 0 && lastModal() !== undefined);
	check('it says nothing has been changed',
		text.indexOf('Hiçbir şey değiştirilmedi') >= 0);

	// The two facts that decide the answer. Without the issuer this dialog is
	// a yes/no about a hex string, which is not a question anybody can answer.
	check('it shows who issued the certificate on the wire',
		text.indexOf('Some Middlebox CA') >= 0);
	// Both ends of the validity window, rendered in the reader's locale. The
	// pair is what separates a routine renewal from a server that reissues
	// every few hours, and the expiry alone says neither.
	check('it shows both ends of the validity window',
		text.indexOf(global.xwrt.localDateTime('2026-09-27T22:10:00Z')) >= 0 &&
		text.indexOf(global.xwrt.localDateTime('2027-09-27T22:10:00Z')) >= 0);
	check('it shows the pin that was in place and the one being offered',
		text.indexOf('aaaa0000') >= 0 && text.indexOf('bbbb1111') >= 0);
	check('it passes on the daemon\'s reading of the certificate',
		text.indexOf('look at the issuer') >= 0);

	// The dialog must not be in English on a Turkish interface.
	check('the dialog is in Turkish',
		text.indexOf('Sertifika değişti') >= 0 &&
		text.indexOf('The certificate has changed') < 0);

	// --- and answering it is a second, separate call ------------------------

	var buttons = [];
	lastModal().body.querySelectorAll('button').forEach(function(b) {
		buttons.push(b);
	});
	var trust = null, cancel = null;
	buttons.forEach(function(b) {
		if ((b.textContent || '').indexOf('sabitle') >= 0) trust = b;
		if ((b.textContent || '').indexOf('Vazgeç') >= 0 ||
			(b.textContent || '').indexOf('İptal') >= 0) cancel = b;
	});
	check('there is a way to decline', cancel !== null);
	check('there is a way to trust the new certificate', trust !== null);

	if (!trust) { process.exit(1); }

	// Pressing it repeats the fetch, this time saying replace.
	var second = 0;
	global.xwrt.fetchCert = function(id, replace) {
		second++;
		check('trusting it asks the daemon to replace the pin', replace === true);
		return Promise.resolve({ pin: CONFLICT.found.pin, endpoint: '87.121.104.212:443',
			sni: '87.121.104.212', trusted: false, chain: CONFLICT.found.chain });
	};
	trust.click();

	setTimeout(function() {
		check('nothing is replaced until the button is pressed', second === 1);
		console.log('\n' + (fail ? 'a changed certificate can be trusted without being seen'
			: 'a changed certificate is shown before it is trusted, and only then'));
		process.exit(fail);
	}, 20);
}).catch(function(e) {
	console.log('FAIL the refusal was thrown rather than shown: ' + e.message);
	process.exit(1);
});
