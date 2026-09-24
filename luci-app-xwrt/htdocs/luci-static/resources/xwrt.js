'use strict';
'require baseclass';
'require rpc';
// applyNotice puts a button in a notification, which is ui's job.
'require ui';
// Loading the catalog is what installs the translation of _(); every view
// reaches it through this file, so this one line covers all of them.
'require xwrt.i18n as i18n';

// Shared RPC bindings and formatting helpers for the xwrt views.
//
// Every call goes to the `xwrt` ubus object, which rpcd serves through the
// plugin at /usr/libexec/rpcd/xwrt. That plugin is a two-line shell script
// delegating to the xwrt binary, so the API shape is defined in exactly one
// place.

var callStatus = rpc.declare({ object: 'xwrt', method: 'status' });
var callEnv = rpc.declare({ object: 'xwrt', method: 'env' });
var callConfig = rpc.declare({ object: 'xwrt', method: 'config' });
var callPutConfig = rpc.declare({
	object: 'xwrt', method: 'put_config', params: [ 'config', 'check' ]
});

// The log call returns { entries, last_error } rather than a bare array, so
// that a view showing both does not need a second round trip. The filters are
// server-side on purpose: an unfiltered log on a busy router is thousands of
// lines, and filtering after transferring them all would be the slow way round.
var callLogs = rpc.declare({
	object: 'xwrt', method: 'logs',
	params: [ 'limit', 'level', 'source', 'step' ]
});
var callErrors = rpc.declare({
	object: 'xwrt', method: 'errors', params: [ 'limit' ]
});
var callClearError = rpc.declare({ object: 'xwrt', method: 'clear_error' });
var callClearErrors = rpc.declare({ object: 'xwrt', method: 'clear_errors' });
var callClearLog = rpc.declare({ object: 'xwrt', method: 'clear_log' });

// The self-test dials the target ten times over three paths, so it is slow by
// nature — seconds, not milliseconds. Callers show a modal for the duration.
var callSelfTest = rpc.declare({
	object: 'xwrt', method: 'selftest', params: [ 'rounds', 'target' ]
});

var callRules = rpc.declare({
	object: 'xwrt', method: 'rules', expect: { result: [] }
});
var callAddRule = rpc.declare({
	object: 'xwrt', method: 'add_rule', params: [ 'rule' ]
});
var callUpdateRule = rpc.declare({
	object: 'xwrt', method: 'update_rule', params: [ 'id', 'rule' ]
});
var callDeleteRule = rpc.declare({
	object: 'xwrt', method: 'delete_rule', params: [ 'id' ]
});
var callReplaceRules = rpc.declare({
	object: 'xwrt', method: 'replace_rules', params: [ 'rules' ]
});

// Three calls, because they cost three different things: reading what the
// daemon already knows, asking the release page again, and installing.
var callUpdate = rpc.declare({ object: 'xwrt', method: 'update' });
var callUpdateCheck = rpc.declare({ object: 'xwrt', method: 'update_check' });
var callUpdateInstall = rpc.declare({ object: 'xwrt', method: 'update_install' });

var callTraffic = rpc.declare({ object: 'xwrt', method: 'traffic' });
var callConnections = rpc.declare({
	object: 'xwrt', method: 'connections', params: [ 'limit' ]
});
var callEnableAccounting = rpc.declare({
	object: 'xwrt', method: 'enable_accounting'
});

var callGroups = rpc.declare({
	object: 'xwrt', method: 'groups', expect: { result: [] }
});
var callAddGroup = rpc.declare({
	object: 'xwrt', method: 'add_group', params: [ 'group' ]
});
var callUpdateGroup = rpc.declare({
	object: 'xwrt', method: 'update_group', params: [ 'id', 'group' ]
});
var callDeleteGroup = rpc.declare({
	object: 'xwrt', method: 'delete_group', params: [ 'id' ]
});

// One connect call serves both kinds of target: the daemon resolves the id
// against profiles and groups alike.
var callConnect = rpc.declare({
	object: 'xwrt', method: 'connect', params: [ 'id' ]
});
var callDisconnect = rpc.declare({ object: 'xwrt', method: 'disconnect' });

var callImport = rpc.declare({
	object: 'xwrt', method: 'import', params: [ 'uri', 'subscription_id' ]
});
var callDeleteProfile = rpc.declare({
	object: 'xwrt', method: 'delete_profile', params: [ 'id' ]
});
// The daemon replaces the whole profile, so callers send the complete object
// with their changes applied rather than the changed fields alone.
var callUpdateProfile = rpc.declare({
	object: 'xwrt', method: 'update_profile', params: [ 'id', 'profile' ]
});
var callPing = rpc.declare({
	object: 'xwrt', method: 'ping', params: [ 'id' ]
});
var callFetchCert = rpc.declare({
	object: 'xwrt', method: 'fetch_cert', params: [ 'id' ]
});

var callSubAdd = rpc.declare({
	object: 'xwrt', method: 'sub_add', params: [ 'url', 'name' ]
});
var callSubRefresh = rpc.declare({
	object: 'xwrt', method: 'sub_refresh', params: [ 'id' ]
});
var callSubDel = rpc.declare({
	object: 'xwrt', method: 'sub_del', params: [ 'id' ]
});


// Themes disagree about how the div-based table classes should lay out, and a
// theme that renders `.th` as a block turns a table into a stack of labels —
// which is what DivineWRT's theme does. These rules are scoped to xwrt's own
// tables and marked important on the display property alone: overriding an
// unknown third-party theme is one of the few places that is the right tool.
// Everything else (colours, borders, spacing) is left to the theme.
var TABLE_CSS = [
	// A phone is narrower than any of these tables, and a table that does not
	// fit makes the whole page scroll sideways — the menu and the headings slide
	// off with it, which is what a router's own web interface looks like when
	// nobody has opened it on a phone. Each table gets its own scroll box
	// instead: the table moves, the page stays put.
	'.xwrt-scroll { overflow-x: auto; max-width: 100%; }',
	'.xwrt-scroll > .xwrt-table { width: auto; min-width: 100%; }',
	'.xwrt-table { display: table !important; width: 100%; }',
	'.xwrt-table > .tr { display: table-row !important; }',
	'.xwrt-table > .tr > .th, .xwrt-table > .tr > .td {',
	'  display: table-cell !important; padding: .45em .6em; vertical-align: middle; }',
	'.xwrt-table > .tr.table-titles > .th {',
	'  font-weight: 600; opacity: .7; white-space: nowrap; }',
	'.xwrt-table > .tr > .td.cbi-section-actions,',
	'.xwrt-table > .tr > .th.cbi-section-actions { text-align: right; white-space: nowrap; }',
	// The buttons at the end of a row sit in a wrapper of their own; see
	// rowActions below for why. This lays them out for themes that have no
	// opinion about it.
	'.xwrt-rowactions {',
	'  display: flex; flex-wrap: nowrap; gap: .3em;',
	'  justify-content: flex-end; align-items: center; }',
	// luci-theme-bootstrap gives everything inside an actions cell
	// `flex: 1 1 4em`, which stretches five buttons to fill a cell the same
	// theme has declared to be 15% of the table: equal widths, squashed, labels
	// wrapping. Each button is as wide as its own label here.
	'.xwrt-rowactions > * { flex: 0 0 auto !important; margin: 0 !important; }',
	// The empty-table message, which xwrt.scroll lifts out of the table
	// altogether: as a row it was laid out in the first column and wrapped into
	// a ribbon of single words — `display: block` does not save it, because a
	// block child of a table is wrapped in an anonymous cell and lands in that
	// same column. Here it is an ordinary line of text under the table.
	'.xwrt-empty { padding: .7em .2em; opacity: .75; }',
	'.xwrt-empty > .td { display: block !important; width: auto !important; }',

	// Below this width a table cannot be a table: six columns of addresses and
	// buttons do not fit on a phone at any font size worth reading. Each row
	// becomes a card instead, each value carries its column's name, and the page
	// stops moving sideways altogether.
	'@media (max-width: 720px) {',
	'  .xwrt-scroll { overflow-x: visible; }',
	'  .xwrt-cards, .xwrt-cards > .tr, .xwrt-cards > .tr > .td {',
	'    display: block !important; width: auto !important; }',
	'  .xwrt-cards > .tr.table-titles { display: none !important; }',
	'  .xwrt-cards > .tr {',
	'    padding: .55em 0; border-bottom: 1px solid rgba(127,127,127,.22); }',
	'  .xwrt-cards > .tr:last-child { border-bottom: 0; }',
	'  .xwrt-cards > .tr > .td {',
	'    display: flex !important; gap: 1em; justify-content: space-between;',
	'    align-items: baseline; padding: .18em 0; text-align: left !important;',
	'    overflow-wrap: anywhere; }',
	'  .xwrt-cards > .tr > .td[data-title]::before {',
	'    content: attr(data-title); opacity: .6; font-weight: 600;',
	'    flex: 0 0 auto; white-space: nowrap; }',
	// Buttons need the full width and their own row, or they end up in a
	// column narrow enough to make each one two lines tall.
	'  .xwrt-cards > .tr > .td.cbi-section-actions {',
	'    display: flex !important; flex-wrap: wrap; gap: .4em;',
	'    justify-content: flex-start; padding-top: .5em;',
	// The rows set nowrap inline so the buttons stay on one line on a desktop.
	'    white-space: normal !important; }',
	'  .xwrt-cards > .tr > .td.cbi-section-actions > .cbi-button { margin: 0; }',
	// On a phone there is no room for five buttons in a line, so the wrapper
	// that keeps them in one on a desktop has to give way here.
	'  .xwrt-cards > .tr > .td.cbi-section-actions > .xwrt-rowactions {',
	'    flex-wrap: wrap !important; justify-content: flex-start;',
	'    width: 100%; }',

	// A control wider than the screen scrolls the page just as a table does, and
	// it only happens with real data: a profile name from a subscription, a
	// pasted URL. Rows of buttons wrap for the same reason.
	'  .cbi-map select, .cbi-map input, .cbi-map textarea { max-width: 100%; }',
	'  .cbi-map #xwrt-actions { flex-wrap: wrap; }',

	// The help text under a field, which is the longest text on the Settings
	// page and the one that ran off the right of the screen on a real phone.
	//
	// Not because it was too long — because of how a theme draws the little
	// information icon in front of it: padding on the left, `width: 100%`, and
	// no border-box sizing, so the box comes out wider than the column it is
	// in by exactly that padding. The text then wraps at an edge that is off
	// the screen, and every line looks cut off mid-word. `max-width` does not
	// help here; in content-box sizing the padding is added outside the width
	// it limits. Saying border-box does.
	//
	// Only xwrt pages load this sheet, so this is xwrt\'s own form.
	'  .cbi-map .cbi-value, .cbi-map .cbi-value-title,',
	'  .cbi-map .cbi-value-field, .cbi-map .cbi-value-description,',
	'  .cbi-map .cbi-section, .cbi-map .cbi-section-descr {',
	'    box-sizing: border-box !important; max-width: 100%;',
	// And a word with no break in it — a pasted URL in a placeholder, a long
	// device name — breaks rather than pushing the page sideways.
	'    overflow-wrap: anywhere; }',

	// The label goes above the field rather than beside it, the same as in the
	// dialogs. Most themes do this themselves at this width; the ones that keep
	// the label in a right-aligned third of the screen leave "Yönlendiricinin
	// kendi trafiğini de proxy\'le" stacked three words high next to a checkbox.
	'  .cbi-map .cbi-value { display: block !important; }',
	'  .cbi-map .cbi-value > .cbi-value-title,',
	'  .cbi-map .cbi-value > .cbi-value-field {',
	'    display: block !important; width: auto !important; float: none !important;',
	'    text-align: left !important; padding-left: 0 !important; }',
	'  .cbi-map .cbi-value > .cbi-value-title { margin-bottom: .25em; }',

	// The tab strip — the page tabs the theme draws above the content, and the
	// tabs inside the Settings form. Every theme keeps them on one line and
	// lets that line scroll, which on a phone hides half the pages behind a
	// sideways swipe nothing indicates: seven tabs, three of them visible, and
	// the ones out of sight may as well not exist. They wrap onto as many rows
	// as they need instead, so every page is one tap away and the strip is a
	// strip rather than a scrolling window. Two rows of tabs cost less than a
	// page the user never finds.
	'  .tabs, ul.tabs, .cbi-tabmenu, ul.cbi-tabmenu {',
	'    display: flex !important; flex-wrap: wrap !important;',
	'    overflow: visible !important; white-space: normal !important;',
	'    max-width: 100%; }',
	'  .tabs > li, ul.tabs > li, .cbi-tabmenu > li, ul.cbi-tabmenu > li {',
	'    float: none !important; display: block !important;',
	'    flex: 0 1 auto; max-width: 100%; }',
	'  .tabs > li > a, ul.tabs > li > a,',
	'  .cbi-tabmenu > li > a, ul.cbi-tabmenu > li > a {',
	'    padding-left: .65em !important; padding-right: .65em !important;',
	'    white-space: nowrap; }',

	// The dialogs are the other half of the page, and the label-beside-field
	// layout they inherit leaves a phone about two words of room for the field
	// itself. The label goes above it instead. Only xwrt pages load this sheet,
	// so these are xwrt\'s own dialogs.
	'  .modal { max-width: 100%; box-sizing: border-box; }',
	'  .modal .cbi-value { display: block !important; }',
	'  .modal .cbi-value > .cbi-value-title,',
	'  .modal .cbi-value > .cbi-value-field {',
	'    display: block !important; width: auto !important; float: none !important;',
	'    text-align: left !important; padding-left: 0 !important; }',
	'  .modal .cbi-value > .cbi-value-title { margin-bottom: .25em; }',
	'  .modal select, .modal input, .modal textarea {',
	'    max-width: 100%; box-sizing: border-box; }',
	'  .modal .right { display: flex; flex-wrap: wrap; gap: .4em;',
	'    justify-content: flex-end; }',
	'}'
].join('\n');

return baseclass.extend({
	status: callStatus,
	env: callEnv,
	config: callConfig,
	putConfig: callPutConfig,
	logs: callLogs,
	errors: callErrors,
	clearError: callClearError,
	clearErrors: callClearErrors,
	clearLog: callClearLog,
	selftest: callSelfTest,
	update: callUpdate,
	updateCheck: callUpdateCheck,
	updateInstall: callUpdateInstall,
	connect: callConnect,
	groups: callGroups,
	rules: callRules,
	addRule: callAddRule,
	updateRule: callUpdateRule,
	deleteRule: callDeleteRule,
	replaceRules: callReplaceRules,
	traffic: callTraffic,
	connections: callConnections,
	enableAccounting: callEnableAccounting,
	addGroup: callAddGroup,
	updateGroup: callUpdateGroup,
	deleteGroup: callDeleteGroup,
	disconnect: callDisconnect,
	importLink: callImport,
	deleteProfile: callDeleteProfile,
	updateProfile: callUpdateProfile,
	ping: callPing,
	fetchCert: callFetchCert,
	subAdd: callSubAdd,
	subRefresh: callSubRefresh,
	subDel: callSubDel,

	// style returns the stylesheet xwrt's own tables need. Every view puts it
	// in its output; a duplicate <style> is harmless and beats tracking whether
	// another view already added one.
	style: function() {
		return E('style', { 'type': 'text/css' }, TABLE_CSS);
	},

	// rowActions builds the last cell of a table row: the buttons that act on
	// that row.
	//
	// The buttons go inside a wrapper rather than straight into the cell, and
	// that wrapper is the whole reason this function exists. LuCI's own
	// generated forms put exactly one element in an actions cell, and at least
	// one theme in wide use — luci-theme-bootstrap, which is what DivineWRT
	// ships — is written against that:
	//
	//     .td.cbi-section-actions > * { display: flex; }
	//
	// It expects to be turning a single wrapper into a row of buttons. Handed
	// five buttons directly, it turns each one into a block-level flex
	// container instead, and block-level boxes stack. The Servers page came out
	// as a column of five buttons per row, every row 118 pixels tall, on that
	// theme and no other — the kind of bug that gets reported as "it looks fine
	// in the other theme" and is invisible to whoever does not use it.
	//
	// Written out at each call site, a wrapper is something the next table
	// forgets. Here it cannot be, and mobile.js measures the result against the
	// real theme rules, quoted from its stylesheet.
	rowActions: function(buttons) {
		return E('div', { 'class': 'td cbi-section-actions' },
			E('div', { 'class': 'xwrt-rowactions' }, buttons));
	},

	// scroll prepares a table for a screen narrower than it is.
	//
	// On a phone the table stops being a table: each row becomes a card and each
	// value gets its column's name in front of it, so nothing scrolls sideways
	// and nothing is cut off. That needs every cell to know its column, and
	// writing that by hand on thirty cells is a thing someone forgets the first
	// time a column is added — so it is copied here from the header row, once,
	// for whatever the table happens to contain.
	//
	// A table with no header row is a list of label/value pairs already. Those
	// are left alone: stacking them would print every label twice.
	//
	// The line that says the table is empty is taken out of the table and put
	// under it. Inside, it is a row with one cell, and a table lays that cell
	// out in the first column — three characters wide on the Rules page — so a
	// whole sentence came out as a vertical ribbon of single words.
	scroll: function(table) {
		var rows = Array.prototype.filter.call(table.children || [], function(n) {
			return /(^|\s)tr(\s|$)/.test(n.className || '');
		});
		var head = rows.filter(function(r) {
			return /(^|\s)table-titles(\s|$)/.test(r.className || '');
		})[0];

		if (head) {
			var titles = Array.prototype.map.call(head.children || [], function(c) {
				return (c.textContent || '').trim();
			});
			rows.forEach(function(row) {
				if (row === head)
					return;
				// A row that does not line up with the columns is not a
				// record: it is the line that says the table is empty, and it
				// spans the whole width. Labelling its one cell with the first
				// column's title is invisible on a desktop, where the label is
				// not drawn, and on a phone prints "NameNo servers yet." The
				// views mark these rows; the mark is honoured here.
				if (/(^|\s)placeholder(\s|$)/.test(row.className || '') ||
				    (row.children || []).length < titles.length)
					return;
				Array.prototype.forEach.call(row.children || [], function(cell, i) {
					// The actions cell holds buttons, which say what they do.
					if (/cbi-section-actions/.test(cell.className || ''))
						return;
					if (titles[i])
						cell.setAttribute('data-title', titles[i]);
				});
			});
			table.className += ' xwrt-cards';
		}

		// Out of the table, into a line of its own. The cells are moved rather
		// than copied — appending them to the new box detaches them from the
		// row — and the row itself is then dropped.
		var empty = [];
		rows.forEach(function(row) {
			if (!/(^|\s)placeholder(\s|$)/.test(row.className || ''))
				return;
			var cells = Array.prototype.slice.call(row.children || []);
			empty.push(E('div', { 'class': 'xwrt-empty' }, cells));
			table.removeChild(row);
		});

		// With the message gone the table holds its column titles and nothing
		// else, and a row of headings over an empty space reads as something
		// that failed to load. The headings go too; they come back with the
		// first record.
		if (head && empty.length && rows.length === empty.length + 1)
			table.removeChild(head);

		var box = E('div', { 'class': 'xwrt-scroll' }, table);
		return empty.length ? E('div', {}, [ box ].concat(empty)) : box;
	},

	// checked unwraps the daemon's error shape, and says so when the change it
	// just saved is not the one currently running.
	//
	// Rules, groups and servers are compiled into the core's configuration at
	// connect time. Editing one while connected changes what will happen next
	// time, not what is happening now — and nothing on screen used to say so,
	// which is how someone tests a rule for ten minutes against a core that has
	// never seen it. The notice carries the button that fixes it.
	applyNotice: function(res) {
		if (!res || !res.reconnect_required)
			return res;
		var self = this;

		// One notice, not one per edit. Three rules added in a row raised three
		// identical banners, each with its own button, and pressing the first
		// left the other two sitting there saying a change was waiting that had
		// already been applied.
		if (self._applyNote && self._applyNote.parentNode)
			return res;

		var note = ui.addNotification(null, E('div', {
			'style': 'display:flex;gap:1em;flex-wrap:wrap;align-items:center'
		}, [
			E('span', {}, _('Saved, but not applied yet: the connection is still running with the previous rules.')),
			E('button', {
				'class': 'cbi-button cbi-button-action',
				'click': ui.createHandlerFn(self, function() {
					return self.connect('').then(function(r) {
						self.checked(r);
						// And the notice goes when what it was asking for is
						// done. It used to stay until the operator closed it by
						// hand, next to a second notice saying "Applied." —
						// which reads as though something is still waiting.
						self.dismissApplyNotice();
						ui.addNotification(null, E('p', _('Applied.')), 'info');
					}).catch(function(err) {
						ui.addNotification(null, E('p',
							_('Could not apply: %s').format(err.message)), 'error');
					});
				})
			}, _('Apply now'))
		]), 'warning');
		self._applyNote = note;
		return res;
	},

	// dismissApplyNotice removes the "not applied yet" banner, if it is up.
	// The only handle LuCI gives on a notification is the node it returns from
	// addNotification, which it appends to the message container itself.
	dismissApplyNotice: function() {
		var n = this._applyNote;
		this._applyNote = null;
		try {
			if (n && n.parentNode)
				n.parentNode.removeChild(n);
		} catch (e) { /* the banner is cosmetic; never break the page over it */ }
	},

	// checked unwraps the daemon's error shape. The RPC layer returns an
	// object with an "error" key rather than failing the call, so that LuCI
	// can show the daemon's own message instead of a generic ubus failure.
	checked: function(res) {
		if (res && res.error)
			throw new Error(res.error);
		// Every mutating call comes through here, and only mutating calls carry
		// the flag, so this is the one place the notice has to be raised.
		return this.applyNotice(res);
	},

	// modeName writes a capture mode the way the Settings page writes it.
	//
	// The daemon stores the identifier — `mixed`, `tproxy` — and printing that
	// raw put a lower-case word in a table of proper nouns. Capitalising the
	// first letter would have been worse than leaving it: "Tproxy" and "Tun"
	// are not how either is spelled anywhere else in the interface, or
	// anywhere else at all. So the four are named here, once.
	modeName: function(mode) {
		switch (String(mode || '')) {
		case 'redirect': return 'Redirect';
		case 'mixed':    return 'Mixed';
		case 'tproxy':   return 'TPROXY';
		case 'tun':      return 'TUN';
		// An empty mode is not an unknown mode: nothing has connected yet.
		case '':         return '-';
		// And a mode this build does not know is shown as it came, because the
		// daemon is the authority on what it is running.
		default:         return String(mode);
		}
	},

	formatBytes: function(n) {
		n = Number(n) || 0;
		var units = [ 'B', 'KiB', 'MiB', 'GiB', 'TiB' ];
		var i = 0;
		while (n >= 1024 && i < units.length - 1) {
			n /= 1024;
			i++;
		}
		return (i === 0 ? n : n.toFixed(1)) + ' ' + units[i];
	},

	formatRate: function(n) {
		return this.formatBytes(n) + '/s';
	},

	formatUptime: function(seconds) {
		seconds = Number(seconds) || 0;
		var d = Math.floor(seconds / 86400);
		var h = Math.floor((seconds % 86400) / 3600);
		var m = Math.floor((seconds % 3600) / 60);
		var s = Math.floor(seconds % 60);
		if (d > 0)
			return d + 'd ' + h + 'h ' + m + 'm';
		if (h > 0)
			return h + 'h ' + m + 'm ' + s + 's';
		if (m > 0)
			return m + 'm ' + s + 's';
		return s + 's';
	},

	// profileLabel mirrors the daemon's own naming so the UI and the logs
	// refer to a server the same way.
	profileLabel: function(p) {
		if (!p)
			return '';
		return p.name || (p.address + ':' + p.port);
	},

	// strategyLabel explains a group's behaviour in the terms that matter:
	// whether it fails over or merely spreads load.
	strategyLabel: function(st) {
		switch (st) {
		case 'leastPing':   return _('fastest server, with failover');
		case 'leastLoad':   return _('steadiest server, with failover');
		case 'random':      return _('random, no failover');
		case 'roundRobin':  return _('in turn, no failover');
		default:            return st || '';
		}
	},

	// verdictText turns the self-test's verdict into a sentence. The daemon
	// sends a code plus the numbers rather than finished prose, so the wording
	// lives here, with the rest of the interface's language.
	verdictText: function(r) {
		if (!r)
			return '';
		var tun = r.tunnel || {};
		var dir = r.direct || {};
		var overhead = Math.round((tun.median_ms || 0) - (dir.median_ms || 0));

		switch (r.verdict_code) {
		case 'unreachable':
			return _('The tunnel could not be measured: the core is not answering on its SOCKS port.');
		case 'no-baseline':
			return _('Only the tunnel could be measured; the direct connection failed, so there is nothing to compare it with.');
		case 'failures':
			return _('Of the %d connections tried through the tunnel, %d failed outright.')
				.format(tun.attempts || 0, tun.failures || 0);
		case 'loss-beyond':
			return _('Usually %d ms, but some connections reach %d ms. The path to the server is clean, so packets are being lost beyond it — the trouble is on the server\'s own network, not on this device.')
				.format(Math.round(tun.median_ms || 0), Math.round(tun.max_ms || 0));
		case 'loss':
			return _('Usually %d ms, but some connections reach %d ms: packets are being lost and sent again.')
				.format(Math.round(tun.median_ms || 0), Math.round(tun.max_ms || 0));
		case 'overhead':
			return _('The tunnel adds %d ms to every connection, which is a lot: the server is probably far away or busy.')
				.format(overhead);
		case 'healthy':
			return _('Healthy: the tunnel adds %d ms and behaves consistently.').format(overhead);
		// The two tunnel-only readings. They deliberately say nothing about
		// overhead: with the router proxied there is no unproxied number to
		// subtract, and inventing one is how a diagnostic starts lying.
		case 'tunnel-only-steady':
			return _('Through the tunnel: %d ms, and steady. There is nothing to compare it with, because the router\'s own traffic goes through the tunnel too.')
				.format(Math.round(tun.median_ms || 0));
		case 'tunnel-only-jitter':
			return _('Through the tunnel: usually %d ms, reaching %d ms. There is nothing to compare it with, because the router\'s own traffic goes through the tunnel too.')
				.format(Math.round(tun.median_ms || 0), Math.round(tun.max_ms || 0));
		// Failures outrank both of those: a median is a statement about the
		// connections that worked, and saying "usually 186 ms" about a run
		// where one in ten never completed is a true number doing the work of
		// a false impression.
		case 'tunnel-only-failures':
			return _('Of the %d connections tried through the tunnel, %d failed outright. The %d ms is the timing of the ones that worked.')
				.format(tun.attempts || 0, tun.failures || 0, Math.round(tun.median_ms || 0));
		default:
			return r.verdict || '';
		}
	},

	// failureMessage and failureHint render a recorded failure in the reader's
	// language. Both take the whole entry — the status banner and the journal
	// send the same shape — and both fall back to the daemon's English, which
	// is always present, so an entry this build has no translation for is
	// still readable rather than blank.
	failureMessage: function(e) {
		if (!e) return '';
		return i18n.failureText(e.code, e.args, e.message || '');
	},

	failureHint: function(e) {
		if (!e) return '';
		return i18n.failureText(e.hint_code, e.hint_args, e.hint || '');
	},

	// stepLabel names the stage of the connect sequence a message came from.
	// This is the single most useful thing in an error: "permission denied"
	// means a different fix in each of these stages.
	stepLabel: function(step) {
		switch (step) {
		case 'config':    return _('configuration');
		case 'core':      return _('proxy core');
		case 'tunnel':    return _('TUN device');
		case 'firewall':  return _('firewall rules');
		case 'dns':       return _('DNS');
		case 'detect':    return _('network detection');
		case 'teardown':  return _('disconnect');
		case 'subscribe': return _('subscription');
		default:          return step || '';
		}
	},

	sourceLabel: function(source) {
		switch (source) {
		case 'xwrt':   return _('service');
		case 'xray':   return _('core');
		case 'tunnel': return _('tunnel');
		default:       return source || '';
		}
	},

	levelColor: function(level) {
		switch (level) {
		case 'error':   return '#f44336';
		case 'warning': return '#e08800';
		case 'debug':   return '#9e9e9e';
		default:        return 'inherit';
		}
	},

	// localTime renders the daemon's RFC 3339 timestamps as a clock time.
	// Absolute dates are noise in a log covering the last few minutes.
	localTime: function(ts) {
		var d = new Date(ts);
		if (isNaN(d.getTime()))
			return ts || '';
		return d.toLocaleTimeString();
	},

	localDateTime: function(ts) {
		var d = new Date(ts);
		if (isNaN(d.getTime()))
			return ts || '';
		return d.toLocaleString();
	},

	// actionLabel says what a rule does in the words the user thinks in.
	actionLabel: function(a) {
		switch (a) {
		case 'direct': return _('bypass the VPN');
		case 'proxy':  return _('force through the VPN');
		case 'block':  return _('block');
		default:       return a || '';
		}
	},

	// ruleSummary renders the matchers compactly; a rule with five domains
	// should not push the action column off the screen.
	ruleSummary: function(r) {
		var parts = [];
		function add(label, list) {
			if (!list || !list.length)
				return;
			var shown = list.slice(0, 3).join(', ');
			if (list.length > 3)
				shown += ', +' + (list.length - 3);
			parts.push(label + ' ' + shown);
		}
		add(_('domains'), r.domains);
		add(_('addresses'), r.ips);
		add(_('clients'), r.sources);
		add(_('protocols'), r.protocols);
		if (r.port)
			parts.push(_('port') + ' ' + r.port);
		if (r.source_port)
			parts.push(_('source port') + ' ' + r.source_port);
		if (r.network)
			parts.push(r.network);
		return parts.join(' · ');
	},

	transportLabel: function(p) {
		if (!p)
			return '';
		var parts = [ p.proto, p.network || 'tcp' ];
		if (p.security && p.security !== 'none')
			parts.push(p.security);
		if (p.flow)
			parts.push(p.flow);
		return parts.join(' / ');
	}
});
