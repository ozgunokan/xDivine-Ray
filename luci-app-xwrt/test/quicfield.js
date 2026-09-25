#!/usr/bin/env node
// The QUIC setting only exists in the mode it affects.
//
// It was written with LuCI's own `depends('mode', 'redirect')`, which is how
// every other conditional option on that page is written — and on the build it
// was reported from it showed in every mode anyway. There is no way to
// reproduce that here, so the field is simply not created unless the saved
// mode is redirect: an option that does not exist cannot be shown by anybody's
// form machinery.
//
//   node luci-app-xwrt/test/quicfield.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');
var formstub = require('./formstub.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

function fieldsFor(mode) {
	var record = formstub.install({ E: stub.El, uci: { mode: mode } });
	var view = stub.load('view/xwrt/settings.js');
	view.render([ null, record.devices, { lan_devices: [ 'br-lan' ], wan_device: 'wwan0_1' } ]);
	return record;
}

[ 'mixed', 'tun', 'tproxy' ].forEach(function(mode) {
	var r = fieldsFor(mode);
	check('no QUIC setting in ' + mode + ' mode', !r.byName['block_quic']);
	// And the mode selector itself is still there, so an empty result cannot
	// pass this test by accident.
	check('the ' + mode + ' page still built its mode selector', !!r.byName['mode']);
});

var r = fieldsFor('redirect');
check('the QUIC setting is there in redirect mode', !!r.byName['block_quic']);
if (r.byName['block_quic']) {
	check('and it defaults to on',
		String(r.byName['block_quic']['default']) === '1');
	check('and it says what it does',
		(r.byName['block_quic'].descr || '').indexOf('QUIC') >= 0);
}

process.exit(fail);
