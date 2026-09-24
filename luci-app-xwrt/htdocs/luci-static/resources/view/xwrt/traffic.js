'use strict';
'require view';
'require ui';
'require dom';
'require poll';
'require xwrt';

// Live traffic: throughput over the last few minutes, and what is flowing
// right now, per LAN client.
//
// The chart plots two series of the same measure on one axis. Colours are the
// validated categorical slots 1 and 2 (blue, orange), which clear the
// colour-vision separation threshold in both light and dark modes; they are
// assigned by identity — download is always blue, upload always orange — and
// never reassigned when one series is hidden.

var CHART_HEIGHT = 180;
var PAD = { top: 12, right: 10, bottom: 20, left: 56 };

function css(el, name, fallback) {
	var v = getComputedStyle(el).getPropertyValue(name);
	return (v && v.trim()) || fallback;
}

function fmtRate(n) {
	return xwrt.formatBytes(n) + '/s';
}

function fmtClock(unix) {
	var d = new Date(unix * 1000);
	return ('0' + d.getHours()).slice(-2) + ':' +
		('0' + d.getMinutes()).slice(-2) + ':' +
		('0' + d.getSeconds()).slice(-2);
}

// niceMax rounds the axis top up to a readable number so the gridline labels
// are not arbitrary, and keeps a floor so an idle link does not draw a chart
// scaled to a few bytes.
function niceMax(v) {
	var min = 64 * 1024;
	if (!v || v < min)
		return min;
	var pow = Math.pow(2, Math.ceil(Math.log2(v)));
	return pow;
}

function Chart(canvas, legendEl) {
	this.canvas = canvas;
	this.legendEl = legendEl;
	this.samples = [];
	this.hover = null;

	var self = this;
	canvas.addEventListener('mousemove', function(ev) {
		var r = canvas.getBoundingClientRect();
		self.hover = { x: ev.clientX - r.left, y: ev.clientY - r.top };
		self.draw();
	});
	canvas.addEventListener('mouseleave', function() {
		self.hover = null;
		self.draw();
	});
}

Chart.prototype.setData = function(samples) {
	this.samples = samples || [];
	this.draw();
};

Chart.prototype.draw = function() {
	var c = this.canvas;
	var parent = c.parentNode;
	if (!parent)
		return;

	var dpr = window.devicePixelRatio || 1;
	var w = parent.clientWidth || 600;
	var h = CHART_HEIGHT;
	if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
		c.width = Math.round(w * dpr);
		c.height = Math.round(h * dpr);
		c.style.width = w + 'px';
		c.style.height = h + 'px';
	}

	var ctx = c.getContext('2d');
	ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
	ctx.clearRect(0, 0, w, h);

	var down = css(c, '--xwrt-down', '#2a78d6');
	var up = css(c, '--xwrt-up', '#eb6834');
	var grid = css(c, '--xwrt-grid', 'rgba(128,128,128,.25)');
	var ink = css(c, '--xwrt-ink', '#52514e');
	var surface = css(c, '--xwrt-surface', '#ffffff');

	var plotW = Math.max(10, w - PAD.left - PAD.right);
	var plotH = Math.max(10, h - PAD.top - PAD.bottom);
	var s = this.samples;

	var peak = 0;
	for (var i = 0; i < s.length; i++) {
		peak = Math.max(peak, s[i].uplink_rate || 0, s[i].downlink_rate || 0);
	}
	var top = niceMax(peak);

	// Recessive gridlines and axis labels: they orient, they do not compete.
	ctx.strokeStyle = grid;
	ctx.fillStyle = ink;
	ctx.lineWidth = 1;
	ctx.font = '11px system-ui, sans-serif';
	ctx.textAlign = 'right';
	ctx.textBaseline = 'middle';
	for (var g = 0; g <= 2; g++) {
		var val = top * (1 - g / 2);
		var y = PAD.top + (plotH * g) / 2;
		ctx.beginPath();
		ctx.moveTo(PAD.left, y + 0.5);
		ctx.lineTo(PAD.left + plotW, y + 0.5);
		ctx.stroke();
		ctx.fillText(fmtRate(val), PAD.left - 6, y);
	}

	if (s.length < 2) {
		ctx.textAlign = 'center';
		ctx.fillText(_('waiting for a measurement…'), PAD.left + plotW / 2, PAD.top + plotH / 2);
		this.renderLegend(null);
		return;
	}

	var stepX = plotW / (s.length - 1);
	function px(i) { return PAD.left + i * stepX; }
	function py(v) { return PAD.top + plotH - (Math.min(v, top) / top) * plotH; }

	function line(key, colour) {
		ctx.strokeStyle = colour;
		ctx.lineWidth = 2;
		ctx.lineJoin = 'round';
		ctx.lineCap = 'round';
		ctx.beginPath();
		for (var i = 0; i < s.length; i++) {
			var x = px(i), y = py(s[i][key] || 0);
			if (i === 0)
				ctx.moveTo(x, y);
			else
				ctx.lineTo(x, y);
		}
		ctx.stroke();
	}
	line('downlink_rate', down);
	line('uplink_rate', up);

	var last = s[s.length - 1];

	// Crosshair and readout. An HTML chart that shows a number only for "now"
	// makes the history unreadable; hovering answers "what was it then".
	var shown = last;
	if (this.hover && this.hover.x >= PAD.left && this.hover.x <= PAD.left + plotW) {
		var idx = Math.round((this.hover.x - PAD.left) / stepX);
		idx = Math.max(0, Math.min(s.length - 1, idx));
		shown = s[idx];

		var hx = px(idx);
		ctx.strokeStyle = grid;
		ctx.lineWidth = 1;
		ctx.beginPath();
		ctx.moveTo(hx + 0.5, PAD.top);
		ctx.lineTo(hx + 0.5, PAD.top + plotH);
		ctx.stroke();

		// A 2px surface ring keeps the markers legible where the two lines
		// cross each other.
		[['downlink_rate', down], ['uplink_rate', up]].forEach(function(pair) {
			var y = py(shown[pair[0]] || 0);
			ctx.beginPath();
			ctx.arc(hx, y, 4.5, 0, Math.PI * 2);
			ctx.fillStyle = pair[1];
			ctx.fill();
			ctx.lineWidth = 2;
			ctx.strokeStyle = surface;
			ctx.stroke();
		});
	}

	ctx.fillStyle = ink;
	ctx.textAlign = 'left';
	ctx.fillText(fmtClock(s[0].at), PAD.left, h - 8);
	ctx.textAlign = 'right';
	ctx.fillText(fmtClock(last.at), PAD.left + plotW, h - 8);

	this.renderLegend(shown, shown !== last);
};

// The legend is always present for two series and carries the value directly,
// so identity never rests on colour alone.
Chart.prototype.renderLegend = function(sample, isHover) {
	if (!this.legendEl)
		return;

	function item(label, colour, value) {
		return E('span', { 'style': 'display:inline-flex;align-items:center;gap:.4em;margin-right:1.4em' }, [
			E('span', {
				'style': 'width:10px;height:10px;border-radius:2px;background:' + colour +
					';display:inline-block;flex:none'
			}),
			E('span', {}, label),
			E('strong', {}, value)
		]);
	}

	var c = this.canvas;
	var down = css(c, '--xwrt-down', '#2a78d6');
	var up = css(c, '--xwrt-up', '#eb6834');

	dom.content(this.legendEl, [
		item(_('Download'), down, sample ? fmtRate(sample.downlink_rate || 0) : '—'),
		item(_('Upload'), up, sample ? fmtRate(sample.uplink_rate || 0) : '—'),
		isHover
			? E('span', { 'style': 'color:#9e9e9e' }, _('as of %s').format(fmtClock(sample.at)))
			: E('span', { 'style': 'color:#9e9e9e' }, _('now'))
	]);
};

// capacityLine shows how full the kernel's connection table is.
//
// It is here because that number is the only visible sign of a failure that
// otherwise has none. Every connection a client makes through a NAT capture
// mode — redirect and mixed — takes a slot; when the table fills, the kernel
// drops new connections until old ones time out, and from a chair that looks
// like a video playing happily for twenty minutes and then stalling for a few
// seconds at a time, over and over. The tunnel is up the whole while and
// nothing else on any screen changes.
//
// TUN mode barely touches the table, because the client's connection is
// terminated in userspace, which is why the same device can be fine there and
// stall in mixed.
function capacityLine(cap) {
	if (!cap || !cap.known)
		return [];

	var text = _('Kernel connection table: %d of %d (%d%%).')
		.format(cap.count || 0, cap.max || 0, cap.percent || 0);

	if (cap.percent < 80)
		return E('div', { 'class': 'cbi-section-descr' }, text);

	return E('div', { 'class': 'alert-message warning' }, [
		E('div', {}, E('strong', {}, text)),
		E('div', { 'style': 'margin-top:.3em' },
			_('When this fills, the kernel drops new connections until old ones time out — which looks like a video freezing for a few seconds and then carrying on. Redirect and mixed modes take a slot per client connection; TUN mode takes almost none.')),
		E('div', { 'style': 'margin-top:.3em' }, [
			_('Raise it with:') + ' ',
			E('code', {}, 'sysctl -w net.netfilter.nf_conntrack_max=' +
				((cap.max || 0) * 2)),
			' ' + _('(add it to /etc/sysctl.conf so it survives a reboot)')
		])
	]);
}

return view.extend({
	load: function() {
		return Promise.all([
			xwrt.traffic(),
			xwrt.connections(100)
		]);
	},

	handleEnableAccounting: function() {
		var self = this;
		return xwrt.enableAccounting().then(function(res) {
			xwrt.checked(res);
			ui.addNotification(null, E('p',
				_('Byte counters are on. Volumes appear as new connections are made.')), 'info');
			return self.refreshConnections();
		}).catch(function(err) {
			ui.addNotification(null, E('p',
				_('Could not turn on the byte counters: %s').format(err.message)), 'error');
		});
	},

	refreshConnections: function() {
		return xwrt.connections(100).then(L.bind(function(snap) {
			var el = document.getElementById('xwrt-conns');
			if (el)
				dom.content(el, this.renderConnections(snap || {}));
		}, this));
	},

	renderClients: function(snap) {
		var acct = snap.accounting;
		var rows = (snap.clients || []).map(function(c) {
			return E('div', { 'class': 'tr' }, [
				E('div', { 'class': 'td' }, [
					c.hostname ? E('strong', {}, c.hostname) : E('em', {}, _('unknown')),
					E('span', { 'style': 'color:#9e9e9e' }, '  ' + c.ip)
				]),
				E('div', { 'class': 'td' }, String(c.flows)),
				E('div', { 'class': 'td' }, acct ? xwrt.formatBytes(c.bytes_up) : '—'),
				E('div', { 'class': 'td' }, acct ? xwrt.formatBytes(c.bytes_down) : '—')
			]);
		});

		if (!rows.length)
			rows = [ E('div', { 'class': 'tr placeholder' }, [
				E('div', { 'class': 'td' }, E('em', {}, _('No active connections from the LAN.')))
			]) ];

		return xwrt.scroll(E('div', { 'class': 'table cbi-section-table xwrt-table' }, [
			E('div', { 'class': 'tr table-titles' }, [
				E('div', { 'class': 'th' }, _('Client')),
				E('div', { 'class': 'th' }, _('Flows')),
				E('div', { 'class': 'th' }, _('Sent')),
				E('div', { 'class': 'th' }, _('Received'))
			])
		].concat(rows)));
	},

	renderFlows: function(snap) {
		var acct = snap.accounting;
		var rows = (snap.flows || []).slice(0, 50).map(function(f) {
			return E('div', { 'class': 'tr' }, [
				E('div', { 'class': 'td' },
					(f.hostname || f.src) + ':' + f.sport),
				E('div', { 'class': 'td' }, f.dst + ':' + f.dport),
				E('div', { 'class': 'td' }, f.protocol + (f.state ? ' · ' + f.state.toLowerCase() : '')),
				E('div', { 'class': 'td' }, acct
					? xwrt.formatBytes(f.bytes_up) + ' / ' + xwrt.formatBytes(f.bytes_down)
					: '—')
			]);
		});

		if (!rows.length)
			rows = [ E('div', { 'class': 'tr placeholder' }, [
				E('div', { 'class': 'td' }, E('em', {}, _('Nothing active.')))
			]) ];

		return xwrt.scroll(E('div', { 'class': 'table cbi-section-table xwrt-table' }, [
			E('div', { 'class': 'tr table-titles' }, [
				E('div', { 'class': 'th' }, _('Source')),
				E('div', { 'class': 'th' }, _('Destination')),
				E('div', { 'class': 'th' }, _('Protocol')),
				E('div', { 'class': 'th' }, _('Sent / received'))
			])
		].concat(rows)));
	},

	renderConnections: function(snap) {
		var parts = [];

		if (!snap.available) {
			parts.push(E('div', { 'class': 'alert-message warning' },
				snap.note || _('Connection tracking is not available on this device.')));
			return parts;
		}
		if (!snap.accounting) {
			parts.push(E('div', { 'class': 'alert-message' }, [
				E('p', {}, _('The core is tracking connections but not counting bytes, which is why the volumes below show “—”. The flow counts are right either way.')),
				E('button', {
					'class': 'cbi-button cbi-button-action',
					'click': ui.createHandlerFn(this, 'handleEnableAccounting')
				}, _('Turn on the byte counters'))
			]));
		}

		parts.push(E('h3', {}, _('Clients')));
		parts.push(this.renderClients(snap));
		parts.push(E('h3', { 'style': 'margin-top:1.4em' }, _('Active flows')));
		parts.push(E('div', { 'class': 'cbi-section-descr' },
			_('%d of the %d tracked connections come from the local network.')
				.format(snap.lan_flows || 0, snap.total_flows || 0)));
		parts.push(capacityLine(snap.capacity));
		parts.push(this.renderFlows(snap));
		return parts;
	},

	render: function(data) {
		var traffic = data[0] || {};
		var conns = data[1] || {};
		var self = this;

		// Both themes are selected rather than flipped: the dark values are the
		// same two hues re-stepped for a dark surface.
		var style = E('style', {}, [
			'.xwrt-viz{' +
			'--xwrt-down:#2a78d6;--xwrt-up:#eb6834;' +
			'--xwrt-grid:rgba(90,90,90,.22);--xwrt-ink:#52514e;--xwrt-surface:#fcfcfb}' +
			'@media (prefers-color-scheme: dark){.xwrt-viz{' +
			'--xwrt-down:#3987e5;--xwrt-up:#d95926;' +
			'--xwrt-grid:rgba(200,200,200,.20);--xwrt-ink:#c3c2b7;--xwrt-surface:#1a1a19}}' +
			'.xwrt-viz canvas{display:block;max-width:100%}'
		]);

		var canvas = E('canvas', { 'id': 'xwrt-chart' });
		var legend = E('div', {
			'id': 'xwrt-legend',
			'style': 'margin-top:.5em;font-size:90%;display:flex;flex-wrap:wrap;align-items:center'
		});

		var chart = new Chart(canvas, legend);
		this.chart = chart;

		window.addEventListener('resize', function() { chart.draw(); });

		poll.add(function() {
			return xwrt.traffic().then(function(t) {
				chart.setData((t || {}).samples || []);
			});
		}, 2);

		poll.add(function() {
			return self.refreshConnections();
		}, 5);

		// Draw once the element is in the document and has a width.
		window.setTimeout(function() {
			chart.setData(traffic.samples || []);
		}, 0);

		return E('div', { 'class': 'cbi-map' }, [
			xwrt.style(),
			style,
			E('h2', {}, _('Traffic')),
			E('div', { 'class': 'cbi-map-descr' },
				_('The throughput going through the proxy, and the connections from the local network the core is watching right now.')),

			E('div', { 'class': 'cbi-section xwrt-viz' }, [
				E('h3', {}, _('Throughput')),
				E('div', { 'style': 'position:relative' }, canvas),
				legend,
				E('div', { 'class': 'cbi-section-descr', 'style': 'margin-top:.4em' },
					_('The counters come from the proxy core, so the traffic here is the traffic that goes through the VPN. Traffic a rule sends out directly is not counted here.'))
			]),

			E('div', { 'class': 'cbi-section', 'id': 'xwrt-conns' },
				this.renderConnections(conns))
		]);
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
