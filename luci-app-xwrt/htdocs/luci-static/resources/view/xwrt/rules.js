'use strict';
'require view';
'require ui';
'require dom';
'require xwrt';

// The exception list: which traffic stays off the VPN, which is forced through
// it, and which is dropped.
//
// Order is part of the meaning — the core takes the first rule that matches —
// so the list is presented in match order and can be reordered, and moving a
// rule saves the whole list rather than one entry.

function textarea(value, placeholder, rows) {
	return E('textarea', {
		'class': 'cbi-input-textarea',
		'rows': rows || 3,
		'style': 'width:100%',
		'placeholder': placeholder
	}, value || '');
}

function lines(el) {
	return (el.value || '').split('\n')
		.map(function(s) { return s.trim(); })
		.filter(function(s) { return s.length > 0; });
}

return view.extend({
	load: function() {
		return xwrt.rules();
	},

	reload: function() {
		return this.load().then(L.bind(function(rules) {
			var body = document.getElementById('xwrt-rules');
			if (body)
				dom.content(body, this.renderTable(rules || []));
			this.rules = rules || [];
		}, this));
	},

	handleEdit: function(existing) {
		var self = this;

		var name = E('input', { 'class': 'cbi-input-text', 'style': 'width:100%',
			'value': existing ? (existing.name || '') : '',
			'placeholder': _('e.g. Bank') });

		var action = E('select', { 'class': 'cbi-input-select', 'style': 'width:100%' }, [
			E('option', { 'value': 'direct' }, _('Bypass the VPN — go out over the normal connection')),
			E('option', { 'value': 'proxy' }, _('force through the VPN')),
			E('option', { 'value': 'block' }, _('Block — drop the traffic'))
		]);
		if (existing && existing.action)
			action.value = existing.action;

		var domains = textarea((existing && existing.domains || []).join('\n'),
			'bank.com\ndomain:gov.tr\nfull:www.example.com\nkeyword:intranet', 4);
		var ips = textarea((existing && existing.ips || []).join('\n'),
			'203.0.113.0/24\n198.51.100.7', 2);
		var sources = textarea((existing && existing.sources || []).join('\n'),
			'192.168.1.50\n192.168.1.0/24', 2);
		var protocols = textarea((existing && existing.protocols || []).join('\n'),
			'tls\nbittorrent', 2);

		var port = E('input', { 'class': 'cbi-input-text', 'style': 'width:100%',
			'value': existing ? (existing.port || '') : '',
			'placeholder': '443 ya da 80,443 ya da 8000-9000' });
		var network = E('select', { 'class': 'cbi-input-select' }, [
			E('option', { 'value': '' }, _('any')),
			E('option', { 'value': 'tcp' }, 'tcp'),
			E('option', { 'value': 'udp' }, 'udp'),
			E('option', { 'value': 'tcp,udp' }, 'tcp,udp')
		]);
		if (existing && existing.network)
			network.value = existing.network;

		function field(label, control, hint) {
			return E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, label),
				E('div', { 'class': 'cbi-value-field' }, hint
					? [ control, E('div', { 'class': 'cbi-value-description' }, hint) ]
					: [ control ])
			]);
		}

		ui.showModal(existing ? _('Edit rule') : _('New rule'), [
			field(_('Name'), name),
			field(_('Action'), action,
				_('A bypass rule also has the router resolve these names itself, so the site is reached over the normal connection rather than at an address that only exists at the far end of the tunnel.')),
			field(_('Domains'), domains,
				_('One per line. A bare name matches that name and its subdomains, and nothing else. Prefixes: full: (that name alone), keyword: (anywhere in the name), regexp:, geosite: (needs the geo data package).')),
			field(_('Addresses'), ips, _('One network or address per line.')),
			field(_('Clients'), sources,
				_('Apply the rule only to these LAN clients. Leave empty for all of them.')),
			field(_('Ports'), port),
			field(_('Protocols'), protocols,
				_('One per line: http, tls, quic or bittorrent. Detected from the first packet of the connection.')),
			field(_('Network'), network),

			E('div', { 'class': 'right', 'style': 'margin-top:1em' }, [
				E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')),
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-positive',
					'click': ui.createHandlerFn(self, function() {
						var rule = {
							name: name.value || '',
							action: action.value,
							enabled: existing ? (existing.enabled !== false) : true,
							domains: lines(domains),
							ips: lines(ips),
							sources: lines(sources),
							protocols: lines(protocols),
							port: (port.value || '').trim(),
							network: network.value
						};
						var req = existing
							? xwrt.updateRule(existing.id, rule)
							: xwrt.addRule(rule);
						return req.then(function(res) {
							ui.hideModal();
							xwrt.checked(res);
							return self.reload();
						}).catch(function(err) {
							// The daemon validates and explains; showing its own
							// message beats a generic failure.
							ui.addNotification(null,
								E('p', _('The rule was not accepted: %s').format(err.message)), 'error');
						});
					})
				}, existing ? _('Save') : _('Create'))
			])
		]);
	},

	handleToggle: function(rule) {
		var self = this;
		var copy = Object.assign({}, rule, { enabled: rule.enabled === false });
		return xwrt.updateRule(rule.id, copy).then(function(res) {
			xwrt.checked(res);
			return self.reload();
		}).catch(function(err) {
			ui.addNotification(null, E('p', err.message), 'error');
		});
	},

	handleDelete: function(rule) {
		var self = this;
		if (!confirm(_('Delete the rule "%s"?').format(rule.name || rule.id)))
			return;
		return xwrt.deleteRule(rule.id).then(function(res) {
			xwrt.checked(res);
			return self.reload();
		}).catch(function(err) {
			ui.addNotification(null, E('p', err.message), 'error');
		});
	},

	handleMove: function(index, delta) {
		var self = this;
		var list = (this.rules || []).slice();
		var to = index + delta;
		if (to < 0 || to >= list.length)
			return;
		var tmp = list[index];
		list[index] = list[to];
		list[to] = tmp;

		return xwrt.replaceRules(list).then(function(res) {
			xwrt.checked(res);
			return self.reload();
		}).catch(function(err) {
			ui.addNotification(null, E('p', err.message), 'error');
		});
	},

	renderTable: function(rules) {
		var self = this;
		this.rules = rules;

		var rows = rules.map(function(r, i) {
			var off = r.enabled === false;
			return E('div', { 'class': 'tr', 'style': off ? 'opacity:.5' : '' }, [
				E('div', { 'class': 'td', 'style': 'width:3em' }, String(i + 1)),
				E('div', { 'class': 'td' }, r.name || r.id),
				E('div', { 'class': 'td' }, xwrt.actionLabel(r.action)),
				E('div', { 'class': 'td', 'style': 'word-break:break-word' },
					xwrt.ruleSummary(r) || E('em', {}, _('matches nothing'))),
				xwrt.rowActions([
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'title': _('Move up'),
						'disabled': i === 0 ? '' : null,
						'click': ui.createHandlerFn(self, function() {
							return self.handleMove(i, -1);
						})
					}, '↑'),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'title': _('Move down'),
						'disabled': i === rules.length - 1 ? '' : null,
						'click': ui.createHandlerFn(self, function() {
							return self.handleMove(i, 1);
						})
					}, '↓'),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							return self.handleToggle(r);
						})
					}, off ? _('Enable') : _('Disable')),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-action',
						'click': ui.createHandlerFn(self, function() {
							return self.handleEdit(r);
						})
					}, _('Edit')),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'click': ui.createHandlerFn(self, function() {
							return self.handleDelete(r);
						})
					}, _('Delete'))
				])
			]);
		});

		if (!rows.length)
			rows = [ E('div', { 'class': 'tr placeholder' }, [
				E('div', { 'class': 'td' }, E('em', {},
					_('No rules. Everything outside the local network goes through the VPN.')))
			]) ];

		return xwrt.scroll(E('div', { 'class': 'table cbi-section-table xwrt-table' }, [
			E('div', { 'class': 'tr table-titles' }, [
				E('div', { 'class': 'th', 'style': 'width:3em' }, '#'),
				E('div', { 'class': 'th' }, _('Name')),
				E('div', { 'class': 'th' }, _('Action')),
				E('div', { 'class': 'th' }, _('Matches')),
				E('div', { 'class': 'th cbi-section-actions' }, '')
			])
		].concat(rows)));
	},

	render: function(rules) {
		return E('div', { 'class': 'cbi-map' }, [
			xwrt.style(),
			E('h2', {}, _('Routing rules')),
			E('div', { 'class': 'cbi-map-descr' },
				_('Exceptions to the default of sending everything through the VPN. Rules are tried in order and the first match wins, so put the narrow ones above the broad ones.')),
			E('div', { 'style': 'margin-bottom:1em' }, [
				E('button', {
					'class': 'cbi-button cbi-button-add',
					'click': ui.createHandlerFn(this, function() {
						return this.handleEdit(null);
					})
				}, _('New rule'))
			]),
			E('div', { 'id': 'xwrt-rules' }, this.renderTable(rules || []))
		]);
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
