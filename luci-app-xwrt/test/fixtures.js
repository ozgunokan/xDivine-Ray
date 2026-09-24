#!/usr/bin/env node
// The data the views are rendered with.
//
// One set, shared by both harnesses. render.js asks whether the pages come out
// the right shape; mobile.js asks whether they fit on a phone. They have to be
// asking about the same pages, or the second one is measuring something the
// first never checked — which is how it worked before this file existed, with
// the markup written out by hand in mobile.js from memory.
//
// The values are deliberately the widest a real page shows: a subscription
// name nobody shortened, a long hostname, a full transport string. A layout
// that fits the pleasant case and breaks on the real one is not a layout that
// was tested.

'use strict';

var STATUS = {
	connected: true, core_running: true, mode: 'mixed',
	profile_id: 'p1', profile_name: 'divine-okan', target_kind: 'profile',
	uptime_seconds: 42, stats: { uplink: 1, downlink: 2 },
	version: '0.9.1', core_version: '26.3.27', auto_connect: true
};
var CONFIG = {
	profiles: [ { id: 'p1', name: 'divine-okan', address: 'peer.example.com',
		port: 443, proto: 'vless', network: 'tcp', security: 'tls',
		flow: 'xtls-rprx-vision', source: 'manual' } ],
	groups: [], subscriptions: [], settings: { active: 'p1', mode: 'mixed' }
};
var ENV = {
	lan_devices: [ 'br-lan' ], lan_cidrs: [ '192.168.2.0/24' ],
	wan_device: 'wwan0_1', firewall: 'nftables', has_tproxy: true,
	mem_total_mb: 900, storage_total_mb: 200, storage_free_mb: 100
};

// The same page with a group connected. A group is the case the Status page
// had least to say about — it named the group and stopped — so it gets its own
// fixture rather than being left to whoever remembers it exists.
var STATUS_GROUP = {
	connected: true, core_running: true, mode: 'mixed',
	profile_id: 'g1', profile_name: 'Avrupa', target_kind: 'group',
	group_strategy: 'leastPing', group_members: 3,
	group_live: [ 'frankfurt-2' ],
	group_usage: [
		{ profile_id: 'p1', name: 'amsterdam-1', uplink: 91000, downlink: 410000, live: false },
		{ profile_id: 'p2', name: 'frankfurt-2', uplink: 2200000, downlink: 18000000, live: true },
		{ profile_id: 'p3', name: 'paris-3', uplink: 0, downlink: 0, live: false }
	],
	uptime_seconds: 1800, stats: { uplink: 2291000, downlink: 18410000 },
	version: '1.0.5', core_version: '26.3.27', auto_connect: true
};

var VIEWS = [
	{ file: 'view/xwrt/status.js', data: [ STATUS, CONFIG, ENV ] },
	{ file: 'view/xwrt/status.js', data: [ STATUS_GROUP, CONFIG, ENV ] },
	{ file: 'view/xwrt/profiles.js', data: [ CONFIG, STATUS ] },
	{ file: 'view/xwrt/json.js', data: CONFIG },
	{ file: 'view/xwrt/rules.js', data: [ { id: 'r1', name: 'Bank',
		action: 'direct', domains: [ 'bank.com' ], ips: [], sources: [],
		protocols: [], port: '443' } ] },
	{ file: 'view/xwrt/traffic.js', data: [
		{ history: [], uplink: 1, downlink: 2, samples: [], peak_up: 0, peak_down: 0 },
		{ clients: [ { ip: '192.168.2.210', hostname: 'pc', flows: 53,
			bytes_up: 2700000, bytes_down: 8000000 } ],
		  flows: [ { src: '192.168.2.210', sport: 22567, dst: '208.103.161.1',
			dport: 443, protocol: 'tcp', state: 'ESTABLISHED',
			bytes_up: 1600000, bytes_down: 4000 } ],
		  available: true, accounting: true, tracking: true,
		  total_flows: 312, lan_flows: 98,
		  // Near the limit on purpose: the warning is the part of this page
		  // that is hard to see and easy to break.
		  capacity: { count: 14200, max: 16384, percent: 86, known: true } }
	] },
	{ file: 'view/xwrt/logs.js', data: [
		{ entries: [ { time: new Date().toISOString(), level: 'error',
			source: 'xray', step: 'core', message: 'failed to start' } ] },
		{ errors: [ { time: new Date().toISOString(), level: 'error',
			source: 'xwrt', step: 'firewall',
			message: 'apply capture rules with nftables: kernel said no',
			code: 'fw.apply', args: [ 'nftables', 'kernel said no' ],
			detail: 'nft: line 3',
			hint: 'install nftables (fw4) or iptables (fw3) command-line tools',
			hint_code: 'hint.fw_install_tools' } ] }
	] },
	{ file: 'view/xwrt/about.js', data: [ STATUS ] }
];

// The dialogs, which render() never reaches. Each entry opens one and says
// what its title must contain, so an empty or half-built dialog is a failure
// rather than a silent pass.
var MODALS = [
	{ file: 'view/xwrt/profiles.js', open: function(v) {
		v.handleEditProfile(CONFIG.profiles[0]);
	}, expect: { en: 'Edit server', tr: 'Sunucuyu düzenle' } },
	{ file: 'view/xwrt/profiles.js', open: function(v) { v.handleImport(); },
	  expect: { en: 'Add config', tr: 'Config ekle' } },
	{ file: 'view/xwrt/profiles.js', open: function(v) { v.handleAddSubscription(); },
	  expect: { en: 'Add subscription', tr: 'Abonelik ekle' } },
	{ file: 'view/xwrt/profiles.js', open: function(v) {
		v.handleAddGroup(CONFIG.profiles, null);
	}, expect: { en: 'New group', tr: 'Yeni grup' } }
];

module.exports = { STATUS: STATUS, CONFIG: CONFIG, ENV: ENV,
	VIEWS: VIEWS, MODALS: MODALS };
