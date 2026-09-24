#!/usr/bin/env node
// Which server in a group the Status page says you are going through.
//
// It used to say "Avrupa (3 üyeli grup, en hızlı sunucu)" and nothing else,
// which on a router pointed at three servers answers none of the question
// anyone has. The balancer never announces its choice, so the answer is read
// off the members' counters — and because that is a derived answer, it has to
// be wrong in the safe direction: name nobody rather than name the wrong one.
//
//   node luci-app-xwrt/test/groupmember.js
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

function page(status) {
	return (view.render([ status, fixtures.CONFIG || {}, {} ]).textContent || '');
}

var base = {
	connected: true, core_running: true, mode: 'mixed',
	profile_id: 'g1', profile_name: 'Avrupa', target_kind: 'group',
	group_strategy: 'leastPing', group_members: 3,
	uptime_seconds: 60, stats: { uplink: 1, downlink: 2 },
	version: '1.0.5', auto_connect: true
};

function withUsage(extra) {
	var s = {};
	for (var k in base) s[k] = base[k];
	for (var k2 in extra) s[k2] = extra[k2];
	return s;
}

var usage = [
	{ profile_id: 'p1', name: 'amsterdam-1', uplink: 91000, downlink: 410000, live: false },
	{ profile_id: 'p2', name: 'frankfurt-2', uplink: 2200000, downlink: 18000000, live: true },
	{ profile_id: 'p3', name: 'paris-3', uplink: 0, downlink: 0, live: false }
];

// 1. The live member is named where the group's name is, not buried in a table.
var t = page(withUsage({ group_live: [ 'frankfurt-2' ], group_usage: usage }));
// The name has to be on the line that labels it, not merely somewhere on the
// page: every member's name appears in the table below, so "the page contains
// frankfurt-2" is true even when the live line has been deleted. Ask for the
// label and the name together.
check('the member carrying traffic is named next to the group',
	t.indexOf('şu an geçilen: frankfurt-2') >= 0);
check('and the group still says how it chooses',
	t.indexOf('3 üyeli grup') >= 0);

// 2. Every member is listed with what it has carried, so "why is that one
//    never used" is answerable without reading the core's log.
[ 'amsterdam-1', 'paris-3', 'Bu gruptaki sunucular' ].forEach(function(want) {
	check('the member table shows ' + want, t.indexOf(want) >= 0);
});

// 3. Two members at once. Under random or round-robin that is the truth, and
//    reporting a single "active server" would be a tidier answer than the one
//    the counters support.
t = page(withUsage({
	group_strategy: 'random',
	group_live: [ 'amsterdam-1', 'paris-3' ],
	group_usage: usage
}));
// Both of them, on that same line, joined — not just the first. Naming one of
// two is worse than naming neither: it reads as certainty.
check('two live members are both named, together',
	t.indexOf('şu an geçilen: amsterdam-1, paris-3') >= 0);

// 4. Connected, nothing moved yet. The tunnel is up and idle; naming a server
//    here would be an invention, and saying nothing at all reads like a bug.
t = page(withUsage({ group_live: [], group_usage: usage }));
check('an idle tunnel says so rather than naming a member',
	t.indexOf('henüz trafik yok') >= 0);

// 5. A single server is not a group and gets none of this.
t = page({
	connected: true, core_running: true, mode: 'mixed',
	profile_id: 'p1', profile_name: 'divine-okan', target_kind: 'profile',
	uptime_seconds: 60, stats: { uplink: 1, downlink: 2 }, auto_connect: true
});
check('a single server gets no member table',
	t.indexOf('Bu gruptaki sunucular') < 0 && t.indexOf('henüz trafik yok') < 0);

process.exit(fail);
