#!/usr/bin/env node
// The "saved, but not applied yet" banner.
//
// It has three jobs and got two of them wrong on a real router. It must appear
// when a change really is waiting, it must not stack up one banner per edit,
// and it must go away when the change it is asking for has been applied —
// instead of sitting there next to "Applied." until someone closes it by hand,
// which reads as though something is still pending.
//
// Whether a change is waiting at all is the daemon's judgement, and that is
// tested in Go (fingerprint_test.go). This is about what the page does with
// the answer.
//
//   node luci-app-xwrt/test/notice.js

'use strict';

var stub = require('./lucistub.js');

var failures = [];
function fail(msg) { failures.push(msg); }
function ok(msg) { console.log('ok   ' + msg); }

function shown() {
	return global.MESSAGES.children.length;
}

function main() {
	stub.stubLuCI();
	var i18n = stub.load('xwrt/i18n.js');
	global._ = i18n.translate;
	global.i18n = i18n;
	var xwrt = stub.load('xwrt.js');

	// Nothing waiting: no banner. The daemon leaves the flag off entirely when
	// the change cannot reach the running core — deleting a server that is not
	// the active one, for instance.
	xwrt.checked({ deleted: 'p_other' });
	if (shown() !== 0)
		fail('a change the core cannot see still raised the "not applied" banner');
	else
		ok('a change that does not reach the core raises nothing');

	// Something waiting: one banner.
	xwrt.checked({ id: 'r1', reconnect_required: true });
	if (shown() !== 1)
		return fail('a pending change did not raise the banner');
	ok('a pending change raises the banner');

	// Two more edits in a row: still one banner, not three.
	xwrt.checked({ id: 'r2', reconnect_required: true });
	xwrt.checked({ id: 'r3', reconnect_required: true });
	if (shown() !== 1)
		fail('three edits raised ' + shown() + ' banners; each with its own ' +
			'button, and applying one leaves the others claiming work is waiting');
	else
		ok('three edits in a row raise one banner, not three');

	// And it goes when the change has been applied.
	xwrt.dismissApplyNotice();
	if (shown() !== 0)
		fail('the banner survived being applied — it sits next to "Applied." ' +
			'until the operator closes it by hand');
	else
		ok('applying takes the banner down');

	// A later edit raises a fresh one: the handle was released, not merely
	// hidden.
	xwrt.checked({ id: 'r4', reconnect_required: true });
	if (shown() !== 1)
		fail('after applying once, a new pending change raises no banner at all');
	else
		ok('a new pending change after that raises a fresh banner');

	if (failures.length) {
		failures.forEach(function(f) { console.error('FAIL ' + f); });
		process.exit(1);
	}
	console.log('\nthe apply banner appears once, and leaves when it is done');
}

main();
