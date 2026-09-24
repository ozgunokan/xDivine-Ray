#!/usr/bin/env node
// The connection-table warning on the Connections page.
//
// It exists because of a failure with no other visible sign: a device that is
// fine in TUN mode and, in mixed mode, plays a video for twenty minutes and
// then stalls for a few seconds at a time, over and over, recovering by
// itself. That is a fixed-size kernel table filling up — new connections
// dropped until old entries age out — and every screen in this app said the
// tunnel was up and healthy throughout.
//
//   node luci-app-xwrt/test/conntrack.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

var view = stub.load('view/xwrt/traffic.js');
var TRAFFIC = { samples: [], uplink: 1, downlink: 2, peak_up: 0, peak_down: 0 };

function page(capacity) {
	return (view.render([ TRAFFIC, {
		clients: [], flows: [], available: true, accounting: true,
		total_flows: 312, lan_flows: 98, capacity: capacity
	} ]).textContent || '');
}

// 1. A table with room says the number and nothing more. A warning on every
//    healthy device is a warning nobody reads on the one device that needs it.
var t = page({ count: 1200, max: 16384, percent: 7, known: true });
check('a table with room reports the number',
	t.indexOf('1200') >= 0 && t.indexOf('16384') >= 0);
check('and does not warn about it',
	t.indexOf('yeni bağlantıları düşürür') < 0);

// 2. A filling table explains what it will look like, because "86%" on its
//    own means nothing to someone whose video is stuttering.
t = page({ count: 14200, max: 16384, percent: 86, known: true });
check('a filling table warns', t.indexOf('yeni bağlantıları düşürür') >= 0);
check('and describes the symptom in the words the user would use',
	t.indexOf('donup sonra devam eden bir video') >= 0);
check('and names the modes that differ',
	t.indexOf('TUN modu neredeyse hiç kaplamaz') >= 0);

// 3. The command, with the number already doubled: nobody should have to do
//    arithmetic while their network is misbehaving.
check('it gives the exact command',
	t.indexOf('net.netfilter.nf_conntrack_max=32768') >= 0);

// 4. A kernel that does not publish the numbers is not a fault, and "0 of 0"
//    would be worse than silence.
t = page({ known: false });
check('an unknown capacity is left out entirely',
	t.indexOf('Çekirdek bağlantı tablosu') < 0);
t = page(undefined);
check('and a missing capacity does not break the page',
	t.indexOf('Bu filtrelere uyan kayıt yok') >= 0 || t.length > 0);

process.exit(fail);
