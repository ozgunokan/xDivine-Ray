#!/usr/bin/env node
// The factory reset window.
//
// The one button in this interface that cannot be undone by pressing another.
// What is checked here is that it asks properly: it says what goes, in numbers
// from the device; it offers the backup that would undo it; it says what it
// means for a router whose only way out is the tunnel; and the button that does
// it is dead until "yes, all of it" has been ticked.
//
//   node luci-app-xwrt/test/factoryreset.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }
function settle() { return new Promise(function(r) { setImmediate(r); }); }

var CONFIG = {
	settings: { version: '1.0.25', mode: 'tun' },
	profiles: [ { id: 'p1' }, { id: 'p2' }, { id: 'p3' } ],
	groups: [ { id: 'g1' } ],
	rules: [ { id: 'r1' }, { id: 'r2' } ],
	subscriptions: [ { id: 's1' } ]
};

var calls = { reset: 0, downloads: [] };
global.xwrt.config = function() { return Promise.resolve(JSON.parse(JSON.stringify(CONFIG))); };
global.xwrt.resetConfig = function() {
	calls.reset++;
	return Promise.resolve({ reset: true, config: {
		settings: { mode: 'mixed' }, profiles: [], groups: [], rules: [], subscriptions: [] } });
};

var view = stub.load('view/xwrt/json.js');
view._io.download = function(text, name) { calls.downloads.push({ text: text, name: name }); };

function modal() { return global.MODALS[global.MODALS.length - 1]; }
function text() { var m = modal(); return m ? m.title + ' ' + m.body.textContent : ''; }
function buttons() { return modal().body.querySelectorAll('button'); }
function button(label) {
	var found = null;
	buttons().forEach(function(b) { if (b.textContent.indexOf(label) >= 0) found = b; });
	return found;
}
function checkbox() { return modal().body.querySelectorAll('input[type=checkbox]')[0]; }

// --- the page ---------------------------------------------------------------

var page = view.render(CONFIG);
var pageText = page.textContent;
check('the page has the reset, in its own section', pageText.indexOf('Fabrika ayarları') >= 0 &&
	pageText.indexOf('Fabrika ayarlarına döndür') >= 0);
check('and the old, misleading button name is gone', pageText.indexOf('Cihazdakini getir') < 0);
check('the button that only resets the box says so',
	pageText.indexOf('Düzenlemeleri geri al') >= 0);

// --- the window -------------------------------------------------------------

global.MODALS = [];
view._resetWindow(view).then(function() {
	check('a window opens', !!modal() && text().indexOf('Fabrika ayarlarına döndür') >= 0);

	// Numbers, from the device, not "all your data".
	check('it says exactly what goes',
		text().indexOf('3 sunucu, 1 grup, 2 kural ve 1 abonelik silinir') >= 0);
	check('and that the tunnel goes down now', text().indexOf('tünel şimdi kapatılır') >= 0);
	check('and what that means on a tunnel-only line, and the way back',
		text().indexOf('yalnızca tünel üzerinden') >= 0 &&
		text().indexOf('internet gerektirmez') >= 0);

	var go = button('Her şeyi sıfırla');
	check('the reset button is there', !!go);
	check('and dead until the box is ticked', !!go && go.disabled === true);

	// Pressing a dead button does nothing.
	go.click && go.click();
	return settle().then(function() {
		check('pressing it unticked resets nothing', calls.reset === 0);

		// The backup, before anything happens.
		var backup = button('Önce yedek indir');
		check('a backup is offered first', !!backup);
		backup.click();
		check('and it downloads the configuration as it is now',
			calls.downloads.length === 1 &&
			JSON.parse(calls.downloads[0].text).profiles.length === 3 &&
			/^xwrt-backup-/.test(calls.downloads[0].name));
		check('and says it did', text().indexOf('indirildi') >= 0);

		// Tick, then press.
		var box = checkbox();
		box.checked = true;
		(box.listeners.change || []).forEach(function(fn) {
			fn.call(box, { type: 'change', target: box });
		});
		check('ticking the box brings the button to life', go.disabled === false);

		go.click();
		return settle().then(settle);
	});
}).then(function() {
	check('the reset is asked for, once', calls.reset === 1);
	check('and the page says what happened',
		global.NOTIFICATIONS.some(function(n) {
			return n.node.textContent.indexOf('Fabrika ayarlarına dönüldü') >= 0;
		}));

	console.log('\n' + (fail
		? 'the reset can happen without the person pressing it knowing what it does'
		: 'the reset says what it removes, offers the backup first, and waits for a yes'));
	process.exit(fail);
}).catch(function(e) {
	console.log('FAIL ' + (e && e.stack || e));
	process.exit(1);
});
