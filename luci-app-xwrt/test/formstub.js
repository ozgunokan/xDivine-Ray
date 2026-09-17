// A LuCI form, recorded rather than rendered.
//
// The Settings page is the one view the other harnesses cannot build: LuCI
// makes the markup, so there is no tree to walk until LuCI is there to make
// it. That exemption has cost three times now — the page shipped for several
// releases with no stylesheet at all, and then its help text ran off the right
// of a phone screen for a week, both of them found by someone looking at a
// real router rather than by anything here.
//
// So the form is stubbed twice over. `install` records what the page asks for:
// every field, its tab, its label, its help text, its choices and its default.
// `markup` turns that record into the HTML LuCI would have produced, which is
// a small, well-known shape — a `.cbi-value` per field, the label and the
// control and the description inside it — and that HTML can then be put in a
// browser and measured like any other page.
//
// It is an imitation of LuCI, not LuCI, and the difference is a real limit: a
// change in how LuCI lays a form out would not show up here. What it does
// cover is everything that depends on our own text and our own stylesheet,
// which is where all three of those defects lived.

'use strict';

function install(opts) {
	opts = opts || {};
	var record = { list: [], byName: {} };

	function Option(tab, type, name, label, descr) {
		this.tab = tab;
		this.type = String(type);
		this.name = name;
		this.label = label || '';
		this.descr = descr || '';
		this.choices = [];
		this.deps = [];
		record.list.push(this);
		record.byName[name] = this;
	}
	Option.prototype.value = function(k, v) { this.choices.push([ k, v ]); };
	Option.prototype.depends = function(k, v) { this.deps.push([ k, v ]); };

	function Section() { this.tabs = []; }
	Section.prototype.tab = function(id, title) { this.tabs.push([ id, title ]); };
	Section.prototype.taboption = function(tab, type, name, label, descr) {
		return new Option(tab, type, name, label, descr);
	};
	Section.prototype.option = function(type, name, label, descr) {
		return new Option('', type, name, label, descr);
	};

	function Map() { this.sections = []; }
	Map.prototype.section = function() {
		var s = new Section();
		this.sections.push(s);
		return s;
	};
	Map.prototype.render = function() {
		return Promise.resolve(opts.E
			? opts.E('div', { 'class': 'cbi-map' }, 'form')
			: null);
	};

	global.form = {
		Map: Map,
		NamedSection: 'NamedSection', TypedSection: 'TypedSection',
		ListValue: 'ListValue', Flag: 'Flag', Value: 'Value',
		DynamicList: 'DynamicList', DummyValue: 'DummyValue',
		Button: 'Button', TextValue: 'TextValue'
	};
	global.uci = { load: function() { return Promise.resolve(); } };

	// What the box has, as LuCI would report it — including the loopback and a
	// tunnel device, because leaving those out of the offer is part of what the
	// settings harness checks.
	var devices = opts.devices || [ 'br-lan', 'eth0', 'lo', 'wwan0_1', 'xwrt0' ];
	record.devices = devices.map(function(n) {
		return { getName: function() { return n; } };
	});
	global.network = {
		getDevices: function() { return Promise.resolve(record.devices); }
	};

	return record;
}

function esc(s) {
	return String(s == null ? '' : s)
		.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;');
}

// control is what LuCI puts in the field for each kind of option. The choices
// are the real ones, which matters: a select is as wide as its longest option,
// and "Karma — TCP redirect ile, UDP TUN uzerinden" is longer than the screen.
function control(o) {
	switch (o.type) {
	case 'ListValue':
		return '<select class="cbi-input-select">' +
			(o.choices.length ? o.choices : [ [ '', '' ] ]).map(function(c) {
				return '<option value="' + esc(c[0]) + '">' + esc(c[1]) + '</option>';
			}).join('') + '</select>';
	case 'Flag':
		return '<input type="checkbox" class="cbi-input-checkbox">';
	case 'DynamicList':
		return '<div class="cbi-dynlist"><input type="text" class="cbi-input-text"' +
			(o.placeholder ? ' placeholder="' + esc(o.placeholder) + '"' : '') +
			'></div>';
	case 'TextValue':
		return '<textarea class="cbi-input-textarea" rows="4"></textarea>';
	case 'DummyValue':
		return '<div class="cbi-value-dummy"></div>';
	default:
		return '<input type="text" class="cbi-input-text"' +
			(o.placeholder ? ' placeholder="' + esc(o.placeholder) + '"' : '') + '>';
	}
}

// markup is the shape LuCI produces: a map, a section, and one `.cbi-value`
// per field holding the label, the control and the help text. The tab strip
// the form draws above itself is the page's, and mobile.js adds that.
function markup(record) {
	var rows = record.list.map(function(o) {
		return '<div class="cbi-value" data-name="' + esc(o.name) + '">' +
			'<label class="cbi-value-title">' + esc(o.label || o.name) + '</label>' +
			'<div class="cbi-value-field">' + control(o) +
			(o.descr
				? '<div class="cbi-value-description">' + esc(o.descr) + '</div>'
				: '') +
			'</div></div>';
	}).join('');
	return '<div class="cbi-map"><h2>xDivine-Ray</h2>' +
		'<div class="cbi-section">' + rows + '</div></div>';
}

module.exports = { install: install, markup: markup };
