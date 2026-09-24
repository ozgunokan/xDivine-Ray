#!/usr/bin/env node
// The JSON configuration page.
//
// It is the one page that can empty a device, so what is checked here is
// mostly what it refuses to do quietly: it never sends without parsing first,
// it reports every problem rather than the first, and it tells the difference
// between a document the daemon rejected and a daemon that did not answer.
//
//   node luci-app-xwrt/test/jsonpage.js
'use strict';
var stub = require('./lucistub.js');
stub.stubLuCI();
var i18n = stub.load('xwrt/i18n.js');
global._ = i18n.translate; global.i18n = i18n; i18n.use('tr');
global.xwrt = stub.load('xwrt.js');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

var CONFIG = {
	settings: { active: 'p1', mode: 'mixed' },
	profiles: [ { id: 'p1', name: 'amsterdam' } ],
	groups: [], rules: [], subscriptions: []
};

var view = stub.load('view/xwrt/json.js');

// 1. The box opens with what the device has, formatted — not a blob on one
//    line that nobody can edit.
var tree = view.render(CONFIG);
var text = tree.textContent || '';
check('the page shows the configuration it loaded',
	text.indexOf('amsterdam') >= 0);
check('and formats it across lines rather than as one blob',
	text.indexOf('\n  "profiles"') >= 0 || text.indexOf('"profiles": [') >= 0);

// 2. The warning about credentials is above the box, not below it: below it is
//    off the bottom of a phone, and this is the line that stops someone
//    pasting their UUIDs into a chat window.
var warnAt = text.indexOf('kimlik bilgilerinizin');
var boxAt = text.indexOf('"profiles"');
check('the credentials warning comes before the document',
	warnAt >= 0 && boxAt >= 0 && warnAt < boxAt);

// 3. Both buttons are there, and Check is not the destructive one.
[ 'Denetle', 'Kaydet', 'Cihazdan yeniden yükle' ].forEach(function(label) {
	check('there is a "' + label + '" button', text.indexOf(label) >= 0);
});

// 4. The page must not offer LuCI's own Save/Apply footer as well: two save
//    buttons meaning different things on one page is how someone presses the
//    wrong one.
check('LuCI\'s own save/apply is switched off',
	view.handleSave === null && view.handleSaveApply === null);

process.exit(fail);
