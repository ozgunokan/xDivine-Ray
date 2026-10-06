#!/usr/bin/env node
// Every link in the interface has to point at a page that exists.
//
// The Status page carried one that did not, for several releases: the "a new
// version is available" link was built as admin/vpn/xwrt/about while the menu
// declares admin/vpn/xdivine-ray/about. Clicking it gave a 404.
//
// That particular link is the only thing anywhere in the interface that tells
// somebody an update exists, so a 404 there is an update nobody installs — and
// nothing fails, nothing is logged, and the page it came from looks perfect.
// The name in the path is the project's old one, which is exactly the kind of
// rename that gets finished everywhere except one line.
//
// So the links are read out of the source and checked against the menu, rather
// than against a list kept here, which would be one more thing to fall out of
// date in the same way.
//
//   node luci-app-xwrt/test/links.js
'use strict';
var fs = require('fs');
var path = require('path');

var fail = 0;
function check(name, cond) { console.log((cond ? 'ok   ' : 'FAIL ') + name); if (!cond) fail = 1; }

var ROOT = path.join(__dirname, '..');
var MENU = path.join(ROOT, 'root/usr/share/luci/menu.d/luci-app-xwrt.json');
var VIEWS = path.join(ROOT, 'htdocs/luci-static/resources/view/xwrt');

var menu = JSON.parse(fs.readFileSync(MENU, 'utf8'));
var paths = Object.keys(menu);
check('the menu declares pages at all', paths.length > 0);

// L.url('admin', 'vpn', 'xdivine-ray', 'logs') -> admin/vpn/xdivine-ray/logs
var CALL = /L\.url\(([^)]*)\)/g;

var files = fs.readdirSync(VIEWS).filter(function(f) { return /\.js$/.test(f); });
var found = 0;

files.forEach(function(f) {
	var src = fs.readFileSync(path.join(VIEWS, f), 'utf8');
	var m;
	while ((m = CALL.exec(src)) !== null) {
		var args = m[1].split(',').map(function(a) { return a.trim(); });
		// Only the links written as plain strings can be checked from here. One
		// built from a variable is not this test's business, and pretending to
		// check it would be worse than saying nothing.
		if (!args.every(function(a) { return /^'[^']*'$/.test(a); }))
			continue;
		var url = args.map(function(a) { return a.slice(1, -1); }).join('/');
		found++;
		check(f + ': ' + url, paths.indexOf(url) >= 0);
	}
});

check('there were links to check', found > 0);

// And the reverse, which is the cheaper half of the same mistake: a page in the
// menu whose view file was never written is a menu entry that opens nothing.
paths.forEach(function(p) {
	var action = menu[p].action || {};
	if (action.type !== 'view')
		return;
	var file = path.join(ROOT, 'htdocs/luci-static/resources/view', action.path + '.js');
	check('the menu entry ' + p + ' has a page', fs.existsSync(file));
});

console.log('\n' + (fail ? 'the interface links somewhere that is not there' :
	'every link points at a page, and every page is reachable'));
process.exit(fail);
