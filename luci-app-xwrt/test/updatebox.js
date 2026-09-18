#!/usr/bin/env node
// The update box on the About page.
//
// It exists because of one screenshot. A router whose repository had no
// releases yet showed a red banner across the top of the page, in English,
// saying the repository might be wrong — while the box underneath still said
// no check had been made. Two mistakes at once, and both of them mine.
//
// The first: every RPC answer goes through one unwrapper that treats an
// "error" key as a call that failed, and the update check reported "I could
// not reach GitHub" in a field with that name. A fact about the last check
// became an exception, which became a banner.
//
// The second: that text was English. Every other failure in this interface is
// a code plus values, translated here; the updater was the one place I wrote
// prose in the daemon and sent it to the page.
//
//   node luci-app-xwrt/test/updatebox.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

// What the daemon sends when the repository has no releases.
var answer = {
	current: '1.0.0',
	check_error: 'ozgunokan/xDivine-Ray has no releases, or is not the right repository; check the update source in Settings',
	error_code: 'update.no_releases',
	error_args: [ 'ozgunokan/xDivine-Ray' ],
	checked_at: '2026-09-18T01:48:00Z'
};

// 1. It must not be treated as a failed call.
var threw = false;
try { global.xwrt.checked(answer); } catch (e) { threw = true; }
check('a check that could not reach GitHub is not an exception', !threw);

// 2. The page must render it in Turkish.
var view = stub.load('view/xwrt/about.js');
var tree = view.render([ { version: '1.0.0' }, answer ]);
var text = tree.textContent || '';
check('the reason is on the page in Turkish',
	text.indexOf('hiç sürüm yok') >= 0);
check('and not in English',
	text.indexOf('has no releases') < 0);
check('the box does not claim no check was made',
	text.indexOf('Henüz kontrol yapılmadı') < 0);

process.exit(fail);
