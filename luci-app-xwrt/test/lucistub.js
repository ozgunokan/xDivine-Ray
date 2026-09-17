#!/usr/bin/env node
// A LuCI small enough to fit in one file, plus a way to write out what the
// views built.
//
// Two harnesses need this. render.js asks the views to build their pages and
// then inspects the result as a tree; mobile.js takes the same pages, turns
// them into HTML and measures them in a real browser at phone width. Before
// this file existed the second one measured markup written by hand to look
// like the pages — which is a copy of the thing under test, kept up to date by
// memory. It measured a page nobody ships.
//
// Imitating the browser here, rather than teaching the views about a stub, is
// what keeps the views free of test-shaped branches.

'use strict';

var fs = require('fs');
var path = require('path');

var BASE = path.join(__dirname, '..', 'htdocs', 'luci-static', 'resources');

// A 2D context that accepts every call and draws nothing. The traffic page
// paints a chart on render; none of that is what these checks are about, but it
// has to not throw.
var CANVAS_2D = new Proxy({}, {
	get: function(_, prop) {
		if (prop === 'canvas') return { width: 600, height: 200 };
		if (prop === 'measureText') return function() { return { width: 10 }; };
		return function() {};
	},
	set: function() { return true; }
});

// A DOM element that is close enough to the real one for the code under test
// to treat it as ordinary: class names, attributes, children and textContent.
function El(tag, attrs, children) {
	var node = {
		tagName: String(tag).toUpperCase(),
		className: '',
		attributes: {},
		parentNode: null,
		children: [],
		texts: [],
		// nodes keeps strings and elements in the order they were added.
		// children and texts are what the checks read; this is what writing
		// the page back out needs, because "<b>a</b> then text" and "text then
		// <b>a</b>" are different lines on a screen and the same two lists.
		nodes: [],

		getAttribute: function(k) {
			return k === 'class' ? node.className : (node.attributes[k] ?? null);
		},
		setAttribute: function(k, v) {
			if (k === 'class') node.className = String(v);
			else node.attributes[k] = String(v);
			// Registered by id, so document.getElementById can find it. The
			// views use that to refresh single cells on a poll, and without a
			// registry here every one of those writes lands on null and the
			// refresh path cannot be tested at all — which is how the Status
			// page ended up with two writers for the same cell that disagreed.
			if (k === 'id' && global.ELEMENTS) global.ELEMENTS[String(v)] = node;
		},
		appendChild: function(c) { add(c); return c; },
		// Taking a node out again is not a luxury here: code that moves a row
		// out of a table and puts it somewhere else is exactly the code a
		// rendering check exists to watch, and a stub without removeChild
		// quietly turns "moved" into "copied". Both lists are kept in step,
		// because `nodes` is what writing the page back out reads.
		removeChild: function(c) {
			var i = node.children.indexOf(c);
			if (i >= 0) node.children.splice(i, 1);
			var j = node.nodes.indexOf(c);
			if (j >= 0) node.nodes.splice(j, 1);
			if (c && c.parentNode === node) c.parentNode = null;
			return c;
		},
		addEventListener: function() {},
		removeEventListener: function() {},
		getBoundingClientRect: function() { return { width: 600, height: 200 }; },
		getContext: function() { return CANVAS_2D; },

		// Enough of a selector engine for the simple lookups the views do: a
		// tag name or a single class, searched depth-first.
		querySelector: function(sel) {
			var want = String(sel).trim();
			var byClass = want.charAt(0) === '.';
			var needle = byClass ? want.slice(1) : want.toUpperCase();
			var found = null;
			(function search(n) {
				if (found || !n.children) return;
				n.children.forEach(function(c) {
					if (found || typeof c === 'string') return;
					var hit = byClass
						? String(c.className || '').split(/\s+/).indexOf(needle) >= 0
						: c.tagName === needle;
					if (hit) { found = c; return; }
					search(c);
				});
			})(node);
			return found;
		},

		get textContent() {
			return node.texts.join('') + node.children.map(function(c) {
				return typeof c === 'string' ? c : c.textContent;
			}).join('');
		}
	};

	function add(c) {
		if (c === null || c === undefined || c === '') return;
		if (Array.isArray(c)) return c.forEach(add);
		if (typeof c === 'string' || typeof c === 'number') {
			node.texts.push(String(c));
			node.nodes.push(String(c));
			return;
		}
		// A node belongs to one parent: appending it somewhere else moves it,
		// as it does in a browser. Code that relies on that — lifting a row
		// out of a table by appending it elsewhere — must not leave a copy
		// behind here, or the check passes on a page that is still wrong.
		if (c.parentNode && c.parentNode.removeChild)
			c.parentNode.removeChild(c);
		c.parentNode = node;
		node.children.push(c);
		node.nodes.push(c);
	}

	Object.keys(attrs || {}).forEach(function(k) {
		if (typeof attrs[k] !== 'function')
			node.setAttribute(k, attrs[k]);
	});
	add(children);
	return node;
}

function stubLuCI() {
	global.E = El;
	global._ = function(s) { return s; };
	String.prototype.format = function() {
		var a = arguments, i = 0;
		return this.replace(/%[sd]/g, function() { return String(a[i++]); });
	};
	global.L = { url: function() { return '/x'; }, bind: function(f, c) { return f.bind(c); } };
	// dom.content really replaces the children: a dialog that builds itself
	// into a detached node is otherwise never examined, and building it is
	// where the throwing happens.
	global.dom = {
		content: function(node, children) {
			if (!node || !node.children) return;
			node.children.length = 0;
			node.texts.length = 0;
			node.nodes.length = 0;
			(Array.isArray(children) ? children : [ children ]).forEach(function(c) {
				if (c !== null && c !== undefined && c !== '') node.appendChild(c);
			});
		},
		parse: function(x) { return x; }
	};
	// The poll callbacks are kept rather than dropped: a page is what it looks
	// like three seconds after it loads, not only at the moment it is built.
	global.POLLS = [];
	global.poll = {
		add: function(fn, interval) { global.POLLS.push({ fn: fn, every: interval }); }
	};
	global.ui = {
		createHandlerFn: function() { return function() {}; },
		showModal: function(title, children) {
			global.MODALS.push({ title: title, body: El('div', {}, children) });
		},
		hideModal: function() {},
		// Notifications go into a container, the way LuCI puts them into
		// #message-container, and the node is handed back — code that takes a
		// banner down again needs something to take down.
		addNotification: function(title, children, level) {
			var node = El('div', { 'class': 'alert-message ' + (level || 'info') },
				children);
			global.NOTIFICATIONS.push({ level: level || 'info', node: node });
			global.MESSAGES.appendChild(node);
			return node;
		}
	};
	global.view = { extend: function(o) { return o; } };
	global.MODALS = [];
	global.ELEMENTS = {};
	global.NOTIFICATIONS = [];
	global.MESSAGES = El('div', { 'id': 'message-container' });
	global.baseclass = { extend: function(o) { return o; } };
	global.rpc = { declare: function() { return function() { return Promise.resolve({}); }; } };
	global.document = {
		getElementById: function(id) {
			return (global.ELEMENTS && global.ELEMENTS[id]) || null;
		},
		createElement: function(t) { return El(t); },
		readyState: 'complete',
		querySelectorAll: function() { return []; },
		addEventListener: function() {}
	};
	global.window = {
		addEventListener: function() {},
		removeEventListener: function() {},
		devicePixelRatio: 2,
		setTimeout: function() { return 0; },
		clearTimeout: function() {},
		matchMedia: function() { return { matches: false, addListener: function() {} }; },
		getComputedStyle: function() {
			return { getPropertyValue: function() { return ''; } };
		}
	};
	global.requestAnimationFrame = function() { return 0; };
}

// load evaluates one of the shipped files. The `'require x as y'` lines at the
// top of a LuCI module are plain string literals here, so anything a module
// expects under one of those names has to be a global before it is loaded.
function load(rel) {
	return new Function(fs.readFileSync(path.join(BASE, rel), 'utf8'))();
}

// Elements the HTML spec closes for you. Writing <input></input> makes the
// browser open a second one, which moves everything after it.
var VOID = { AREA: 1, BASE: 1, BR: 1, COL: 1, EMBED: 1, HR: 1, IMG: 1,
	INPUT: 1, LINK: 1, META: 1, SOURCE: 1, TRACK: 1, WBR: 1 };

function escapeText(s) {
	return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
		.replace(/>/g, '&gt;');
}

function escapeAttr(s) {
	return escapeText(s).replace(/"/g, '&quot;');
}

// serialize writes a built tree back out as HTML, so a browser can lay out
// exactly what the view produced.
function serialize(node) {
	if (node === null || node === undefined) return '';
	if (typeof node === 'string' || typeof node === 'number')
		return escapeText(node);
	if (Array.isArray(node)) return node.map(serialize).join('');

	var tag = String(node.tagName || 'div').toLowerCase();
	var attrs = '';
	if (node.className) attrs += ' class="' + escapeAttr(node.className) + '"';
	Object.keys(node.attributes || {}).forEach(function(k) {
		attrs += ' ' + k + '="' + escapeAttr(node.attributes[k]) + '"';
	});

	// A control's current value is a property in a browser, not an attribute:
	// the views write `el.value = ...` and the browser shows it. Written back
	// out as HTML, that assignment would vanish, and every screenshot would
	// show a form full of first-options — which reads exactly like a dialog
	// that fails to load what it is editing. It is not: it is this file
	// forgetting. So the property is turned back into the attribute the
	// browser would have produced.
	if (node.value !== undefined && node.value !== null) {
		if (node.tagName === 'INPUT')
			attrs += ' value="' + escapeAttr(node.value) + '"';
		if (node.tagName === 'SELECT')
			(node.children || []).forEach(function(opt) {
				if (opt.tagName === 'OPTION' &&
				    String(opt.getAttribute('value')) === String(node.value))
					opt.setAttribute('selected', 'selected');
			});
	}
	if (node.checked)
		attrs += ' checked="checked"';

	if (VOID[node.tagName]) return '<' + tag + attrs + '>';

	var inner = (node.nodes && node.nodes.length)
		? node.nodes.map(serialize).join('')
		// A node built before `nodes` existed, or one whose children were
		// replaced through a path that did not keep it: fall back to the two
		// lists, text first. Order can be wrong here; nothing else can be.
		: escapeText((node.texts || []).join('')) +
			(node.children || []).map(serialize).join('');

	// A <style> body must not be escaped, or every > in a selector becomes
	// text and the rules stop applying.
	if (tag === 'style')
		inner = (node.texts || []).join('');

	return '<' + tag + attrs + '>' + inner + '</' + tag + '>';
}

module.exports = { BASE: BASE, El: El, stubLuCI: stubLuCI, load: load,
	serialize: serialize };
