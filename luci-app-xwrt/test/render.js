#!/usr/bin/env node
// Renders every xwrt view against a stubbed LuCI and checks what a browser
// would otherwise have to tell us.
//
// There is no LuCI to run here, so this is not a substitute for opening the
// pages. It catches the two things that kept getting through anyway: a view
// that throws on data it will really be given, and a table wide enough to push
// the whole page sideways on a phone — which looks like a broken theme rather
// than a layout bug, and is invisible on the desktop where the work is done.
//
//   node luci-app-xwrt/test/render.js

'use strict';

var fs = require('fs');
var path = require('path');

// The stub LuCI and the loader live in lucistub.js, because mobile.js needs
// the same ones: it measures the pages these views build, and a second copy of
// this file would be a second thing to keep true.
var stub = require('./lucistub.js');
var BASE = stub.BASE;
var El = stub.El;
var stubLuCI = stub.stubLuCI;
var load = stub.load;

// --- checks --------------------------------------------------------------

function walk(node, fn, parent) {
	if (!node || typeof node !== 'object' || !node.children) return;
	fn(node, parent);
	node.children.forEach(function(c) { walk(c, fn, node); });
}

function classesOf(node) {
	return String(node.className || '').split(/\s+/);
}

// Every table must sit in its own scroll box. Without one, a table wider than
// the screen scrolls the page instead of itself, and the menu and headings
// slide off with it.
function checkTablesScroll(name, tree, fail) {
	walk(tree, function(node, parent) {
		if (classesOf(node).indexOf('xwrt-table') < 0) return;
		if (!parent || classesOf(parent).indexOf('xwrt-scroll') < 0)
			fail(name + ': a table is not inside a .xwrt-scroll box');
	});
}

// A row with more cells than its header has columns means the table is
// misaligned for every reader, which is easy to do and hard to see in a diff.
function checkRowWidths(name, tree, fail) {
	walk(tree, function(node) {
		if (classesOf(node).indexOf('xwrt-table') < 0) return;
		var widths = node.children
			.filter(function(r) {
				var cl = classesOf(r);
				// An empty-table message is deliberately one cell wide.
				return cl.indexOf('tr') >= 0 && cl.indexOf('placeholder') < 0;
			})
			.map(function(r) {
				return r.children.filter(function(c) {
					var cl = classesOf(c);
					return cl.indexOf('td') >= 0 || cl.indexOf('th') >= 0;
				}).length;
			});
		var header = widths[0];
		widths.forEach(function(w, i) {
			if (i > 0 && w !== header)
				fail(name + ': row ' + i + ' has ' + w + ' cells, header has ' + header);
		});
	});
}

// On a phone the table becomes a list of cards, and a value with no label in
// front of it is a number nobody can identify. The labels are filled in from
// the header row, so a missing one means a column was added without a header —
// or that the filling stopped working.
function checkCellsAreLabelled(name, tree, fail) {
	walk(tree, function(node) {
		if (classesOf(node).indexOf('xwrt-cards') < 0) return;
		node.children.forEach(function(row) {
			var cl = classesOf(row);
			if (cl.indexOf('tr') < 0 || cl.indexOf('table-titles') >= 0 ||
			    cl.indexOf('placeholder') >= 0) return;
			row.children.forEach(function(cell, i) {
				var cc = classesOf(cell);
				if (cc.indexOf('td') < 0) return;
				// Buttons say what they do; a label in front of them is noise.
				if (cc.indexOf('cbi-section-actions') >= 0) return;
				if (!cell.getAttribute('data-title'))
					fail(name + ': cell ' + i + ' has no column label');
			});
		});
	});
}

// The other half of the same rule. A row that spans the table — the line that
// says it is empty — must carry no column label at all: in card mode the label
// is drawn in front of the cell, so one on a placeholder reads as
// "NameNo servers yet." It is invisible on a desktop, where labels are not
// drawn, and invisible in a diff; it was found in a screenshot.
function checkPlaceholdersAreNotLabelled(name, tree, fail) {
	walk(tree, function(node) {
		if (classesOf(node).indexOf('xwrt-cards') < 0) return;
		var head = node.children.filter(function(r) {
			return classesOf(r).indexOf('table-titles') >= 0;
		})[0];
		var columns = head ? head.children.length : 0;
		node.children.forEach(function(row) {
			var cl = classesOf(row);
			if (cl.indexOf('tr') < 0 || cl.indexOf('table-titles') >= 0) return;
			var spans = cl.indexOf('placeholder') >= 0 ||
				row.children.length < columns;
			if (!spans) return;
			row.children.forEach(function(cell) {
				var label = cell.getAttribute('data-title');
				if (label)
					fail(name + ': a placeholder row is labelled "' + label +
						'", which a phone prints in front of the message');
			});
		});
	});
}

// Every page has to carry the stylesheet, and each one inserts it itself:
// LuCI loads a view on its own, so a page that forgets gets the theme's
// layout and none of ours. On a phone that means tab strips that scroll
// sideways and tables that never become cards — on that page only, which is
// why it survives: every other page looks right, so the stylesheet is
// obviously installed and the one that does not is read as a different bug.
//
// The Settings page forgot for several releases. It was the one view that does
// not talk to the daemon, so it did not load xwrt.js at all, and the
// stylesheet lives there.
//
// This reads the directory rather than a list, so a view added later is
// covered without anyone remembering to add it here.
function checkEveryViewCarriesTheStylesheet(fail) {
	var dir = path.join(BASE, 'view', 'xwrt');
	fs.readdirSync(dir).filter(function(f) {
		return /\.js$/.test(f);
	}).forEach(function(f) {
		var src = fs.readFileSync(path.join(dir, f), 'utf8');
		if (src.indexOf('xwrt.style()') < 0)
			fail('view/xwrt/' + f + ' never inserts xwrt.style(), so none of ' +
				'the phone layout applies on that page');
		else
			console.log('ok   view/xwrt/' + f + ' carries the stylesheet');
	});
}

// The line that says a table is empty must not be a row of that table.
//
// A table lays a one-cell row out in the first column, and on the Rules page
// that column is three characters wide — so a sentence came out as a vertical
// ribbon of single words. `display: block` does not rescue it either: a block
// child of a table gets wrapped in an anonymous cell and lands in the same
// column. The message has to leave the table, which is what xwrt.scroll does;
// this checks that it did.
function checkEmptyMessageIsNotARow(name, tree, fail) {
	walk(tree, function(node) {
		if (!/(^|\s)xwrt-table(\s|$)/.test(node.className || '')) return;
		node.children.forEach(function(row) {
			if (classesOf(row).indexOf('placeholder') >= 0)
				fail(name + ': the "table is empty" line is still a row of the ' +
					'table, where it wraps into a narrow column');
		});
	});
	walk(tree, function(node) {
		if (!/(^|\s)xwrt-empty(\s|$)/.test(node.className || '')) return;
		if (!(node.textContent || '').trim())
			fail(name + ': the "table is empty" line was lifted out of the ' +
				'table and lost on the way');
	});
}

// And the lifting itself, on a table built here, because the views only cover
// whichever tables happen to be empty in the fixtures. Three things have to be
// true at once: the sentence survives, the row is gone, and the column titles
// go with it — headings over an empty space read as a page that failed to
// load.
function checkScrollLiftsEmptyMessage(fail) {
	var xwrt = load('xwrt.js');
	var table = E('div', { 'class': 'table xwrt-table' }, [
		E('div', { 'class': 'tr table-titles' }, [
			E('div', { 'class': 'th' }, '#'),
			E('div', { 'class': 'th' }, 'Name')
		]),
		E('div', { 'class': 'tr placeholder' }, [
			E('div', { 'class': 'td' }, E('em', {}, 'Nothing here yet.'))
		])
	]);
	var out = xwrt.scroll(table);
	var text = out.textContent || '';

	if (text.indexOf('Nothing here yet.') < 0)
		fail('scroll(): the empty-table message did not survive being lifted out');
	if (table.children.length)
		fail('scroll(): the table still has rows — expected the message row and ' +
			'the headings both gone');

	var lifted = null;
	walk(out, function(n) {
		if (/(^|\s)xwrt-empty(\s|$)/.test(n.className || '')) lifted = n;
	});
	if (!lifted)
		fail('scroll(): the message is not in an .xwrt-empty line');

	// And the ordinary case is untouched: a table with records keeps its
	// headings and its rows.
	var full = E('div', { 'class': 'table xwrt-table' }, [
		E('div', { 'class': 'tr table-titles' }, [ E('div', { 'class': 'th' }, 'Name') ]),
		E('div', { 'class': 'tr' }, [ E('div', { 'class': 'td' }, 'one') ])
	]);
	xwrt.scroll(full);
	if (full.children.length !== 2)
		fail('scroll(): a table with records lost a row');
	if ((full.children[1].children[0].getAttribute('data-title') || '') !== 'Name')
		fail('scroll(): a table with records lost its column labels');
}

// The capture mode is a name, not an identifier.
//
// The daemon stores `mixed` and `tproxy`; printing those straight out put a
// lower-case word in a table where everything else is a proper noun, next to a
// Settings page that calls the same thing Mixed. Capitalising the first letter
// is not the fix either — "Tproxy" and "Tun" are spelled that way nowhere.
function checkTheModeIsNamedProperly(name, tree, fail) {
	var text = tree.textContent || '';
	// The fixture is connected in mixed mode.
	if (text.indexOf('Mixed') < 0)
		fail(name + ': the capture mode is not named on the page');
	if (/(^|[\s>])mixed([\s<]|$)/.test(text))
		fail(name + ': the capture mode is printed as the stored identifier ' +
			'("mixed") rather than its name');
}

// --- the views and the data they will really be given --------------------

var fixtures = require('./fixtures.js');
var STATUS = fixtures.STATUS;
var CONFIG = fixtures.CONFIG;
var VIEWS = fixtures.VIEWS;
var MODALS = fixtures.MODALS;

// A dialog nobody can fill in is not a dialog. Every one of these has fields,
// and every field needs its label.
function checkModal(name, modal, fail) {
	var labelled = 0, fields = 0;
	walk(modal.body, function(node) {
		if (classesOf(node).indexOf('cbi-value-title') >= 0) labelled++;
		if (classesOf(node).indexOf('cbi-value-field') >= 0) fields++;
	});
	var text = modal.body.textContent || '';
	if (!text.trim())
		fail(name + ': the dialog is empty');
	if (fields > 0 && labelled < fields)
		fail(name + ': ' + (fields - labelled) + ' field(s) have no label');
}

// One string per language that must come out of the Status page. Rendering in
// Turkish without the catalog installed silently produces an English page, and
// nothing else in these checks would notice.
var PROOF = { en: 'Connection', tr: 'Bağlantı' };

// A failure is the one screen where falling back to English is least
// acceptable and easiest to miss: the journal renders whatever the daemon
// sent, so a broken translation still looks like a working page. These are the
// words that can only come from translating by code — the daemon's own message
// says neither of them.
var FAILURE_PROOF = {
	en: [ 'apply capture rules with nftables', 'command-line tools' ],
	tr: [ 'yakalama kuralları uygulanamadı', 'komut satırı araçlarını kurun' ]
};

function main() {
	stubLuCI();

	var i18n = load('xwrt/i18n.js');
	// The module wraps window._; the views call the bare global, which is the
	// same binding in a browser and a different one here.
	global._ = i18n.translate;
	// LuCI's loader binds `'require xwrt.i18n as i18n'` to a local name inside
	// the module; here those lines are inert string literals, so the binding
	// has to be made by hand or xwrt.js finds no catalog to translate failures
	// with.
	global.i18n = i18n;

	var failures = [];
	function fail(msg) { failures.push(msg); }

	checkEveryViewCarriesTheStylesheet(fail);
	checkScrollLiftsEmptyMessage(fail);

	[ 'en', 'tr' ].forEach(function(lang) {
		i18n.use(lang);
		// Reloaded per language because a view may translate at load time, in
		// a constant outside render().
		global.xwrt = load('xwrt.js');

		VIEWS.forEach(function(v) {
			var name = lang + ' ' + v.file, tree;
			try {
				tree = load(v.file).render(v.data);
			} catch (e) {
				fail(name + ': render threw: ' + e.message);
				return;
			}
			checkTablesScroll(name, tree, fail);
			checkRowWidths(name, tree, fail);
			checkCellsAreLabelled(name, tree, fail);
			checkPlaceholdersAreNotLabelled(name, tree, fail);
			checkEmptyMessageIsNotARow(name, tree, fail);
			if (v.file === 'view/xwrt/status.js')
				checkTheModeIsNamedProperly(name, tree, fail);
			if (v.file === 'view/xwrt/status.js' &&
			    tree.textContent.indexOf(PROOF[lang]) < 0)
				fail(name + ': rendered without the ' + lang + ' catalog');
			if (v.file === 'view/xwrt/logs.js')
				FAILURE_PROOF[lang].forEach(function(want) {
					if (tree.textContent.indexOf(want) < 0)
						fail(name + ': the recorded failure is not in ' + lang +
							' — expected to read "' + want + '"');
				});
			console.log('ok   ' + name);
		});

		MODALS.forEach(function(m, i) {
			var name = lang + ' ' + m.file + ' #' + (i + 1);
			global.MODALS = [];
			try {
				m.open(load(m.file));
			} catch (e) {
				fail(name + ': opening the dialog threw: ' + e.message);
				return;
			}
			var opened = global.MODALS[global.MODALS.length - 1];
			if (!opened) {
				fail(name + ': no dialog was opened');
				return;
			}
			var want = m.expect[lang];
			if (String(opened.title).indexOf(want) < 0)
				fail(name + ': title is ' + JSON.stringify(opened.title) +
					', expected to contain ' + JSON.stringify(want));
			checkModal(name, opened, fail);
			console.log('ok   ' + name + ' — ' + opened.title);
		});
	});

	if (failures.length) {
		failures.forEach(function(f) { console.error('FAIL ' + f); });
		process.exit(1);
	}
	console.log('\nall views render in both languages; every table stacks, ' +
		'labelled and square');
}

main();
