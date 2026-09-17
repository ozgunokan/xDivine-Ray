#!/usr/bin/env node
// What the Status page looks like three seconds after it loads.
//
// The page is built once and then refreshed on a timer, and those are two
// different pieces of code writing the same cells: `row(...)` when the page is
// built, `dom.content(document.getElementById(...), ...)` every three seconds
// after that. Every check until now looked only at the first one.
//
// They drifted, and the symptom was exactly what that implies: the capture mode
// appeared correctly for an instant after a reload and then changed back, three
// seconds later, to the raw identifier the daemon stores. Someone watching the
// page sees a value that fixes itself and then breaks itself, which reads like
// a caching problem and is not one.
//
//   node luci-app-xwrt/test/poll.js

'use strict';

var stub = require('./lucistub.js');
var fixtures = require('./fixtures.js');

var failures = [];
function fail(msg) { failures.push(msg); }
function ok(msg) { console.log('ok   ' + msg); }

function cell(id) {
	var n = global.ELEMENTS[id];
	return n ? (n.textContent || '').trim() : null;
}

function main() {
	stub.stubLuCI();
	var i18n = stub.load('xwrt/i18n.js');
	global._ = i18n.translate;
	global.i18n = i18n;
	i18n.use('tr');
	var xwrt = stub.load('xwrt.js');
	global.xwrt = xwrt;

	var view = stub.load('view/xwrt/status.js');
	view.render(fixtures.VIEWS.filter(function(v) {
		return v.file === 'view/xwrt/status.js';
	})[0].data);

	if (!global.POLLS.length)
		return fail('the Status page registers no refresh at all');
	ok('the Status page refreshes itself (' + global.POLLS[0].every + 's)');

	// What the page was built with, so the comparison is against something.
	var built = {
		mode: cell('xwrt-mode'),
		uptime: cell('xwrt-uptime')
	};
	if (built.mode === null)
		return fail('there is no mode cell on the page to refresh');

	// And now the refresh, with a different status than the page was built
	// from — a different mode among other things, because a value that only
	// ever refreshes to what it already said proves nothing.
	var later = {
		connected: true, core_running: true, tun_running: true,
		mode: 'tproxy', target_kind: 'profile',
		profile_id: 'p1', profile_name: 'Sunucu',
		uptime_seconds: 3661, auto_connect: true,
		stats: { uplink: 2048, downlink: 4096, uplink_rate: 128, downlink_rate: 256 },
		version: '9.9.9', core_version: '1.2.3'
	};
	xwrt.status = function() { return Promise.resolve(later); };

	return global.POLLS[0].fn().then(function() {
		var mode = cell('xwrt-mode');
		if (mode === 'tproxy')
			fail('after the refresh the capture mode is the stored identifier ' +
				'again ("tproxy"): the page writes it one way when it is built ' +
				'and another way three seconds later');
		else if (mode !== 'TPROXY')
			fail('after the refresh the mode cell reads "' + mode + '"');
		else
			ok('the mode survives the refresh (' + built.mode + ' → ' + mode + ')');

		// The cells around it, because the same drift can happen to any of
		// them and nothing here was watching.
		var checks = [
			[ 'xwrt-uptime', '1h 1m 1s', 'the uptime' ],
			[ 'xwrt-up', '2.0 KiB (128 B/s)', 'the upload counter' ],
			[ 'xwrt-down', '4.0 KiB (256 B/s)', 'the download counter' ]
		];
		checks.forEach(function(c) {
			var got = cell(c[0]);
			if (got !== c[1])
				fail(c[2] + ' refreshed to "' + got + '", expected "' + c[1] + '"');
		});
		if (!failures.length)
			ok('uptime and the counters refresh the way the page builds them');

		// The two cells that are not plain text: they must still be filled in
		// rather than emptied by a refresh.
		[ [ 'xwrt-state', 'the connection badge' ],
		  [ 'xwrt-server', 'the server name' ],
		  [ 'xwrt-autoconnect', 'the connect-on-startup row' ] ].forEach(function(c) {
			if (!cell(c[0]))
				fail(c[1] + ' is empty after a refresh');
		});
		ok('the badge, the server and the startup row are still filled in');

		if (failures.length) {
			failures.forEach(function(f) { console.error('FAIL ' + f); });
			process.exit(1);
		}
		console.log('\nthe Status page still says the same things after it refreshes');
	});
}

Promise.resolve().then(main).catch(function(e) {
	console.error(e);
	process.exit(1);
});
