#!/usr/bin/env node
// The errors a connection keeps hitting while it looks fine.
//
// This panel exists because of a week spent on a tunnel that was up, green,
// passing traffic, and dropping enough of it that phones stopped getting their
// notifications. Nothing on the status page said so. The core had been saying
// so all along, hundreds of times an hour, into a log that holds two thousand
// lines and turns over in minutes — so by the time anyone looked, the evidence
// had been overwritten by the traffic that followed it.
//
// A count survives that. What this file checks is that the count is shown when
// there is one, is not shown when there is not, and says both halves of what
// makes a count mean anything: how many, and how recently.
//
//   node luci-app-xwrt/test/faults.js
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
	version: '1.0.22', auto_connect: true
};

function page(faults) {
	var s = {};
	for (var k in base) s[k] = base[k];
	if (faults) s.core_faults = faults;
	return (view.render([ s, fixtures.CONFIG || {}, {} ]).textContent || '');
}

var dialFail = 'app/proxyman/outbound: failed to process outbound traffic > ' +
	'proxy/vless/outbound: failed to find an available destination > ' +
	'transport/internet/websocket: failed to dial WebSocket > EOF';

// --- when there is something to say -----------------------------------------

var t = page([
	{ source: 'core', message: dialFail, count: 412,
	  first: '2026-09-20T18:00:00Z', last: '2026-09-20T21:04:57Z' },
	{ source: 'core', message: 'proxy/socks: failed to read request > EOF', count: 3,
	  first: '2026-09-20T20:10:00Z', last: '2026-09-20T20:12:00Z' }
]);

check('the panel appears', t.indexOf('Tekrarlayan hatalar') >= 0);
check('with the count, which is the whole point', t.indexOf('412') >= 0);

// Not translated, and that is deliberate: this is the core's own sentence and
// the string somebody will paste into a search engine. A translated copy would
// be a different string from the one that can be looked up.
check('and the error in the core\'s own words',
	t.indexOf('transport/internet/websocket: failed to dial WebSocket') >= 0);

// A count with no window is a number nobody can use: 412 over three hours and
// 412 over three minutes are different diagnoses.
check('and when it last happened', t.indexOf('son görülme') >= 0);

check('the rarer fault is shown too, under the worst one', t.indexOf('proxy/socks') >= 0);
check('and the log is one click away', t.indexOf('Günlüğü aç') >= 0);

// --- and when there is not ---------------------------------------------------

// A heading that is always there with nothing under it teaches people that this
// part of the page is dead, and then they stop reading it on the day it fills.
var quiet = page(null);
check('a clean connection shows no panel at all',
	quiet.indexOf('Tekrarlayan hatalar') < 0);
check('and an empty list is the same as none',
	page([]).indexOf('Tekrarlayan hatalar') < 0);

console.log('\n' + (fail
	? 'a connection can still be failing silently'
	: 'what keeps going wrong is on the page, with how often and how recently'));
process.exit(fail);
