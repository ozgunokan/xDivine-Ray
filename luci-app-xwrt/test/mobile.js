#!/usr/bin/env node
// Measures the pages at phone width in a real browser.
//
// render.js checks the shape of what the views build; it cannot tell whether
// the result fits on a screen, because there is no screen. This loads the
// stylesheet into Chromium at 390 CSS pixels — an ordinary phone — together
// with the markup LuCI puts around it, and measures.
//
// The pages are the real ones. Each view is asked to build itself with the
// same data render.js gives it, and the result is written out as HTML — so
// what gets measured is what ships. It used to be markup written by hand here
// to look like the pages, which is a copy of the thing under test kept up to
// date by memory; it passed while the real Servers table had a column it did
// not know about.
//
// The theme is imitated, not imported: LuCI's themes live on the router, and
// the only parts that matter here are the two habits every one of them has —
// a tab strip kept on one line that scrolls sideways, and floated list items.
// The rules under test have to win against those, which is what the fake theme
// below is for. A theme that does something else entirely is not covered, and
// nothing here replaces looking at the real thing once.
//
//   node luci-app-xwrt/test/mobile.js

'use strict';

var fs = require('fs');
var path = require('path');

var stub = require('./lucistub.js');
var fixtures = require('./fixtures.js');
var formstub = require('./formstub.js');

var BASE = stub.BASE;

var PHONE = { width: 390, height: 844 };
var DESKTOP = { width: 1280, height: 900 };

// What a LuCI theme does to a tab strip, reduced to the part that fights with
// the rules under test. There are two ways themes keep a strip on one line and
// both are in the wild, so both are measured: a flex row that never wraps, and
// inline-block items in a nowrap line. Either way the overflow scrolls, and
// either way it has to stop.
var COMMON_CSS = [
	'* { box-sizing: border-box; }',
	'body { margin: 0; font: 14px/1.4 sans-serif; }',
	'#maincontent { padding: 1em; }',
	'ul.tabs { list-style: none; margin: 0 0 1em 0; padding: 0;',
	'  border-bottom: 1px solid #ccc; }',
	'ul.cbi-tabmenu { list-style: none; margin: 0; padding: 0; }',
	'ul.tabs > li > a { display: block; padding: .6em 1.2em;',
	'  text-decoration: none; white-space: nowrap; }',
	'ul.cbi-tabmenu > li > a { display: block; padding: .5em 1em;',
	'  white-space: nowrap; }',
	'.cbi-value { display: flex; }',
	'.cbi-value-title { width: 30%; text-align: right; padding-right: 1em; }',
	'.cbi-value-field { width: 70%; }',
	'.modal { width: 40rem; padding: 1em; border: 1px solid #ccc; }'
].join('\n');

var THEMES = {
	// The modern themes: one flex row, shrink-to-fit off, scroll the remainder.
	flex: [
		'ul.tabs, ul.cbi-tabmenu { display: flex; overflow-x: auto; }',
		'ul.tabs > li, ul.cbi-tabmenu > li { flex: 0 0 auto; }'
	].join('\n'),
	// The older ones: a line of inline-blocks that is told not to break.
	nowrap: [
		'ul.tabs, ul.cbi-tabmenu { white-space: nowrap; overflow-x: auto; }',
		'ul.tabs > li, ul.cbi-tabmenu > li { display: inline-block; }'
	].join('\n'),
	// And the habit that put the Settings page's help text off the right of a
	// real phone: an information icon drawn with left padding on a box that is
	// already `width: 100%`, sized the old way so the padding is added outside
	// that width. The box ends up wider than its column, the text wraps at an
	// edge nobody can see, and every line looks cut off mid-word.
	//
	// It is here because this is what the theme on the reporter's router does,
	// and because `* { box-sizing: border-box }` above — which this harness
	// sets for convenience — is exactly the kindness that hid it. A theme is
	// not obliged to be kind.
	// luci-theme-bootstrap, quoted from its own cascade.css. This is the one
	// that turned the Servers page into a column of five buttons per row, five
	// rows tall, while every other theme drew them side by side.
	//
	// The rule that does it is `.td.cbi-section-actions > * { display: flex }`.
	// The theme assumes the actions cell holds exactly one element — LuCI's own
	// generated forms put the buttons in a wrapper div — and makes that one
	// element the flex row. Buttons placed directly in the cell each become a
	// block-level flex container instead, and block-level boxes stack.
	//
	// It is here rather than fixed only in the stylesheet because the fix is to
	// give the theme the wrapper it is looking for, and a wrapper is easy to
	// leave out of the next table somebody adds.
	bootstrap: [
		'ul.tabs, ul.cbi-tabmenu { display: flex; overflow-x: auto; }',
		'ul.tabs > li, ul.cbi-tabmenu > li { flex: 0 0 auto; }',
		'.td.cbi-section-actions {',
		'  text-align: right; vertical-align: middle; width: 15%; }',
		'.td.cbi-section-actions > * { display: flex; }',
		'.td.cbi-section-actions > :not(.cbi-dropdown) > *,',
		'.td.cbi-section-actions > * > form > * { flex: 1 1 4em; margin: 0 1px; }',
		'.td.cbi-section-actions > * > form { display: inline-flex; margin: 0; }'
	].join('\n'),
	icon: [
		'ul.tabs, ul.cbi-tabmenu { display: flex; overflow-x: auto; }',
		'ul.tabs > li, ul.cbi-tabmenu > li { flex: 0 0 auto; }',
		'.cbi-value-description {',
		'  box-sizing: content-box; width: 100%; padding-left: 1.9em;',
		'  position: relative; }',
		'.cbi-value-description::before {',
		'  content: "i"; position: absolute; left: 0; width: 1.3em;',
		'  height: 1.3em; border-radius: 50%; background: #2a6; color: #fff;',
		'  text-align: center; }'
	].join('\n')
};

var TABS = [ 'Status', 'Servers', 'Rules', 'Traffic', 'Settings', 'Log', 'About' ];
var FORM_TABS = [ 'General', 'DNS', 'Routing', 'Advanced' ];

// buildPages renders every view and every dialog once, and hands back the
// HTML. Rendering happens before Chromium is launched: a view that throws is
// render.js's failure to report, not this one's.
function buildPages(lang) {
	stub.stubLuCI();
	var i18n = stub.load('xwrt/i18n.js');
	global._ = i18n.translate;
	global.i18n = i18n;
	i18n.use(lang);
	global.xwrt = stub.load('xwrt.js');

	var out = [];

	// The Settings page first, because it is the one that keeps getting away.
	// It is built from what the page really asks LuCI for — every field, every
	// choice, every line of help text, in this language — wrapped in the markup
	// LuCI would have produced. See formstub.js for what that imitation covers
	// and what it does not.
	var record = formstub.install({ E: stub.El });
	var settings = stub.load('view/xwrt/settings.js');
	settings.render([ null, record.devices,
		{ lan_devices: [ 'br-lan' ], wan_device: 'wwan0_1' } ]);
	out.push({ name: 'settings-form', html: formstub.markup(record), form: true });

	fixtures.VIEWS.forEach(function(v) {
		out.push({
			name: v.file.replace('view/xwrt/', '').replace('.js', ''),
			html: stub.serialize(stub.load(v.file).render(v.data))
		});
	});
	fixtures.MODALS.forEach(function(m, i) {
		global.MODALS = [];
		m.open(stub.load(m.file));
		var opened = global.MODALS[global.MODALS.length - 1];
		if (!opened) return;
		// A dialog is laid out by the theme's .modal box, not by the page, so
		// it is measured inside one.
		out.push({
			name: 'dialog#' + (i + 1) + ' ' + String(opened.title).slice(0, 24),
			html: '<div class="modal"><h4>' + opened.title + '</h4>' +
				stub.serialize(opened.body) + '</div>',
			modal: true
		});
	});
	return out;
}

// page wraps one rendered view in the markup LuCI puts around it: the tab
// strip above it, the form tabs a settings page carries, and the theme.
function page(xwrtCSS, theme, body, withTabs) {
	return '<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">' +
		'<meta name="viewport" content="width=device-width, initial-scale=1">' +
		'<style>' + COMMON_CSS + '</style>' +
		'<style>' + THEMES[theme] + '</style>' +
		'<style>' + xwrtCSS + '</style></head><body>' +
		'<div id="maincontent"><div class="container">' +
		(withTabs
			? '<ul class="tabs">' + TABS.map(function(t, i) {
				return '<li class="tabmenu-item' + (i === 0 ? ' active' : '') +
					'"><a href="#">' + t + '</a></li>';
			}).join('') + '</ul>' +
			'<ul class="cbi-tabmenu">' + FORM_TABS.map(function(t) {
				return '<li><a href="#">' + t + '</a></li>';
			}).join('') + '</ul>'
			: '') +
		body +
		'</div></div></body></html>';
}

// The stylesheet as the views ship it, read out of the module rather than
// copied, so this measures what is actually installed.
function stylesheet() {
	global.baseclass = { extend: function(o) { return o; } };
	global.rpc = { declare: function() { return function() {}; } };
	global.window = {};
	var css = null;
	global.E = function(tag, attrs, children) {
		if (tag === 'style') css = String(children);
		return {};
	};
	global._ = function(s) { return s; };
	var i18n = new Function(
		fs.readFileSync(path.join(BASE, 'xwrt', 'i18n.js'), 'utf8'))();
	global.i18n = i18n;
	var mod = new Function(fs.readFileSync(path.join(BASE, 'xwrt.js'), 'utf8'))();
	mod.style();
	if (!css)
		throw new Error('could not read the stylesheet out of xwrt.js');
	return css;
}

async function measure(browser, size, html, shot) {
	var ctx = await browser.newContext({
		viewport: size, deviceScaleFactor: 2, isMobile: size.width < 700
	});
	var page = await ctx.newPage();
	await page.setContent(html, { waitUntil: 'load' });
	// XWRT_SHOT=<dir> also writes the rendered page out, for looking at.
	if (shot && process.env.XWRT_SHOT)
		await page.screenshot({
			path: path.join(process.env.XWRT_SHOT, shot), fullPage: true
		});
	var out = await page.evaluate(function() {
		function rows(sel) {
			var el = document.querySelector(sel);
			if (!el) return null;
			var tops = {};
			Array.prototype.forEach.call(el.children, function(li) {
				tops[Math.round(li.getBoundingClientRect().top)] = true;
			});
			return {
				lines: Object.keys(tops).length,
				scrolls: el.scrollWidth > el.clientWidth + 1,
				overflowing: Array.prototype.filter.call(el.children, function(li) {
					return li.getBoundingClientRect().right > window.innerWidth + 1;
				}).map(function(li) { return li.textContent.trim(); })
			};
		}
		var wide = [];
		document.querySelectorAll('*').forEach(function(el) {
			if (el.getBoundingClientRect().right > window.innerWidth + 1)
				wide.push((el.className || el.tagName) + ': ' +
					el.textContent.trim().slice(0, 40));
		});
		// The buttons at the end of each table row. On a desktop they belong
		// on one line; a theme that stacks them makes every row as tall as
		// the buttons it holds, and a five-row table a screenful.
		var actions = [];
		document.querySelectorAll(
			'.xwrt-table > .tr > .td.cbi-section-actions').forEach(function(cell) {
			var btns = cell.querySelectorAll('button');
			if (btns.length < 2) return;
			var tops = {};
			Array.prototype.forEach.call(btns, function(b) {
				tops[Math.round(b.getBoundingClientRect().top)] = true;
			});
			actions.push({
				buttons: btns.length,
				lines: Object.keys(tops).length,
				height: Math.round(cell.getBoundingClientRect().height)
			});
		});

		return {
			pageScroll: document.documentElement.scrollWidth -
				document.documentElement.clientWidth,
			tabs: rows('ul.tabs'),
			formTabs: rows('ul.cbi-tabmenu'),
			actions: actions,
			wide: wide.slice(0, 5)
		};
	});
	await ctx.close();
	return out;
}

async function main() {
	var css = stylesheet();
	var failures = [];

	// Playwright is not a dependency of anything else here, and a checkout
	// without it should not look like a failing test suite.
	var chromium;
	try {
		chromium = require('playwright').chromium;
	} catch (e) {
		console.log('skip playwright is not installed ' +
			'(npm i -g playwright) — phone layout not measured');
		return;
	}
	var browser = await chromium.launch();

	// Turkish, because it is the longer language here and the one that
	// overflows first: "Sertifikayı sabitle" against "Pin the certificate".
	// A layout measured only in English is measured in its easiest case.
	for (var lang of [ 'tr', 'en' ]) {
		var pages = buildPages(lang);

		for (var theme of Object.keys(THEMES)) {
			for (var p of pages) {
				var where = lang + '/' + theme + ' ' + p.name;
				var html = page(css, theme, p.html, !p.modal);
				var shot = (lang + '-' + theme + '-' + p.name)
					.replace(/[^a-z0-9.-]+/gi, '-') + '.png';
				var phone = await measure(browser, PHONE, html, shot);

				if (phone.pageScroll > 1)
					failures.push(where + ': the page scrolls sideways by ' +
						phone.pageScroll + 'px at ' + PHONE.width + 'px' +
						(phone.wide.length ? ' — widest: ' +
							phone.wide.join(' | ') : ''));

				if (p.modal)
					continue;

				[ [ 'page tabs', phone.tabs ], [ 'settings tabs', phone.formTabs ] ]
					.forEach(function(pair) {
						var what = where + ': ' + pair[0], t = pair[1];
						if (!t) return;
						if (t.scrolls)
							failures.push(what + ' still scroll sideways on a phone');
						if (t.overflowing.length)
							failures.push(what + ' run off the screen: ' +
								t.overflowing.join(', '));
					});

				// Seven tabs cannot fit on one phone line; if they claim to,
				// the media query did not apply and the strip is scrolling.
				if (phone.tabs && phone.tabs.lines < 2)
					failures.push(where + ': page tabs are still on one line at ' +
						PHONE.width + 'px');

				// The desktop is not the problem being solved, and must stay
				// as it was. The two pages with per-row buttons are measured
				// there, because a stacked column of them is a desktop
				// problem that a phone layout hides.
				if (p.name === 'profiles' || p.name === 'rules') {
					var desktop = await measure(browser, DESKTOP, html);
					if (desktop.tabs && desktop.tabs.lines !== 1)
						failures.push(where + ': page tabs wrapped on the ' +
							'desktop, where they fit');
					if (desktop.pageScroll > 1)
						failures.push(where + ': the page scrolls sideways on ' +
							'the desktop');
					desktop.actions.forEach(function(a) {
						if (a.lines > 1)
							failures.push(where + ': the ' + a.buttons +
								' buttons on a row are stacked ' + a.lines +
								' deep on the desktop, making the row ' +
								a.height + 'px tall');
					});
				}
			}
			if (!failures.length)
				console.log('ok   ' + lang + ' · ' + theme + ' theme — ' +
					pages.length + ' real pages and dialogs at ' + PHONE.width +
					'px: none scrolls sideways, tabs wrap, desktop unchanged');
		}
	}

	await browser.close();

	if (failures.length) {
		failures.forEach(function(f) { console.error('FAIL ' + f); });
		process.exit(1);
	}
}

main().catch(function(e) {
	console.error(e);
	process.exit(1);
});
