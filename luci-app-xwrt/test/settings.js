#!/usr/bin/env node
// The Settings page, built and inspected.
//
// LuCI builds this form itself, so there is no tree to walk until LuCI is
// there to build it — and that exemption has cost three times: the page
// shipped for several releases with no stylesheet at all, and then its help
// text ran off the right of a phone screen, both found by someone looking at a
// real router rather than by anything here.
//
// So the form is recorded rather than rendered (formstub.js), and this file
// asks the questions that are about the record: does the page still build
// without throwing, does the device picker really offer the devices this box
// has, is automatic really the first choice and the default. Whether the
// result fits on a screen is a different question, and mobile.js now answers
// it — from the same record, in a browser, at 390 pixels.
//
//   node luci-app-xwrt/test/settings.js

'use strict';

var stub = require('./lucistub.js');

var failures = [];
function fail(msg) { failures.push(msg); }
function ok(msg) { console.log('ok   ' + msg); }

// The form is the shared imitation in formstub.js, which mobile.js also uses
// to measure this page in a browser. One imitation rather than two: the
// version that lived here drifted out of step with the one over there, and a
// stub that is wrong in only one harness is worse than no stub at all.
var formstub = require('./formstub.js');

var record = null;
function options() { return record.byName; }

function main() {
	stub.stubLuCI();
	var i18n = stub.load('xwrt/i18n.js');
	global._ = i18n.translate;
	global.i18n = i18n;
	global.xwrt = stub.load('xwrt.js');

	record = formstub.install({ E: stub.El });

	// What the daemon says it is using right now.
	var ENV = { lan_devices: [ 'br-lan' ], wan_device: 'wwan0_1' };

	var view = stub.load('view/xwrt/settings.js');

	return Promise.resolve(view.load()).then(function() {
		// First the case that must not break the page: no device list at all.
		// Someone whose daemon will not start, or whose LuCI cannot enumerate
		// the interfaces, is exactly the person who came here to change a
		// setting — and a page that throws instead of rendering leaves them
		// with a shell as their only option.
		return view.render([ null, undefined, {} ]);
	}).then(function(node) {
		if (!node)
			return fail('with no device list the page did not render at all');
		var o = options().lan_device;
		if (!o || !o.choices.length || o.choices[0][0] !== '')
			fail('with no device list the page does not even offer automatic');
		else
			ok('with no device list the page still builds and offers automatic');

		var devs = record.devices;
		record = formstub.install({ E: stub.El });
		return view.render([ null, devs, ENV ]);
	}).then(function(node) {
		if (!node)
			return fail('render() produced nothing');
		ok('the Settings page builds');

		[ [ 'lan_device', 'br-lan' ], [ 'wan_device', 'wwan0_1' ] ].forEach(function(pair) {
			var name = pair[0], detected = pair[1];
			var o = options()[name];
			if (!o) return fail(name + ' is not on the page any more');

			var keys = o.choices.map(function(c) { return c[0]; });
			if (keys[0] !== '')
				return fail(name + ': automatic is not the first choice (' +
					JSON.stringify(keys) + ')');
			ok(name + ': automatic first, then ' + (keys.length - 1) + ' devices');

			// The devices this box actually has, and not the two that are
			// never the answer.
			[ 'br-lan', 'eth0', 'wwan0_1' ].forEach(function(want) {
				if (keys.indexOf(want) < 0)
					fail(name + ' does not offer ' + want);
			});
			[ 'lo', 'xwrt0' ].forEach(function(no) {
				if (keys.indexOf(no) >= 0)
					fail(name + ' offers ' + no + ', which is never the answer');
			});

			// The one in use is marked, so nobody has to guess which of four
			// similar names is the live one.
			var mark = o.choices.filter(function(c) { return c[0] === detected; })[0];
			if (!mark || String(mark[1]) === detected)
				fail(name + ': the device in use (' + detected + ') is not marked');
			else
				ok(name + ': the device in use is marked — "' + mark[1] + '"');
		});

		// Two defaults worth pinning: the empty value means automatic, and
		// connecting on startup is on unless someone turns it off.
		if (options().lan_device && options().lan_device.default)
			fail('lan_device has a default other than automatic');
		if (!options().auto_connect || options().auto_connect.default !== '1')
			fail('auto_connect no longer defaults to on');
		else
			ok('a fresh install connects on startup');
	}).then(function() {
		if (failures.length) {
			failures.forEach(function(f) { console.error('FAIL ' + f); });
			process.exit(1);
		}
		console.log('\nthe Settings page builds, and the device pickers offer ' +
			'what this box has');
	});
}

main().catch(function(e) {
	console.error(e);
	process.exit(1);
});
