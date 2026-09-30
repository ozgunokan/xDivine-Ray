#!/usr/bin/env node
// The window an update runs in.
//
// An update used to be a button followed by silence: no way to tell a download
// that was progressing from one that had stalled, or either from an install
// that had quietly failed. The window shows each step. The last of them cannot
// be reported by the daemon — the installer stops it to replace it — so the
// window has to read that step from silence, and this is where it is easiest to
// get wrong: silence that means "restarting" and silence that means "gone" look
// the same until you wait.
//
// Everything is walked here with a clock the test controls, so four whole
// updates take no time at all.
//
//   node luci-app-xwrt/test/updatewindow.js
'use strict';
var fs = require('fs');
var path = require('path');
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

// --- a controllable world ---------------------------------------------------

function world(answers) {
	var w = { queue: [], time: 0, fetched: [], reloaded: 0, logAsked: 0 };
	var view = stub.load('view/xwrt/about.js');
	view._clock.later = function(fn, ms) { w.queue.push({ fn: fn, at: w.time + (ms || 0) }); };
	view._clock.now = function() { return w.time; };

	var i = 0;
	global.xwrt.update = function() {
		var a = answers[Math.min(i++, answers.length - 1)];
		// Two shapes of "not there": the call failing outright, and the RPC
		// layer answering on the daemon's behalf with an error of its own.
		if (a === 'down') return Promise.reject(new Error('no daemon'));
		if (a === 'down-answered') return Promise.resolve({ error: 'Failed to connect to the daemon' });
		return Promise.resolve(a);
	};
	global.xwrt.updateLog = function() {
		w.logAsked++;
		return Promise.resolve({ log: '==> verifying\n    ERROR: the new daemon did not answer\n==> rolled back' });
	};
	global.fetch = function(url, opts) {
		w.fetched.push({ url: url, cache: opts && opts.cache });
		return Promise.resolve({});
	};
	global.location = { reload: function() { w.reloaded++; } };
	global.L.resource = function(f) { return '/luci-static/resources/' + f; };
	global.L.env = { resource_version: 'abc123' };

	global.MODALS = [];
	w.view = view;
	w.text = function() {
		var m = global.MODALS[global.MODALS.length - 1];
		return m ? (m.title + ' ' + m.body.textContent) : '';
	};
	w.bar = function() {
		var m = global.MODALS[global.MODALS.length - 1];
		return m ? m.body.querySelector('.xwrt-update-bar') : null;
	};
	// One step of the world: run what is due, then let the promises settle.
	w.step = function() {
		var next = w.queue.shift();
		if (!next) return Promise.resolve(false);
		w.time = Math.max(w.time, next.at);
		next.fn();
		return new Promise(function(r) { setImmediate(r); })
			.then(function() { return new Promise(function(r) { setImmediate(r); }); })
			.then(function() { return true; });
	};
	w.run = function(max) {
		var n = 0;
		function go() {
			if (n++ >= (max || 400)) return Promise.resolve();
			return w.step().then(function(more) { return more ? go() : undefined; });
		}
		return go();
	};
	return w;
}

function busy(stage, extra) {
	var a = { current: '1.0.25', installing: true, stage: stage, target: '1.0.26' };
	Object.keys(extra || {}).forEach(function(k) { a[k] = extra[k]; });
	return a;
}

// --- 1. an update that works ------------------------------------------------

function success() {
	var w = world([
		busy('checking'),
		busy('downloading', { done: 1048576, total: 3145728 }),
		busy('verifying'),
		busy('unpacking'),
		busy('installing'),
		'down', 'down-answered', 'down',
		{ current: '1.0.26', installing: false }
	]);
	w.view._installWindow('1.0.26');
	check('the window opens, named for the version', w.text().indexOf('1.0.26 sürümüne güncelleniyor') >= 0);
	check('and lists every step from the start',
		['Sürüm denetleniyor', 'İndiriliyor', 'Sağlama toplamı doğrulanıyor', 'Paket açılıyor',
			'Kuruluyor', 'Servis yeniden başlatılıyor', 'Sayfa yenileniyor']
			.every(function(s) { return w.text().indexOf(s) >= 0; }));

	return w.step().then(function() { return w.step(); }).then(function() {
		// The download: the one step long enough to need a number that moves.
		check('the download says how far it has got', w.text().indexOf('1.0 / 3.0 MB') >= 0);
		var bar = w.bar();
		check('with a bar a third of the way', !!bar && /width:33%/.test(bar.getAttribute('style') || ''));
		return w.run();
	}).then(function() {
		check('the service going quiet is shown as restarting, not as an error',
			w.text().indexOf('Servis yeniden başlatılıyor') >= 0 && w.text().indexOf('Kapat') < 0);
		check('the end is announced', w.text().indexOf('1.0.26 kuruldu') >= 0);

		// Past the cache, every one of this app's files, before the reload.
		var fresh = w.fetched.filter(function(f) { return f.cache === 'reload'; });
		var every = w.view._ownFiles.every(function(file) {
			return fresh.some(function(f) { return f.url === '/luci-static/resources/' + file; }) &&
				fresh.some(function(f) { return f.url === '/luci-static/resources/' + file + '?v=abc123'; });
		});
		check('every interface file is fetched past the cache, both addresses', every);
		check('and then the page reloads itself, once', w.reloaded === 1);
	});
}

// --- 2. an update that fails before the installer runs ---------------------

function failure() {
	var w = world([
		busy('checking'),
		busy('downloading', { done: 500000, total: 3145728 }),
		{ current: '1.0.25', installing: false, stage: 'failed', target: '1.0.26',
			check_error: 'the download does not match the checksum' }
	]);
	w.view._installWindow('1.0.26');
	return w.run().then(function() {
		check('a failure is shown', w.text().indexOf('checksum') >= 0);
		check('with a way out of the window', w.text().indexOf('Kapat') >= 0);
		check('and the page is not reloaded onto a version that is not there', w.reloaded === 0);
		check('and nothing is fetched as if it had worked', w.fetched.length === 0);
	});
}

// --- 3. the installer puts the old version back ----------------------------

function rollback() {
	var w = world([
		busy('installing'),
		'down', 'down',
		{ current: '1.0.25', installing: false }
	]);
	w.view._installWindow('1.0.26');
	return w.run().then(function() {
		check('coming back on the old version is named as a rollback, not success',
			w.text().indexOf('1.0.26 başlatılamadı') >= 0 && w.text().indexOf('1.0.25 geri yüklendi') >= 0);
		check("and the installer's own account is shown", w.logAsked === 1 && w.text().indexOf('rolled back') >= 0);
		check('and there is no reload', w.reloaded === 0);
	});
}

// --- 4. the service never comes back ----------------------------------------

function gone() {
	var w = world([ busy('installing'), 'down' ]);
	w.view._installWindow('1.0.26');
	return w.run(400).then(function() {
		check('silence is not waited on for ever', w.text().indexOf('üç dakika') >= 0);
		check('and says where the account of it is', w.text().indexOf('/tmp/xwrt-update.log') >= 0);
		check('the window stopped asking', w.queue.length === 0);
		check('and did not reload', w.reloaded === 0);
		// A few seconds of silence must not have been called failure: three
		// minutes is the limit, at one poll a second.
		check('it waited the whole three minutes first', w.time >= 180000);
	});
}

// --- 5. no page is left behind ----------------------------------------------

function ownFiles() {
	var base = path.join(__dirname, '..', 'htdocs', 'luci-static', 'resources');
	var shipped = [];
	(function walk(dir, rel) {
		fs.readdirSync(dir).forEach(function(n) {
			var p = path.join(dir, n), r = rel ? rel + '/' + n : n;
			if (fs.statSync(p).isDirectory()) walk(p, r);
			else if (/\.js$/.test(n)) shipped.push(r);
		});
	})(base, '');
	var listed = stub.load('view/xwrt/about.js')._ownFiles.slice();
	shipped.sort(); listed.sort();
	check('the refresh list is exactly the files that ship',
		JSON.stringify(shipped) === JSON.stringify(listed));
	if (JSON.stringify(shipped) !== JSON.stringify(listed))
		console.log('     shipped: ' + shipped.join(', ') + '\n     listed:  ' + listed.join(', '));
	return Promise.resolve();
}

console.log('-- an update that works'); success()
	.then(function() { console.log('\n-- an update that fails'); return failure(); })
	.then(function() { console.log('\n-- the old version put back'); return rollback(); })
	.then(function() { console.log('\n-- the service never returns'); return gone(); })
	.then(function() { console.log('\n-- nothing left on the old version'); return ownFiles(); })
	.then(function() {
		console.log('\n' + (fail
			? 'the install window says something other than what happened'
			: 'the install window shows each step, knows restarting from gone, and reloads onto the new version'));
		process.exit(fail);
	}).catch(function(e) { console.log('FAIL ' + (e && e.stack || e)); process.exit(1); });
