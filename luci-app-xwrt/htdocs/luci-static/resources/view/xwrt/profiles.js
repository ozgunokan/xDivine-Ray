'use strict';
'require view';
'require ui';
'require dom';
'require xwrt';

// Server list: import share links, connect, test reachability, and manage
// subscriptions.

// PIN_BOX styles the one value the dialog exists to show. Both colours are set
// explicitly: a theme variable for the background left the text white on white
// in DivineWRT's dark theme, and a translucent grey works on any background
// while the text keeps whatever colour the theme already reads well in.
var PIN_BOX = 'font-family:monospace;font-size:12px;word-break:break-all;' +
	'background:rgba(127,127,127,.18);color:inherit;padding:.6em;border-radius:3px;' +
	'user-select:all';

// The fields a server can be edited through, and when each one is worth
// showing. A share link carries a dozen parameters and most profiles use a
// handful, so the form follows the profile: the fields its protocol,
// transport and security actually use, plus any field that already holds a
// value — nothing a link brought in can be hidden by this form and then lost
// on save.
//
// allow_insecure is deliberately absent. It means "accept any certificate",
// newer cores have removed it, and the certificate button on each row does the
// same job by pinning the one certificate the server really presents.
var FIELDS = [
	{ key: 'name',         label: function() { return _('Name'); }, always: true },
	{ key: 'address',      label: function() { return _('Address'); }, always: true },
	{ key: 'port',         label: function() { return _('Port'); }, always: true, number: true },

	{ key: 'uuid',         label: function() { return _('UUID'); }, protos: [ 'vless', 'vmess' ] },
	{ key: 'password',     label: function() { return _('Password'); }, protos: [ 'trojan', 'shadowsocks' ] },
	{ key: 'method',       label: function() { return _('Encryption method'); }, protos: [ 'shadowsocks' ] },
	// Three values, two of them easy to mistype into a connection that fails
	// with no explanation, so they are picked rather than typed.
	{ key: 'flow',         label: function() { return _('Flow'); }, protos: [ 'vless' ],
	  options: [ '', 'xtls-rprx-vision', 'xtls-rprx-vision-udp443' ], empty: true },

	{ key: 'network',      label: function() { return _('Transport'); }, always: true, reload: true,
	  options: [ 'tcp', 'ws', 'grpc', 'h2', 'xhttp', 'kcp', 'quic' ] },
	{ key: 'security',     label: function() { return _('Security'); }, always: true, reload: true,
	  options: [ 'none', 'tls', 'reality' ] },

	{ key: 'sni',          label: function() { return _('SNI (server name)'); }, securities: [ 'tls', 'reality' ] },
	// ALPN is a list, in preference order, and the three values in use are the
	// only ones worth offering. h3 is kept because links carry it, but the
	// hint says what happens to it.
	{ key: 'alpn',         label: function() { return 'ALPN'; }, securities: [ 'tls' ],
	  multi: [ 'h2', 'http/1.1', 'h3' ],
	  hint: function() { return _('h3 belongs to QUIC and is dropped on any other transport.'); } },
	// The uTLS fingerprint: what the handshake is made to look like.
	{ key: 'fingerprint',  label: function() { return _('TLS fingerprint (uTLS)'); },
	  securities: [ 'tls', 'reality' ], empty: true,
	  options: [ '', 'chrome', 'firefox', 'safari', 'ios', 'android', 'edge',
		'360', 'qq', 'random', 'randomized' ] },
	{ key: 'public_key',   label: function() { return _('REALITY public key'); }, securities: [ 'reality' ] },
	{ key: 'short_id',     label: function() { return _('REALITY short ID'); }, securities: [ 'reality' ] },
	{ key: 'spider_x',     label: function() { return 'SpiderX'; }, securities: [ 'reality' ] },

	{ key: 'path',         label: function() { return _('Path'); }, networks: [ 'ws', 'h2', 'xhttp' ] },
	{ key: 'host',         label: function() { return _('Host header'); }, networks: [ 'ws', 'h2', 'xhttp' ] },
	{ key: 'service_name', label: function() { return _('gRPC service name'); }, networks: [ 'grpc' ] },
	{ key: 'header_type',  label: function() { return _('Header camouflage'); }, networks: [ 'tcp', 'kcp' ] },
	{ key: 'seed',         label: function() { return _('mKCP seed'); }, networks: [ 'kcp' ] },

	{ key: 'remark',       label: function() { return _('Note'); }, always: true }
];

function shows(f, p) {
	if (f.always)
		return true;
	// A value that is already set is always shown, whatever the protocol says:
	// hiding it would make the save silently drop it.
	if (p[f.key] !== undefined && p[f.key] !== null && p[f.key] !== '')
		return true;
	if (f.protos && f.protos.indexOf(p.proto) >= 0)
		return true;
	if (f.networks && f.networks.indexOf(p.network || 'tcp') >= 0)
		return true;
	if (f.securities && f.securities.indexOf(p.security || 'none') >= 0)
		return true;
	return false;
}

// shortIssuer keeps the part of an X.509 name a person reads — the common name
// — instead of the full comma-separated distinguished name.
function shortIssuer(dn) {
	if (!dn)
		return '';
	var m = /CN=([^,]+)/.exec(dn);
	return m ? m[1] : dn;
}

return view.extend({
	load: function() {
		return Promise.all([ xwrt.config(), xwrt.status() ]);
	},

	// Editing a server that is already in the list. Until now a typo in an
	// address meant deleting the server and pasting the config again, which
	// also threw away its pinned certificate and its place in any group.
	handleEditProfile: function(p) {
		var self = this;
		var draft = {};
		Object.keys(p).forEach(function(k) { draft[k] = p[k]; });

		var body = E('div', {});

		// A select that never loses what it was given. A share link can carry a
		// transport or a fingerprint this list has never heard of; dropping it
		// into a list of known values would quietly replace it on save, so an
		// unknown value joins the list instead.
		function select(f, value) {
			var opts = f.options.slice();
			if (value && opts.indexOf(value) < 0)
				opts.push(value);

			var el = E('select', { 'class': 'cbi-input-select', 'style': 'width:100%' },
				opts.map(function(o) {
					return E('option', { 'value': o }, o === '' ? _('None') : o);
				}));
			el.value = value || opts[0];
			return el;
		}

		// ALPN and nothing else so far: a list of values, stored as one comma
		// separated string, edited as boxes to tick. Order is preference order,
		// so the boxes are rebuilt in the order this list declares.
		function multi(f, value) {
			var have = String(value || '').split(',')
				.map(function(s) { return s.trim(); })
				.filter(function(s) { return s !== ''; });
			var known = f.multi.slice();
			have.forEach(function(v) { if (known.indexOf(v) < 0) known.push(v); });

			var boxes = known.map(function(v) {
				var cb = E('input', { 'type': 'checkbox' });
				cb.checked = have.indexOf(v) >= 0;
				cb.addEventListener('change', function() {
					// Read every box back in declaration order, so the stored
					// order is the preference order rather than click order.
					var picked = [];
					known.forEach(function(k, i) {
						if (boxes[i].checked)
							picked.push(k);
					});
					draft[f.key] = picked.join(',');
				});
				return cb;
			});

			return E('div', { 'style': 'display:flex;flex-wrap:wrap;gap:.9em' },
				known.map(function(v, i) {
					return E('label', {
						'style': 'display:flex;align-items:center;gap:.35em'
					}, [ boxes[i], E('span', {}, v) ]);
				}));
		}

		function row(f) {
			var value = draft[f.key];
			var input;

			if (f.multi) {
				input = multi(f, value);
			} else if (f.options) {
				input = select(f, value === undefined || value === null ? '' : String(value));
				input.addEventListener('change', function() {
					draft[f.key] = input.value;
					// Changing the transport or the security changes which
					// fields matter, so the form is rebuilt around the answer.
					if (f.reload)
						fill();
				});
			} else {
				input = E('input', {
					'class': 'cbi-input-text', 'style': 'width:100%',
					'value': value === undefined || value === null ? '' : String(value)
				});
				var write = function() {
					draft[f.key] = f.number ? parseInt(input.value, 10) || 0 : input.value;
				};
				input.addEventListener('change', write);
				input.addEventListener('input', write);
			}

			var field = [ input ];
			if (f.hint)
				field.push(E('div', {
					'style': 'opacity:.7;font-size:90%;margin-top:.25em'
				}, f.hint()));

			return E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, f.label()),
				E('div', { 'class': 'cbi-value-field' }, field)
			]);
		}

		function fill() {
			var parts = [];

			if (draft.subscription)
				parts.push(E('div', { 'class': 'alert-message warning' },
					_('This server came from a subscription. Refreshing that subscription will overwrite your changes.')));

			parts.push(E('div', { 'style': 'opacity:.75;font-size:92%;margin-bottom:.6em' },
				_('Protocol: %s. Changing it is not offered — paste a new config instead.')
					.format(draft.proto || '-')));

			FIELDS.forEach(function(f) {
				if (shows(f, draft))
					parts.push(row(f));
			});

			parts.push(E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Multiplexing (mux)')),
				E('div', { 'class': 'cbi-value-field' },
					E('label', { 'style': 'display:flex;align-items:center;gap:.4em' }, [
						(function() {
							var cb = E('input', { 'type': 'checkbox' });
							cb.checked = !!draft.mux;
							cb.addEventListener('change', function() { draft.mux = !!cb.checked; });
							return cb;
						})(),
						E('span', { 'style': 'opacity:.75;font-size:92%' },
							_('Carries several TCP connections over one. Usually slower; UDP does not need it.'))
					]))
			]));

			parts.push(E('div', { 'class': 'right', 'style': 'margin-top:1em' }, [
				E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')),
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-positive',
					'click': ui.createHandlerFn(self, function() {
						if (!String(draft.name || '').trim() || !String(draft.address || '').trim()) {
							ui.addNotification(null, E('p',
								_('A name and an address are required.')), 'warning');
							return;
						}
						if (!(draft.port > 0 && draft.port < 65536)) {
							ui.addNotification(null, E('p',
								_('The port has to be between 1 and 65535.')), 'warning');
							return;
						}
						return xwrt.updateProfile(p.id, draft).then(function(res) {
							ui.hideModal();
							xwrt.checked(res);
							ui.addNotification(null, E('p',
								_('Saved.')), 'info');
							return self.reload();
						}).catch(function(err) {
							ui.addNotification(null, E('p',
								_('Could not save: %s').format(err.message)), 'error');
						});
					})
				}, _('Save'))
			]));

			dom.content(body, parts);
		}

		fill();
		ui.showModal(_('Edit server'), [ body ]);
	},

	handleAddGroup: function(profiles, existing) {
		var self = this;
		var name = E('input', { 'class': 'cbi-input-text', 'style': 'width:100%',
			'value': existing ? (existing.name || '') : '' });

		var strategy = E('select', { 'class': 'cbi-input-select', 'style': 'width:100%' }, [
			E('option', { 'value': 'leastPing' }, _('Lowest ping — fastest server, with failover')),
			E('option', { 'value': 'leastLoad' }, _('Least loaded — steadiest server, with failover')),
			E('option', { 'value': 'random' }, _('Random — spreads the load, no failover')),
			E('option', { 'value': 'roundRobin' }, _('In turn — uses the servers one after another, no failover'))
		]);
		if (existing && existing.strategy)
			strategy.value = existing.strategy;

		// How often each member is checked, which is also the worst case for
		// noticing that the one in use has died: the balancer chooses from the
		// last measurement, so a dead server keeps being used until the next
		// probe. Minutes here are minutes without internet.
		var probe = E('select', { 'class': 'cbi-input-select', 'style': 'width:100%' }, [
			E('option', { 'value': '30s' }, _('every 30 seconds')),
			E('option', { 'value': '60s' }, _('every minute (recommended)')),
			E('option', { 'value': '2m' }, _('every 2 minutes')),
			E('option', { 'value': '3m' }, _('every 3 minutes')),
			E('option', { 'value': '5m' }, _('every 5 minutes'))
		]);
		probe.value = (existing && existing.probe_interval) || '60s';

		// Where the check is sent.
		//
		// The request does not leave through the device's normal route: the
		// core hands it to the member's own outbound, so it travels through
		// that server and the answer's round trip is what the ranking is made
		// of. A router with no way out except the tunnel can still run it.
		//
		// Which address is used barely changes the number, because all of
		// these are answered by whichever edge is nearest the *server*, so
		// what is being measured is the path to the server either way. It
		// matters when one of them is unreachable from a particular exit — an
		// endpoint that never answers makes every member look equally dead and
		// the ranking becomes noise.
		var probeURLs = [
			'https://www.gstatic.com/generate_204',
			'https://cp.cloudflare.com/generate_204',
			'http://cp.cloudflare.com/generate_204',
			'https://connectivitycheck.gstatic.com/generate_204'
		];
		var probeURL = E('input', {
			'class': 'cbi-input-text', 'style': 'width:100%',
			'type': 'text', 'list': 'xwrt-probe-urls',
			'placeholder': 'https://www.gstatic.com/generate_204',
			'value': (existing && existing.probe_url) || ''
		});
		var probeList = E('datalist', { 'id': 'xwrt-probe-urls' },
			probeURLs.map(function(u) { return E('option', { 'value': u }); }));

		var chosen = (existing && existing.members) ? existing.members.slice() : [];
		var list = E('div', {
			'style': 'max-height:16em;overflow:auto;border:1px solid var(--border-color-medium,#ccc);' +
				'padding:.4em;border-radius:4px'
		}, profiles.length
			? profiles.map(function(p) {
				return E('label', { 'style': 'display:block;padding:.2em 0' }, [
					E('input', {
						'type': 'checkbox',
						'value': p.id,
						'checked': chosen.indexOf(p.id) >= 0 ? '' : null
					}),
					' ',
					xwrt.profileLabel(p),
					E('span', { 'style': 'color:#9e9e9e' },
						'  ' + p.address + ':' + p.port)
				]);
			})
			: [ E('em', {}, _('Add a few servers first.')) ]);

		ui.showModal(existing ? _('Edit group') : _('New group'), [
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Name')),
				E('div', { 'class': 'cbi-value-field' }, name)
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Strategy')),
				E('div', { 'class': 'cbi-value-field' }, [
					strategy,
					E('div', { 'class': 'cbi-value-description' },
						_('Only the failover strategies notice that a server has gone down. Random and in-turn keep sending connections to a dead one.'))
				])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Health check')),
				E('div', { 'class': 'cbi-value-field' }, [
					probe,
					E('div', { 'class': 'cbi-value-description' },
						_('Each member is asked for a page with an empty body. This interval is also how long a server that has died keeps being used, so it is the worst case for how long you are offline. It costs nothing worth counting; the failover strategies are the only ones that use it.'))
				])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Health check address')),
				E('div', { 'class': 'cbi-value-field' }, [
					probeURL,
					probeList,
					E('div', { 'class': 'cbi-value-description' },
						_('The check is sent through each member\'s own server, not out of the router directly, so a device with no internet except the tunnel can still run it. Leave empty for the default. Pick something that answers from everywhere your servers are: one that does not answer makes every member look equally dead.'))
				])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Members')),
				E('div', { 'class': 'cbi-value-field' }, list)
			]),
			E('div', { 'class': 'right', 'style': 'margin-top:1em' }, [
				E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')),
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-positive',
					'click': ui.createHandlerFn(self, function() {
						var members = [];
						list.querySelectorAll('input[type=checkbox]').forEach(function(cb) {
							if (cb.checked)
								members.push(cb.value);
						});
						if (!members.length) {
							ui.addNotification(null,
								E('p', _('Select at least one server.')), 'warning');
							return;
						}
						// An address the core cannot parse is refused here
						// rather than saved: the core rejects the whole
						// configuration over it, and the failure surfaces as
						// "the tunnel will not start" with nothing pointing
						// back at this field.
						var url = String(probeURL.value || '').trim();
						if (url && !/^https?:\/\/[^\/\s]+/.test(url)) {
							ui.addNotification(null, E('p',
								_('The health check address has to start with http:// or https:// and name a host, like %s.')
									.format('https://cp.cloudflare.com/generate_204')), 'warning');
							return;
						}
						var group = {
							name: name.value || _('Group'),
							strategy: strategy.value,
							members: members,
							probe_interval: probe.value,
							probe_url: url
						};
						var req = existing
							? xwrt.updateGroup(existing.id, group)
							: xwrt.addGroup(group);
						return req.then(function(res) {
							ui.hideModal();
							xwrt.checked(res);
							return self.reload();
						}).catch(function(err) {
							ui.hideModal();
							ui.addNotification(null,
								E('p', _('The group could not be saved: %s').format(err.message)), 'error');
						});
					})
				}, existing ? _('Save') : _('Create'))
			])
		]);
	},

	handleGroupDelete: function(id, name) {
		var self = this;
		if (!confirm(_('Delete the group "%s"? The servers in it are kept.').format(name)))
			return;
		return xwrt.deleteGroup(id).then(function(res) {
			xwrt.checked(res);
			return self.reload();
		}).catch(function(err) {
			ui.addNotification(null, E('p', _('Could not delete: %s').format(err.message)), 'error');
		});
	},

	reload: function() {
		return this.load().then(L.bind(function(data) {
			var body = document.getElementById('xwrt-body');
			if (body)
				dom.content(body, this.renderBody(data));
		}, this));
	},

	handleImport: function() {
		var textarea = E('textarea', {
			'class': 'cbi-input-textarea',
			'rows': 8,
			'style': 'width:100%',
			'placeholder': 'vless://…\nvmess://…\ntrojan://…\nss://…'
		});
		var self = this;

		ui.showModal(_('Add config'), [
			E('p', {}, _('Paste one or more configs, one per line.')),
			textarea,
			E('div', { 'class': 'right', 'style': 'margin-top:1em' }, [
				E('button', {
					'class': 'cbi-button',
					'click': ui.hideModal
				}, _('Cancel')),
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-positive',
					'click': ui.createHandlerFn(self, function() {
						var text = (textarea.value || '').trim();
						if (!text) {
							ui.addNotification(null, E('p', _('There is nothing to import.')), 'warning');
							return;
						}
						return xwrt.importLink(text, '').then(function(res) {
							ui.hideModal();
							xwrt.checked(res);
							ui.addNotification(null, E('p',
								_('%d servers added, %d skipped.')
									.format(res.imported || 0, res.skipped || 0)), 'info');
							return self.reload();
						}).catch(function(err) {
							ui.hideModal();
							ui.addNotification(null, E('p',
								_('Could not import: %s').format(err.message)), 'error');
						});
					})
				}, _('Import'))
			])
		]);
	},

	handleAddSubscription: function() {
		var url = E('input', { 'class': 'cbi-input-text', 'style': 'width:100%',
			'placeholder': 'https://example.com/subscribe?token=…' });
		var name = E('input', { 'class': 'cbi-input-text', 'style': 'width:100%',
			'placeholder': _('Optional name') });
		var self = this;

		ui.showModal(_('Add subscription'), [
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Address (URL)')),
				E('div', { 'class': 'cbi-value-field' }, url)
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Name')),
				E('div', { 'class': 'cbi-value-field' }, name)
			]),
			E('div', { 'class': 'right', 'style': 'margin-top:1em' }, [
				E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')),
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-positive',
					'click': ui.createHandlerFn(self, function() {
						if (!url.value) {
							ui.addNotification(null, E('p', _('An address (URL) is required.')), 'warning');
							return;
						}
						ui.showModal(_('Fetching…'), [
							E('p', { 'class': 'spinning' }, _('Downloading the server list'))
						]);
						return xwrt.subAdd(url.value, name.value || '').then(function(res) {
							ui.hideModal();
							xwrt.checked(res);
							return self.reload();
						}).catch(function(err) {
							ui.hideModal();
							ui.addNotification(null, E('p',
								_('Subscription failed: %s').format(err.message)), 'error');
						});
					})
				}, _('Add'))
			])
		]);
	},

	handleConnect: function(id) {
		var self = this;
		ui.showModal(_('Connecting…'), [
			E('p', { 'class': 'spinning' }, _('Starting the core and applying the rules'))
		]);
		return xwrt.connect(id).then(function(res) {
			ui.hideModal();
			xwrt.checked(res);
			return self.reload();
		}).catch(function(err) {
			ui.hideModal();
			ui.addNotification(null, E('p', _('Could not connect: %s').format(err.message)), 'error');
		});
	},

	handlePing: function(id, cell) {
		dom.content(cell, E('em', {}, _('measuring…')));
		return xwrt.ping(id).then(function(res) {
			if (res && res.ok)
				dom.content(cell, E('span', { 'style': 'color:#4caf50' }, res.ms + ' ms'));
			else
				dom.content(cell, E('span', {
					'style': 'color:#f44336',
					'title': (res && res.error) || ''
				}, _('unreachable')));
		}).catch(function() {
			dom.content(cell, E('span', { 'style': 'color:#f44336' }, _('error')));
		});
	},


	//
	// The chain is shown before anything is saved-looking, because a pin records
	// whatever answered: on a network that is already intercepting the
	// connection, this would pin the interceptor. Seeing the issuer is how
	// someone notices that.
	handlePinCert: function(id, label) {
		var self = this;
		ui.showModal(_('Reading the certificate…'), [
			E('p', { 'class': 'spinning' }, _('Connecting to %s').format(label))
		]);
		return xwrt.fetchCert(id).then(function(res) {
			ui.hideModal();
			xwrt.checked(res);

			var leaf = (res.chain || [])[0] || {};

			// What the reader needs is one line of identity and the value that
			// was saved. The rest of the chain is real information but not the
			// point, so it goes behind a disclosure rather than on top of the
			// thing they came to see.
			var body = [
				E('p', {}, _('%s will now be accepted only with this certificate.')
					.format(res.endpoint)),

				E('div', { 'style': 'margin:.9em 0 .3em;opacity:.75' }, _('Fingerprint')),
				E('div', { 'style': PIN_BOX }, res.pin),

				E('div', { 'style': 'margin-top:1em' }, [
					E('strong', {}, leaf.subject || ''),
					E('div', { 'style': 'opacity:.75;font-size:92%;margin-top:.2em' },
						_('Issued by %s, valid until %s')
							.format(shortIssuer(leaf.issuer), xwrt.localDateTime(leaf.not_after)))
				])
			];

			// Three situations, three different things worth saying. Conflating
			// "nobody vouches for this" with "issued for another name" would be
			// alarming and, for a normal setup like this one, simply wrong.
			if (!res.trusted)
				body.push(E('div', { 'class': 'alert-message warning', 'style': 'margin-top:1em' },
					_('No public authority vouches for this certificate. On a self-signed server that is normal — but check that the issuer above is the party you expect: if something is sitting between you and the server right now, its certificate is the one that would be pinned.')));
			else if (!res.name_match)
				body.push(E('div', { 'style': 'margin-top:1em;opacity:.8' },
					_('The certificate is publicly trusted, but it was issued for a name other than the SNI this profile uses (%s). That is a common arrangement; the pinning itself is what does the work.').format(res.sni)));
			else
				body.push(E('div', { 'style': 'margin-top:1em;opacity:.8' },
					_('The certificate is publicly trusted and matches the SNI in use.')));

			if ((res.chain || []).length > 1)
				body.push(E('details', { 'style': 'margin-top:1em' }, [
					E('summary', { 'style': 'cursor:pointer;opacity:.75' }, _('Full chain')),
					E('div', { 'style': 'margin-top:.5em' }, res.chain.map(function(c, i) {
						return E('div', { 'style': 'margin-bottom:.5em;font-size:92%' }, [
							E('div', {}, (i === 0 ? _('Certificate') : _('Issuer')) + ': ' + c.subject),
							E('div', { 'style': 'opacity:.7' },
								_('Issued by %s, expires %s')
									.format(c.issuer, xwrt.localDateTime(c.not_after)))
						]);
					}))
				]));

			body.push(E('div', { 'class': 'right', 'style': 'margin-top:1.2em' },
				E('button', {
					'class': 'cbi-button cbi-button-action',
					'click': ui.createHandlerFn(self, function() {
						ui.hideModal();
						return self.reload();
					})
				}, _('OK'))));

			ui.showModal(_('Certificate pinned'), body);
		}).catch(function(err) {
			ui.hideModal();
			ui.addNotification(null, E('p',
				_('Could not read the certificate: %s').format(err.message)), 'error');
		});
	},

	handleDelete: function(id, label) {
		var self = this;
		if (!confirm(_('Delete the server "%s"?').format(label)))
			return;
		return xwrt.deleteProfile(id).then(function(res) {
			xwrt.checked(res);
			return self.reload();
		}).catch(function(err) {
			ui.addNotification(null, E('p', _('Could not delete: %s').format(err.message)), 'error');
		});
	},

	handleSubRefresh: function(id) {
		var self = this;
		ui.showModal(_('Refreshing…'), [
			E('p', { 'class': 'spinning' }, _('Downloading the server list'))
		]);
		return xwrt.subRefresh(id).then(function(res) {
			ui.hideModal();
			xwrt.checked(res);
			ui.addNotification(null, E('p',
				_('The subscription has %d servers.').format(res.count || 0)), 'info');
			return self.reload();
		}).catch(function(err) {
			ui.hideModal();
			ui.addNotification(null, E('p', _('Could not refresh: %s').format(err.message)), 'error');
		});
	},

	handleSubDelete: function(id, name) {
		var self = this;
		if (!confirm(_('Delete the subscription "%s" and every server in it?').format(name)))
			return;
		return xwrt.subDel(id).then(function(res) {
			xwrt.checked(res);
			return self.reload();
		}).catch(function(err) {
			ui.addNotification(null, E('p', _('Could not delete: %s').format(err.message)), 'error');
		});
	},

	renderProfiles: function(profiles, status) {
		var self = this;
		var rows = profiles.map(function(p) {
			var pingCell = E('div', { 'class': 'td' }, '-');
			var active = (status.profile_id === p.id) && status.connected;

			return E('div', { 'class': 'tr' }, [
				E('div', { 'class': 'td' }, [
					active
						? E('span', {
							'style': 'color:#4caf50;font-weight:bold',
							'title': _('Connected right now')
						}, '● ')
						: '',
					xwrt.profileLabel(p)
				]),
				E('div', { 'class': 'td' }, p.address + ':' + p.port),
				E('div', { 'class': 'td' }, xwrt.transportLabel(p)),
				E('div', { 'class': 'td' }, p.subscription ? _('subscription') : _('manual')),
				pingCell,
				xwrt.rowActions([
					E('button', {
						'class': 'cbi-button cbi-button-apply',
						'click': ui.createHandlerFn(self, function() {
							return self.handleConnect(p.id);
						})
					}, _('Connect')),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							return self.handlePing(p.id, pingCell);
						})
					}, _('Test')),
					' ',
					// Only offered where it means something: a TLS profile that
					// still relies on skipping verification, or one whose pin
					// needs refreshing after the server's certificate is renewed.
					(p.security === 'tls')
						? E('button', {
							'class': 'cbi-button ' +
								(p.allow_insecure ? 'cbi-button-action' : 'cbi-button-neutral'),
							'title': p.pinned_cert
								? _('Certificate pinned. Read it again if the server has renewed it.')
								: _('Save the server\'s certificate instead of skipping verification'),
							'click': ui.createHandlerFn(self, function() {
								return self.handlePinCert(p.id, xwrt.profileLabel(p));
							})
						}, p.pinned_cert ? _('Pin again') : _('Pin the certificate'))
						: '',
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							return self.handleEditProfile(p);
						})
					}, _('Edit')),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'click': ui.createHandlerFn(self, function() {
							return self.handleDelete(p.id, xwrt.profileLabel(p));
						})
					}, _('Delete'))
				])
			]);
		});

		if (!rows.length)
			rows = [ E('div', { 'class': 'tr placeholder' }, [
				E('div', { 'class': 'td' }, E('em', {}, _('No servers yet. Paste a config to get started.')))
			]) ];

		return xwrt.scroll(E('div', { 'class': 'table cbi-section-table xwrt-table' }, [
			E('div', { 'class': 'tr table-titles' }, [
				E('div', { 'class': 'th' }, _('Name')),
				E('div', { 'class': 'th' }, _('Endpoint')),
				E('div', { 'class': 'th' }, _('Transport')),
				E('div', { 'class': 'th' }, _('Source')),
				E('div', { 'class': 'th' }, _('Latency')),
				E('div', { 'class': 'th cbi-section-actions' }, '')
			])
		].concat(rows)));
	},

	renderGroups: function(groups, profiles, status) {
		var self = this;
		var byID = {};
		profiles.forEach(function(p) { byID[p.id] = p; });

		var rows = groups.map(function(g) {
			var active = status.connected && status.profile_id === g.id;
			var names = (g.members || []).map(function(id) {
				return byID[id] ? xwrt.profileLabel(byID[id]) : _('(deleted)');
			});

			return E('div', { 'class': 'tr' }, [
				E('div', { 'class': 'td' }, [
					active ? E('span', {
						'style': 'color:#4caf50;font-weight:bold',
						'title': _('Connected right now')
					}, '● ') : '',
					g.name || g.id
				]),
				E('div', { 'class': 'td' }, xwrt.strategyLabel(g.strategy)),
				E('div', { 'class': 'td' }, [
					String(names.length) + ' ',
					E('span', {
						'style': 'color:#9e9e9e',
						'title': names.join('\n')
					}, names.length ? '(' + names.slice(0, 3).join(', ') +
						(names.length > 3 ? ', …' : '') + ')' : '')
				]),
				xwrt.rowActions([
					E('button', {
						'class': 'cbi-button cbi-button-apply',
						'click': ui.createHandlerFn(self, function() {
							return self.handleConnect(g.id);
						})
					}, _('Connect')),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							return self.handleAddGroup(profiles, g);
						})
					}, _('Edit')),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'click': ui.createHandlerFn(self, function() {
							return self.handleGroupDelete(g.id, g.name || g.id);
						})
					}, _('Delete'))
				])
			]);
		});

		if (!rows.length)
			rows = [ E('div', { 'class': 'tr placeholder' }, [
				E('div', { 'class': 'td' }, E('em', {},
					_('No groups. A group holds several servers at once and, with a failover strategy, moves off one that stops answering.')))
			]) ];

		return xwrt.scroll(E('div', { 'class': 'table cbi-section-table xwrt-table' }, [
			E('div', { 'class': 'tr table-titles' }, [
				E('div', { 'class': 'th' }, _('Name')),
				E('div', { 'class': 'th' }, _('Strategy')),
				E('div', { 'class': 'th' }, _('Members')),
				E('div', { 'class': 'th cbi-section-actions' }, '')
			])
		].concat(rows)));
	},

	renderSubscriptions: function(subs) {
		var self = this;
		var rows = subs.map(function(s) {
			return E('div', { 'class': 'tr' }, [
				E('div', { 'class': 'td' }, s.name || '-'),
				E('div', { 'class': 'td', 'style': 'word-break:break-all' }, s.url),
				E('div', { 'class': 'td' }, String(s.count || 0)),
				E('div', { 'class': 'td' }, s.updated || _('never')),
				xwrt.rowActions([
					E('button', {
						'class': 'cbi-button cbi-button-neutral',
						'click': ui.createHandlerFn(self, function() {
							return self.handleSubRefresh(s.id);
						})
					}, _('Refresh')),
					' ',
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'click': ui.createHandlerFn(self, function() {
							return self.handleSubDelete(s.id, s.name || s.url);
						})
					}, _('Delete'))
				])
			]);
		});

		if (!rows.length)
			rows = [ E('div', { 'class': 'tr placeholder' }, [
				E('div', { 'class': 'td' }, E('em', {}, _('No subscriptions.')))
			]) ];

		return xwrt.scroll(E('div', { 'class': 'table cbi-section-table xwrt-table' }, [
			E('div', { 'class': 'tr table-titles' }, [
				E('div', { 'class': 'th' }, _('Name')),
				E('div', { 'class': 'th' }, _('Address (URL)')),
				E('div', { 'class': 'th' }, _('Servers')),
				E('div', { 'class': 'th' }, _('Updated')),
				E('div', { 'class': 'th cbi-section-actions' }, '')
			])
		].concat(rows)));
	},

	renderBody: function(data) {
		var config = data[0] || {};
		var status = data[1] || {};
		return [
			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Servers')),
				this.renderProfiles(config.profiles || [], status)
			]),
			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Groups')),
				this.renderGroups(config.groups || [], config.profiles || [], status)
			]),
			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Subscriptions')),
				this.renderSubscriptions(config.subscriptions || [])
			])
		];
	},

	render: function(data) {
		return E('div', { 'class': 'cbi-map' }, [
			xwrt.style(),
			E('h2', {}, _('Servers')),
			E('div', { 'class': 'cbi-map-descr' },
				_('Add servers by pasting configs, or from a subscription address.')),
			E('div', { 'style': 'margin-bottom:1em;display:flex;gap:.5em;flex-wrap:wrap' }, [
				E('button', {
					'class': 'cbi-button cbi-button-add',
					'click': ui.createHandlerFn(this, 'handleImport')
				}, _('Add config')),
				E('button', {
					'class': 'cbi-button cbi-button-add',
					'click': ui.createHandlerFn(this, 'handleAddSubscription')
				}, _('Add subscription')),
				E('button', {
					'class': 'cbi-button cbi-button-add',
					'click': ui.createHandlerFn(this, function() {
						return xwrt.config().then(L.bind(function(cfg) {
							this.handleAddGroup(cfg.profiles || [], null);
						}, this));
					})
				}, _('New group'))
			]),
			E('div', { 'id': 'xwrt-body' }, this.renderBody(data))
		]);
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
