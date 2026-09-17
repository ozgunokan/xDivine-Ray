#!/usr/bin/env node
// Checks the Turkish catalog against the strings the views actually use.
//
// The catalog is a plain table keyed by the English source text, so it drifts
// in exactly two ways: a string is added to a view and never translated, which
// shows up as one English line in an otherwise Turkish page; or a string is
// reworded and its old entry stays behind, translating nothing. Both are
// invisible unless somebody reads the page in Turkish, and neither shows up in
// a diff. This finds them.
//
//   node luci-app-xwrt/test/i18n.js

'use strict';

var fs = require('fs');
var path = require('path');

var BASE = path.join(__dirname, '..', 'htdocs', 'luci-static', 'resources');

// Every _( '…' ) in a file, read the way the browser will read it: the literal
// after the call, with its escapes resolved. A regular expression would trip
// over the apostrophes that Turkish and English are both full of.
function literals(src) {
	var found = [];
	for (var i = 0; i < src.length; i++) {
		if (src[i] !== '_') continue;
		if (i > 0 && /[A-Za-z0-9_$.]/.test(src[i - 1])) continue;
		var j = i + 1;
		while (j < src.length && /\s/.test(src[j])) j++;
		if (src[j] !== '(') continue;
		j++;
		while (j < src.length && /\s/.test(src[j])) j++;
		var q = src[j];
		if (q !== '\'' && q !== '"') continue;
		var k = j + 1, lit = '';
		while (k < src.length) {
			if (src[k] === '\\') { lit += src[k] + src[k + 1]; k += 2; continue; }
			if (src[k] === q) break;
			lit += src[k]; k++;
		}
		found.push(JSON.parse('"' +
			lit.replace(/\\'/g, '\'').replace(/"/g, '\\"') + '"'));
		i = k;
	}
	return found;
}

function sources() {
	var files = [ 'xwrt.js' ];
	fs.readdirSync(path.join(BASE, 'view', 'xwrt')).forEach(function(f) {
		if (/\.js$/.test(f)) files.push(path.join('view', 'xwrt', f));
	});
	return files;
}

function loadCatalog() {
	// The module reads the language out of its surroundings and touches the
	// DOM to translate the menu; with none of that present it has to fall
	// through to English without throwing, which is itself worth asserting.
	global.baseclass = { extend: function(o) { return o; } };
	global.window = {};
	var src = fs.readFileSync(path.join(BASE, 'xwrt', 'i18n.js'), 'utf8');
	var mod = new Function(src)();
	if (mod.language() !== 'en')
		throw new Error('with no language anywhere, the catalog should be English');
	return mod;
}

function main() {
	var i18n = loadCatalog();
	var tr = i18n.catalog('tr');
	var failures = [];

	var used = {};
	sources().forEach(function(rel) {
		literals(fs.readFileSync(path.join(BASE, rel), 'utf8')).forEach(function(s) {
			(used[s] = used[s] || []).push(rel);
		});
	});

	// The menu titles never pass through _() in a view — the server renders
	// them — so they are used strings that no source file mentions.
	Object.keys(i18n.menuTitles).forEach(function(k) {
		used[i18n.menuTitles[k]] = used[i18n.menuTitles[k]] || [ 'menu.d' ];
	});

	Object.keys(used).forEach(function(s) {
		if (!tr[s])
			failures.push('no Turkish for ' + JSON.stringify(s) +
				'  (' + used[s].join(', ') + ')');
	});

	Object.keys(tr).forEach(function(s) {
		if (!used[s])
			failures.push('catalog entry nothing uses: ' + JSON.stringify(s));
	});

	// A translation that loses a %s formats into the wrong thing at runtime —
	// a missing number, or the literal placeholder on screen.
	Object.keys(tr).forEach(function(s) {
		var a = (s.match(/%[sd]/g) || []).join('');
		var b = (tr[s].match(/%[sd]/g) || []).join('');
		if (a !== b)
			failures.push('placeholders differ: ' + JSON.stringify(s) +
				' [' + a + '] vs [' + b + ']');
	});

	if (failures.length) {
		failures.forEach(function(f) { console.error('FAIL ' + f); });
		process.exit(1);
	}
	console.log('ok   ' + Object.keys(tr).length +
		' strings, all used, all translated, placeholders intact');
}

main();
