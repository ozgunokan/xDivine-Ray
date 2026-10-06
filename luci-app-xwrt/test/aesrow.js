#!/usr/bin/env node
// What the Status page says about hardware AES, and why it says anything.
//
// It is one fact about the processor and it decides which TLS fingerprint is
// fast on this device. A browser fingerprint offers AES-GCM first, because
// browsers run on machines where AES is free; a router with no AES instructions
// then does AES in software for every byte, while ChaCha20 — designed for that
// processor — sits unused at the bottom of the ClientHello. The undisguised
// fingerprint hands the ordering back to Go and roughly doubles the throughput.
//
// Nobody knows this about their own router. So the row does not just print a
// word: when the answer is no, it says what to do about it. And when the answer
// is yes it says nothing further, because then there is nothing to do and a
// suggestion that changes nothing is how a page teaches people to stop reading
// it.
//
//   node luci-app-xwrt/test/aesrow.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fixtures = require('./fixtures.js');
var view = stub.load('view/xwrt/status.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

var status = {
	connected: true, core_running: true, mode: 'mixed',
	profile_id: 'p1', profile_name: 'divine', target_kind: 'profile',
	uptime_seconds: 60, stats: {}, version: '1.0.28', auto_connect: true
};

function page(accel) {
	var env = { lan_devices: [ 'br-lan' ], wan_device: 'wwan0_1',
		firewall: 'nftables', mem_total_mb: 512, storage_total_mb: 256,
		storage_free_mb: 128 };
	if (accel !== undefined) env.crypto_accel = accel;
	return (view.render([ status, fixtures.CONFIG || {}, env ]).textContent || '');
}

console.log('-- a processor with AES');

var withAES = page('yes');
check('the row is there and says so', withAES.indexOf('Donanımsal AES') >= 0 &&
	withAES.indexOf('var') >= 0);
check('and nothing is suggested, because nothing would change',
	withAES.indexOf('helloXdivine') < 0);

console.log('\n-- a processor without');

var without = page('no');
check('the row says so', without.indexOf('Donanımsal AES') >= 0 &&
	without.indexOf('yok') >= 0);
check('and names the fingerprint that helps here',
	without.indexOf('helloXdivine') >= 0);
check('and says what it is worth', without.indexOf('iki katına') >= 0);
check('and where to set it', without.indexOf('Sunucular') >= 0);

console.log('\n-- an older daemon, or a probe that could not answer');

// An upgraded interface against a daemon that does not report this yet, or a
// device where the probe failed. "We could not tell" and "it has none" lead
// somewhere different, and recommending a change on the strength of a missing
// field is how a page gives advice nobody asked for.
[ undefined, '' ].forEach(function(v) {
	var quiet = page(v);
	check('nothing is claimed when the answer is ' + JSON.stringify(v),
		quiet.indexOf('helloXdivine') < 0);
	check('and the row is still there rather than a hole in the table (' +
		JSON.stringify(v) + ')', quiet.indexOf('Donanımsal AES') >= 0);
});

console.log('\n' + (fail ? 'the hardware AES row is wrong' :
	'the page states the one fact that decides the fingerprint, and acts on it'));
process.exit(fail);
