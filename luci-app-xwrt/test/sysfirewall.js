#!/usr/bin/env node
// The router with no NAT.
//
// A line was swapped and every client lost the internet. The router itself was
// fine: it pinged, it resolved names, `ip route get` returned a correct answer
// for a LAN source address, and its configuration had masquerading switched on
// for a wan zone that included the new interface. With the tunnel on,
// everything worked — so the tunnel looked like the culprit, and an hour went
// into the one part of the system that was healthy.
//
// The device's own firewall was not loaded at all. One file under
// /etc/nftables.d had `ip ttl set` with no value after it; fw4 refuses a
// ruleset it cannot fully parse, so from the first reload after that file
// appeared the kernel had no masquerade. Clients went out with a 192.168.1.x
// source address that nothing would answer. The router needed no masquerade for
// its own traffic and neither did the tunnel, which is exactly why both looked
// healthy and only the clients suffered.
//
// So the question nobody asked is now on the page, with the firewall's own
// words underneath it.
//
//   node luci-app-xwrt/test/sysfirewall.js
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

var base = {
	connected: true, core_running: true, mode: 'mixed',
	profile_id: 'p1', profile_name: 'divine', target_kind: 'profile',
	uptime_seconds: 3600, stats: { uplink: 1, downlink: 2 },
	version: '1.0.26', auto_connect: true
};

// The real thing, from the router it happened on.
var REASON = 'the file it cannot parse is /etc/nftables.d/99-reset-ttl-from-br-lan.nft' +
	' — move it aside or fix it, then run: /etc/init.d/firewall restart\n\n' +
	'/etc/nftables.d/99-reset-ttl-from-br-lan.nft:3:34-40: Error: syntax error, unexpected comment';

function page(extra) {
	var s = {};
	for (var k in base) s[k] = base[k];
	for (var j in (extra || {})) s[j] = extra[j];
	return view.render([ s, fixtures.CONFIG || {}, {} ]);
}

function text(extra) { return page(extra).textContent || ''; }

console.log('-- a healthy device');

var quiet = text({});
check('nothing is said about a firewall that is loaded',
	quiet.indexOf('güvenlik duvarı yüklü değil') < 0);

console.log('\n-- the device with no NAT');

var warned = text({ system_firewall_down: true, system_firewall_reason: REASON });

check('the page says the firewall is not loaded',
	warned.indexOf('güvenlik duvarı yüklü değil') >= 0);
check('and says what that costs — no NAT',
	warned.indexOf('NAT yok') >= 0);
// The sentence that would have saved the hour: the router being fine is the
// symptom, not evidence against the diagnosis.
check('and warns that this stays hidden until the tunnel is switched off',
	warned.indexOf('tünel kapatılınca') >= 0);
check('and names the file that has to be fixed',
	warned.indexOf('99-reset-ttl-from-br-lan.nft') >= 0);
check('and gives the command that proves it',
	warned.indexOf('fw4 check') >= 0);

console.log('\n-- how it is shown');

var box = page({ system_firewall_down: true, system_firewall_reason: REASON });

function findAll(node, test, out) {
	out = out || [];
	if (!node || typeof node !== 'object') return out;
	if (test(node)) out.push(node);
	var kids = node.childNodes || node.children || [];
	for (var i = 0; i < kids.length; i++) findAll(kids[i], test, out);
	return out;
}

var alerts = findAll(box, function(n) {
	return n.getAttribute && (n.getAttribute('class') || '').indexOf('alert-message') >= 0;
});
check('it is an alert rather than a row somewhere in a table', alerts.length >= 1);

// Monospaced, because this is the text somebody pastes into a search box or
// sends to whoever is helping them, and the file path is the whole value of it.
var pre = findAll(box, function(n) {
	return (n.tagName || '').toLowerCase() === 'pre';
});
check('the firewall\'s own words are in a block that keeps its shape',
	pre.length >= 1);

// Not dismissible, unlike a failed connect. That is an event that has passed;
// this is a condition that is true right now, and a dismissed banner would hide
// a router that still has no NAT.
//
// Checked against a page that also has a failed connect on it, so that a page
// with no dismiss button anywhere cannot pass this by accident.
var both = page({
	system_firewall_down: true, system_firewall_reason: REASON,
	last_error: { message: 'the core would not start', step: 'core', time: '2026-10-01T18:03:00Z' }
});

function buttonsIn(node) {
	return findAll(node, function(n) {
		return (n.tagName || '').toLowerCase() === 'button' &&
			(n.textContent || '').indexOf('Kapat') >= 0;
	});
}

check('the failed connect still has its dismiss button',
	buttonsIn(both).length === 1);

var ours = findAll(both, function(n) {
	return n.getAttribute &&
		(n.getAttribute('class') || '').indexOf('alert-message') >= 0 &&
		(n.textContent || '').indexOf('güvenlik duvarı yüklü değil') >= 0;
});
check('the no-NAT warning is an alert of its own', ours.length === 1);
check('and it carries no way to dismiss it',
	ours.length === 1 && buttonsIn(ours[0]).length === 0);

console.log('\n-- a firewall that is merely stopped');

// No reason: fw4 parses fine, somebody stopped the service. Still no NAT, so
// still worth saying — but nothing is invented to explain it.
var bare = text({ system_firewall_down: true });
check('it is still reported with no reason to give',
	bare.indexOf('güvenlik duvarı yüklü değil') >= 0);
check('and no file is named that nobody mentioned',
	bare.indexOf('/etc/nftables.d/') < 0);

console.log('\n-- and when it is fixed');

// The banner is a condition, not an event: it has to go away on the poll after
// the firewall comes back, without anybody reloading the page.
var fixed = text({ system_firewall_down: false, system_firewall_reason: REASON });
check('a reason left over from before is not shown on its own',
	fixed.indexOf('99-reset-ttl-from-br-lan.nft') < 0);

console.log('\n' + (fail ? 'the no-NAT warning is wrong' :
	'a router with no NAT says so, in words, with the file that caused it'));
process.exit(fail);
