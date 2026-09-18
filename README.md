# xDivine-Ray

A device-independent Xray proxy manager for OpenWrt.

The package, the service, the command and `/etc/config/xwrt` are all still
called `xwrt`: the name in the interface changed, and changing the other one
would rename a configuration file on every device that already has one. So the
project is xDivine-Ray and the thing on the filesystem is xwrt, deliberately.

The point of this project is portability. Nothing about the router is assumed:
the daemon asks the running system what the LAN and WAN actually are, which
firewall stack is in use, and whether the kernel can do TPROXY, then builds its
configuration from the answers. The same binary works on a vendor image whose
bridge is called `br0` and on stock OpenWrt where it is `br-lan`.

## What it does

* Manages VLESS, VMess, Trojan and Shadowsocks servers, imported from share
  links or a subscription URL.
* Groups several servers behind one selection, with health-aware strategies
  that move off a server that stops answering.
* Routing exceptions: keep a site off the VPN, force one through it, or block
  it — by domain, address, client, port or protocol.
* Live traffic view: throughput over the last ten minutes and the connections
  the kernel is tracking, per LAN client.
* Captures LAN traffic in one of four modes, from a pure NAT redirect that
  needs nothing extra, to a TUN device served by hev-socks5-tunnel.
* Steers DNS through the proxy without breaking local name resolution.
* Stores everything in UCI, so LuCI edits it and it survives sysupgrade.
* Ships one binary that is both the daemon and the CLI, and a LuCI app.

## Supported devices

**The floor is 128 MB of RAM and 128 MB of writable storage.** Anything below
that is out of scope by design. Tuning the stack down for 32/64 MB devices
would constrain the design for everyone else, and the storage floor is what
lets the daemon, the proxy core and its optional geo data sit side by side
instead of turning every install into a jigsaw puzzle.

The daemon checks both at startup, reading `/proc/meminfo` and the filesystem
it is installed on, and says so in the log and on the Status page when a device
is under the line — rather than failing later in a way that looks like a bug in
the proxy.

Storage means writable storage: on OpenWrt that is the overlay, so a device
with a 16 MB flash chip and a USB disk mounted as extroot passes, and a bare
16 MB device does not.

Architecture is a separate question from the floor. These are the build
targets; whether a particular board qualifies depends on its RAM and storage,
not on its CPU:

| Target | Typical hardware |
|---|---|
| `aarch64` | Filogic, IPQ807x, Rockchip, Raspberry Pi 4 and newer |
| `x86_64` | mini PCs, VMs, x86 routers |
| `armv7` | IPQ40xx and other Cortex-A7/A9 boards |
| `mipsel` | MT7621 — the NAND models qualify, the 16/32 MB ones need extroot |
| `mips64el` | Octeon, e.g. EdgeRouter |
| `riscv64` | newer RISC-V boards |

## Software requirements

| Component | Needed for | Notes |
|---|---|---|
| `xray-core` | everything | The proxy core. Install from the packages feed, or drop a binary in and point `xray_bin` at it. |
| `hev-socks5-tunnel` | `mixed` and `tun` modes | Small C program, about 400 KB. |
| `kmod-tun` | `mixed` and `tun` modes | |
| `ip-full` | every mode except plain `redirect` | BusyBox `ip` cannot manage rules and tables. |
| `kmod-nft-tproxy` or `kmod-ipt-tproxy` | `tproxy` mode only | The daemon refuses to connect in tproxy mode without it, rather than starting and silently dropping UDP. |
| `xray-geodata` (geoip.dat) | optional | When present, `geoip:private` is added to the direct rules on top of the built-in CIDR list. Detected, never assumed: naming it without the files makes the core refuse to start. |

## Installing

### Quick path, no SDK

```sh
./build.sh                 # every architecture into ./dist
./build.sh aarch64         # or just one
```

Then on the router:

```sh
scp dist/<target>/xwrt root@192.168.1.1:/usr/sbin/xwrt
scp -r package/xwrt/files/* root@192.168.1.1:/

ssh root@192.168.1.1 '
  ln -sf /usr/sbin/xwrt /usr/sbin/xwrtd
  chmod +x /usr/sbin/xwrt /etc/init.d/xwrt /usr/libexec/rpcd/xwrt \
           /etc/uci-defaults/99-xwrt /etc/hotplug.d/firewall/99-xwrt
  sh /etc/uci-defaults/99-xwrt
  /etc/init.d/rpcd restart
  /etc/init.d/xwrt enable && /etc/init.d/xwrt start
'
```

For the LuCI app, copy `luci-app-xwrt/htdocs` and `luci-app-xwrt/root` over `/`
and restart rpcd and uhttpd.

### Proper packages

Every release carries `.apk` files for OpenWrt 25.12 and `.ipk` files for
24.10, built with the official SDK. Pick the one whose architecture your device
reports — `apk --print-arch`, or `opkg print-architecture` on 24.10 — and note
that it has to match exactly: an `aarch64_cortex-a53` package is refused on an
`aarch64_cortex-a72` device even though the code inside is identical.

They are not signed, which is what `--allow-untrusted` is about:

```sh
# OpenWrt 25.12
apk add --allow-untrusted ./openwrt-25.12.0-xwrt_*.apk \
                          ./openwrt-25.12.0-luci-app-xwrt_*.apk
# OpenWrt 24.10
opkg install ./openwrt-24.10.0-xwrt_*.ipk \
             ./openwrt-24.10.0-luci-app-xwrt_*.ipk
```

If your architecture is not among them, the tarball bundle above works on any
device of that CPU family, and building it yourself is the section below.

### Building it yourself

Symlink both directories into an OpenWrt SDK and build them:

```sh
ln -s $PWD/package/xwrt      <sdk>/package/xwrt
ln -s $PWD/luci-app-xwrt     <sdk>/package/luci-app-xwrt
cd <sdk> && make menuconfig      # Network -> xwrt, LuCI -> luci-app-xwrt
make package/xwrt/compile V=s
```

Or, to build it into a firmware image with everything it needs:

```sh
echo "src-link xwrt $PWD" >> <buildroot>/feeds.conf.default
cd <buildroot>
./scripts/feeds update xwrt && ./scripts/feeds install -a -p xwrt
make menuconfig && make -j$(nproc)
```

One thing to know if you do that: xwrt's own updater replaces
`/usr/sbin/xwrt` directly, and on a device where apk or opkg installed it, that
leaves the package database recording the old version — a later sysupgrade puts
it back. The About page detects this and says so before you press the button.

## Using it

```sh
xwrt import 'vless://…'       # add servers from a share link
xwrt list                     # see them, with their IDs
xwrt connect <profile-id>     # connect
xwrt status                   # state, mode, traffic counters
xwrt env                      # what the daemon detected on this device
xwrt disconnect

xwrt logs 100                 # recent log lines
xwrt logs error               # errors only; also source=xray or step=firewall
xwrt errors                   # failures, each with its cause and a suggested fix
xwrt clear-error              # dismiss the failure shown on the Status page

xwrt groups                   # configured groups
xwrt group-add 'EU' leastPing <profile-id> <profile-id> ...
xwrt connect <group-id>       # connect to a group the same way

xwrt sub-add https://example.com/sub 'Provider'
xwrt sub-refresh <sub-id>

xwrt set mode=mixed           # redirect | mixed | tproxy | tun
xwrt set proxy_router=true    # also proxy traffic the router itself sends
```

`logs` and `errors` print text, since they are read by people; everything else
prints JSON, which is also how the LuCI app talks to it: the rpcd
plugin at `/usr/libexec/rpcd/xwrt` is two lines of shell delegating to this
binary.

### Servers that skipped certificate verification

Recent Xray builds removed `allowInsecure`, and a share link carrying
`allowInsecure=1` is refused with an explanation rather than silently connecting
in the clear. The replacement is a certificate pin:

```sh
xwrt import 'vless://…allowInsecure=1…'
xwrt fetch-cert <profile-id>     # reads the certificate, stores its SHA-256
xwrt connect <profile-id>
```

`fetch-cert` must run on the router, over the path the connection will use: a
pin records whatever answered, so fetching it through something that intercepts
TLS would pin the interceptor. It prints the certificate's subject, issuer and
expiry for exactly that reason — and warns when no public authority vouches for
it, which is the normal case here. LuCI has the same thing as a "Pin cert"
button on the Servers page. Re-run it when the server renews its certificate.

The pinned value is the core's `pinnedPeerCertSha256`: hex SHA-256 of the
server's leaf certificate. It authenticates that one certificate and skips both
chain and hostname checks, which is what makes a borrowed SNI work — and it is
strictly better than the old flag, which accepted any certificate at all.

## How it fits together

```
LuCI  ──ubus──▶  rpcd plugin  ──▶  xwrt CLI  ──HTTP(loopback)──▶  xwrtd
                                                                    │
                        ┌───────────────────────────────────────────┤
                        ▼                     ▼                     ▼
                  UCI (/etc/config)     xray-core            firewall / routing
                                        (+ hev-socks5-tunnel
                                           in TUN mode)
```

The API listens on 127.0.0.1 only and has no authentication of its own: the
LuCI session is the authentication boundary. Do not bind it to a LAN address
without adding a token first.

### Capture modes

Everything here follows from one kernel limitation: **NAT REDIRECT cannot carry
UDP.** An application recovers the pre-NAT destination with `SO_ORIGINAL_DST`,
and that only exists for TCP. TPROXY has no such problem — it delivers the
packet locally without rewriting it — but it needs the tproxy kernel module and
a policy route. So proxying UDP means either TPROXY or a TUN device.

That matters more than it used to, because QUIC is UDP. Proxy TCP only and
QUIC either bypasses the proxy entirely or stalls until the browser falls back
to TCP.

| Mode | TCP | UDP | Needs | Notes |
|---|---|---|---|---|
| `redirect` | NAT redirect | not proxied | nothing extra | Most compatible. QUIC leaks straight out. |
| `mixed` | NAT redirect | TUN device | `hev-socks5-tunnel`, `kmod-tun` | **Default.** No tproxy module anywhere. TCP stays on the kernel-fast path; UDP crosses a userspace stack. |
| `tproxy` | TPROXY | TPROXY | `kmod-nft-tproxy` or `kmod-ipt-tproxy`, `ip-full` | Both protocols in the kernel, and TCP never enters conntrack's NAT table. Fastest for UDP. |
| `tun` | TUN device | TUN device | `hev-socks5-tunnel`, `kmod-tun` | Uniform and simple to reason about; everything pays the userspace cost. |

**Which should you pick?** `mixed` unless you have a reason not to: it proxies
UDP without needing a kernel module that may not be in your image, and TCP —
the bulk of connections — still takes the cheapest path. Move to `tproxy` if
UDP throughput matters and the module is available. Use `redirect` only when
you genuinely do not want UDP proxied.

The firewall rules live in a table of this daemon's own (`ip xwrt` in
nftables, `XWRT_*` chains in iptables). Nothing the device's own firewall owns
is modified, so teardown is a single delete.

TUN routing never touches the main table. A dedicated table holds the default
route through the TUN device, and higher-priority rules carve out what must not
go there: packets the core itself sends, matched by the socket mark it sets,
and packets bound for the LAN. An unclean shutdown therefore cannot strand the
device without a default route.

In mixed mode the same machinery carries UDP alone: the firewall marks UDP in
prerouting, and a rule matching that mark diverts it into the TUN table while
TCP falls through to the main table as usual. The core's escape rule earns its
keep here — a profile on a UDP transport such as QUIC or mKCP sends UDP itself,
and without it the tunnel would swallow its own upstream traffic.

Three marks are derived from the one configured base so they can never be
confused: base for the core's own sockets, base+1 for TPROXY, base+2 for
mixed-mode UDP.

### Groups and failover

A group holds several servers and a strategy that decides which one carries a
given connection. The strategy is the whole point, and only two of the four
give failover:

| Strategy | Behaviour | Failover |
|---|---|---|
| `leastPing` | Probes every member and uses the fastest | yes |
| `leastLoad` | Samples latency repeatedly, prefers the steadiest | yes |
| `random` | Spreads connections at random | no |
| `roundRobin` | Cycles through members in order | no |

Failover happens inside the core, not in the daemon: `leastPing` configures an
observatory that probes each member on an interval, and the balancer stops
selecting a member that fails. Nothing is restarted and no connection is
re-established when a server drops out. `random` and `roundRobin` have no
health information at all, so a dead member keeps receiving its share of
connections — the daemon says so in the log when you connect with one.

The core is strict about the wiring: `leastPing` without an observatory, or
`leastLoad` without a burst observatory, makes it refuse to start with an
unresolved dependency. The generator emits the right one for the strategy, and
the test suite checks every combination against the real core.

Deleting a server removes it from any group that held it, and a group whose
members have all been deleted — which a subscription refresh can cause — is
refused at connect time with an explanation rather than starting empty.

**Edits made while connected are weighed before they interrupt anything.**
Servers, groups and rules are compiled into the core's configuration when a
connection is made, so editing one changes what *will* run, not what is
running: the change is saved and the interface says so, with a button that
applies it. Which changes get that notice is not "any edit while connected" —
that was the first version, and it meant deleting a spare server offered to
drop a working tunnel and rebuild it to apply nothing. The material a
connection was built from is fingerprinted when it is made (the active server
or group with its members, every rule, and the settings the core reads), and an
edit raises the notice only when it would change that fingerprint. Deleting a
server that is not in use, renaming one outside the active group, or turning
off connect-on-startup are all stored quietly; anything the core would see
still asks.

### Routing exceptions

The default sends everything through the proxy. Rules are the exception list,
checked in order, first match wins:

```sh
xwrt rule-add 'Bank'    direct domain=bank.example.com domain=domain:gov.tr
xwrt rule-add 'Torrent' block  proto=bittorrent
xwrt rule-add 'Printer' direct ip=192.0.2.0/24 src=192.168.1.50 port=9100
xwrt rules
```

Matchers are `domain=`, `ip=`, `src=` (LAN client), `port=`, `sport=`,
`proto=` (http, tls, quic, bittorrent) and `net=`. Every matcher a rule sets
must hold for it to fire. Domain matching works because the core sniffs the TLS
SNI or HTTP Host out of the first packet of each connection; a firewall never
sees a domain, which is why rules live in the core rather than in nftables.

Domains accept the core's syntax: a bare name covers its subdomains, `full:` is
exact, `keyword:` is a substring, `regexp:` a pattern, and `geosite:` a named
list from the geo data package. A rule naming a `geosite:` or `geoip:` list
without those files installed is refused with an explanation rather than
producing a config the core will not start.

**A bypass also changes name resolution.** Without that it would not be a
bypass: the name would be looked up through the tunnel and the "direct"
connection made to an address chosen for the exit node's part of the world,
which is exactly what breaks a bank or a national service. Domains in a
bypass rule are therefore resolved by the device's own resolver. A rule scoped
to particular clients or source ports is left out of that, so a narrow
exception does not change resolution for everyone.

### Live traffic

```sh
xwrt traffic              # throughput history, ten minutes at two seconds
xwrt connections          # active flows, grouped by LAN client
xwrt enable-accounting    # turn on the kernel's byte counters
```

Two different sources, answering two different questions. Throughput comes from
the core's own counters, so it measures what went through the proxy. The
connection list comes from the kernel's tracking table, so it sees everything —
including traffic a rule sent out directly — and it knows which client each
flow belongs to, which the core does not. Client names come from the DHCP
leases.

The kernel tracks connections but does not count bytes unless
`net.netfilter.nf_conntrack_acct` is on, which it usually is not. Flow counts
are accurate either way; volumes read as zero until it is enabled, and both the
CLI and the LuCI page say so rather than showing a silently empty column.

Nothing is written to flash: the history is a ring in memory, which is the
right trade for a question about the present.

### DNS

Three modes. `dnsmasq` (the default) drops a snippet in `/tmp/dnsmasq.d` that
points the resolver at the core's DNS inbound, which keeps local hostnames and
DHCP leases working; removing the file reverses it completely. `redirect`
hijacks port 53 in the firewall instead. `off` leaves DNS alone.

### Surviving a firewall reload

`fw4` rebuilds its ruleset from scratch on reload and some versions flush the
whole nftables ruleset while doing it, taking this daemon's table with it. A
hotplug hook asks the daemon to reapply; the daemon checks first and does
nothing when its rules are still there.

## Configuration

Everything lives in `/etc/config/xwrt`. The two options worth knowing about:

* `lan_device` and `wan_device` are empty by default and should stay that way.
  The daemon detects them through ubus, UCI and `/proc/net/route` at every
  connect. Set them only if the Status page shows the wrong device.
* `tun_name` must match the device listed in the `xwrt` firewall zone that
  `/etc/uci-defaults/99-xwrt` creates. If you rename one, rename the other, or
  fw4 will drop forwarded traffic.

## Layout

```
cmd/xwrt/            entry point; daemon or CLI depending on how it is invoked
internal/app/        the two entry points, sharing one binary
internal/model/      profiles, settings, status
internal/ucicfg/     UCI parser, writer and typed store
internal/uri/        vless/vmess/trojan/ss share link parsing
internal/sub/        subscription fetching and decoding
internal/xray/       Xray config generation
internal/netenv/     runtime LAN/WAN/firewall detection
internal/netmon/     connection tracking, per-client traffic
internal/fw/         nftables and iptables backends
internal/mode/       TUN mode and DNS integration
internal/proc/       supervised child processes
internal/daemon/     connection lifecycle, stats, log ring
internal/api/        loopback HTTP API
package/xwrt/        OpenWrt package: init script, UCI defaults, rpcd plugin
luci-app-xwrt/       LuCI front-end (views, Turkish catalog, render tests)
```

The interface is English and Turkish. It follows the language the router's web
interface is set to (LuCI's own setting, or the browser's when that is set to
automatic), so there is nothing to choose in xwrt itself. Source strings are
English and the Turkish translation lives in
`luci-app-xwrt/htdocs/luci-static/resources/xwrt/i18n.js`, because a package
installed from a tarball has no buildsystem to compile `.po` files with.
Failures are translated too, but not the same way. Matching the daemon's
English sentences with patterns would break silently the first time one was
reworded, and an error box that quietly reverts to a language the reader does
not speak is worse than one that was never translated, because nobody notices.
So every failure carries a code: `internal/daemon/messages.go` holds the code,
its English sentence and the values it interpolates; the interface translates
by code and falls back to the English when it has nothing for one. Failures
decided further down — in the DNS or firewall packages — name themselves with
`internal/fault` and the step that reports them adopts the name.

The log line stays English regardless, because a log is usually read by
whoever is helping rather than by the person at the router, and a message that
can be searched for is worth more there. So does the `detail` block in the
interface: that is the core's or the kernel's own output, quoted, and a
translated copy of it is a string nobody can look up.

## Testing

```sh
go test ./...
```

Three layers, in increasing order of how much they prove.

**Generated output is checked against the real tools.** Firewall rulesets go
through `nft -c`; core configurations go to Xray itself with `run -test`, for
every protocol, transport, capture mode and group strategy. Point the suite at
a core with `XWRT_XRAY_BIN=/path/to/xray go test ./...`; without it those tests
skip. This is what catches a config the core rejects — and it has: Xray has
removed the mKCP header/seed options, the standalone HTTP/2 and QUIC
transports, and `allowInsecure`, all of which this generator used to emit.

**Share links are followed end to end.** The fidelity tests parse a link, build
the config, and assert that every parameter landed in the right field — path,
SNI, fingerprint, ALPN, flow, short ID, service name. They also check the other
direction: a minimal link must not gain a fingerprint or ALPN it never
specified, because an invented value changes how the connection looks on the
wire.

**The front-end is rendered without a browser.** `node luci-app-xwrt/test/render.js`
runs every view against a stubbed LuCI, in both languages, and checks what only
a phone would otherwise tell you: that no view throws on the data it will
really be given, that every table sits in its own scroll box, that no row has
more cells than its header, and that every cell carries the column label it
needs once the table becomes a stack of cards. `node luci-app-xwrt/test/i18n.js`
checks the Turkish catalog against the strings the views use — a string added
without a translation, an entry left behind after a rewording, and a
translation that dropped a `%s` are all things no diff shows. The failure
catalogs are checked from the Go side, by `go test ./internal/daemon/`: it
reads `i18n.js`, and fails when a code the daemon can raise has no sentence
there, when a sentence remains that nothing raises any more, when the two
disagree about how many values they interpolate, or when a call site passes a
code that is in neither catalog. The render test then reads a recorded failure
off the log page in both languages and insists on words that can only come from
translating by code.
`node luci-app-xwrt/test/settings.js` covers the one view the others cannot
reach: LuCI builds that form itself, so there is no tree to walk until LuCI is
there to build it. A stubbed `form` records what the page asks for instead —
every field, its label, its help text, its choices and its default
(`test/formstub.js`) — and checks that the page still builds when the device
list cannot be fetched, that the device pickers offer the interfaces this box
has (and not the loopback or our own tunnel), that automatic is the first
choice, and that the device in use is marked. The same record is turned back
into the markup LuCI would have produced and measured in the browser by
`mobile.js`, so the page's own text is laid out rather than merely counted.
That exemption had already cost three times: the page shipped for several
releases with no stylesheet at all, and then its help text ran off the right of
a phone screen for a week — both found by someone looking at a real router.

`node luci-app-xwrt/test/poll.js` covers what the Status page looks like three
seconds after it loads. The page is built once and then refreshed on a timer,
and those are two pieces of code writing the same cells; everything else here
looks only at the first one. They drifted, and the symptom was a value that
appeared correctly for an instant after a reload and then changed back — which
reads as a caching problem and is not one.

`node luci-app-xwrt/test/notice.js` covers the "saved, but not applied yet"
banner: that it appears once rather than once per edit, and that it goes away
when the change has been applied instead of sitting next to "Applied." until
someone closes it. Whether a change is pending at all is the daemon's
judgement and is tested on the Go side — see the fingerprint below.
`node luci-app-xwrt/test/mobile.js` goes further and measures: it renders every
view and every dialog with the same data, writes them out as HTML, and lays
them out in Chromium at 390 CSS pixels — in both languages, with the two kinds
of tab strip LuCI themes build — asserting that nothing runs off the screen,
that the tabs wrap onto as many rows as they need instead of scrolling
sideways, and that the desktop layout is untouched. Three theme habits are
imitated, not imported: the two ways a tab strip is kept on one line, and the
way an information icon is drawn in front of help text — left padding on a box
that is already `width: 100%`, sized so the padding lands outside it. That last
one put every help sentence on the Settings page off the right of a real phone,
and the harness had been hiding it by setting `box-sizing: border-box` on
everything for its own convenience. Turkish is measured first
because it is the longer language here and overflows first. It needs Playwright
(`npm i -g playwright`) and skips without it. `XWRT_SHOT=<dir>` also writes the
pages out as images, which is worth doing once in a while: the first run of it
found a label printed in front of an empty table's message, which every
measurement had passed.

**The uninstaller tears down before it deletes.** Removing the files is the
easy half; the half that matters is making sure nothing the daemon put in the
kernel outlives it. Capture rules pointing at a core that is gone black-hole
every LAN connection, and the binary that would have explained that has just
been deleted. It used to be stop-then-delete, which relies on the stop
finishing — and the stop does real work (reverting the firewall, putting DNS
back, restarting dnsmasq, waiting for the core) inside procd's patience, five
seconds by default. When it did not finish in time the daemon was killed
halfway and the rules, the tunnel device and the core were all still there
afterwards. On an upgrade that is invisible, because the next start clears
them; on an uninstall there is no next start. So the service now asks procd for
thirty seconds, and the uninstaller disconnects first — while everything still
exists and nothing is timing it — then stops, then waits for the process to
really be gone, then removes by hand whatever is still in the kernel, and only
then deletes anything. If something is still there at the end it says so, names
it, prints the recovery commands and exits non-zero. `sh uninstall-cycle.sh` in
the bundle runs the whole cycle on the router and puts xwrt back afterwards.

## What it runs on

The daemon is a single static binary with no C in it and nothing outside the Go
standard library, so the floor is not a library version but what the device
has:

| | |
|---|---|
| OpenWrt | 21.02 and newer for the prebuilt bundle. The firewall is driven through fw4/nftables (22.03+) or fw3/iptables, whichever the device has. |
| Building in-tree | 24.10 and newer. The packages feed's Go is 1.21 on 23.05 and this needs 1.22 — the API server uses method patterns in `net/http`. On 23.05 the prebuilt bundle works; the in-tree build does not. |
| Hardware | 128 MB of RAM and 128 MB of writable storage. The daemon says so at startup on a device with less, and keeps running. |
| Needs | `xray-core`, `ip-full`; `kmod-tun` and `hev-socks5-tunnel` for mixed and TUN modes; `kmod-nft-tproxy` for tproxy mode. The package depends on all of them. |

`./build.sh` cross-compiles for aarch64, x86_64, armv7, mipsel, mips64el and
riscv64 — the architectures of devices that meet the memory floor. The 32 and
64 MB classes are deliberately absent. Building in-tree builds for whatever
target the buildroot is configured for, which is the same set plus anything
else Go supports.

Verified on a real router: an ipq807x device (aarch64) on a 25.12-based build,
kernel 6.12, fw4, LTE upstream. Everything else in that table is what the code
requires, not what has been run.

**Updating itself.** The daemon asks GitHub's release page once a day whether
a newer version exists — or never, if `update_check` is off — and the About
page shows the answer with a button. That button does what the tarball's
installer does, because it *is* the tarball's installer: the release is
downloaded, checked against the `sha256sums` file published with it, unpacked,
and handed to `install.sh`, which runs its own pre-flight, keeps the previous
binary, rolls back if the new one cannot serve, and reconnects a tunnel that
was up. A release with no checksum file, or none for this architecture, is
reported but not offered — there is no point having an updater that installs
whatever it is given. The installer is started in a session of its own, because
the first thing it does is stop the service that started it.

`release.sh` writes `sha256sums` next to the bundles and says so: upload it
with them or in-place updating will refuse the release. `sh test/update.sh`
runs the whole check against a release page served out of a directory — newer,
same, older, and newer-but-unverifiable — and `internal/update`'s own tests
cover the download: a bundle whose checksum does not match is refused and
deleted rather than installed.

**Two ways in, one thing installed.** `release.sh`'s tarball is for testing on
a device that already runs OpenWrt; `package/xwrt/Makefile` and
`luci-app-xwrt/Makefile` build it into a firmware image:

```sh
echo "src-link xwrt /path/to/xwrt" >> feeds.conf.default
./scripts/feeds update xwrt && ./scripts/feeds install -a -p xwrt
make menuconfig     # Network -> xwrt, LuCI -> 3. Applications -> luci-app-xwrt
```

The package depends on everything a capture mode needs — `xray-core`,
`ip-full`, `kmod-tun`, `hev-socks5-tunnel`, `kmod-nft-tproxy` — rather than
listing them in its description as something to install afterwards, which is a
way of saying an image can be built with xwrt on it and no way to run a tunnel.
`sh test/package.sh` compares the two installation paths against each other:
the same files, the same version (both Makefiles read the `VERSION` file the
binary is stamped from), the dependencies present, and the teardown wired into
both removal paths. Removing the package runs the same
`/usr/libexec/xwrt-teardown` the tarball's uninstaller does — one copy of that
sequence, because two copies is how the package manager's path stayed broken
after the other one was fixed. `sh test/teardown.sh` checks the half of it that
kills things: that it ends the daemon and the core this daemon started, and
leaves alone an unrelated `xray` and anything that merely mentions our paths.
It used to use `pkill -f`, which matched the shell running the test suite.

**Installing and uninstalling are done for real.** `sh test/install.sh` builds
a bundle, installs it into `/usr/sbin`, `/etc` and `/www`, upgrades over
itself, uninstalls, and reinstalls — checking at each step that every shipped
file landed, that the binary on disk is the one in the bundle, that an upgrade
keeps the existing configuration, that the uninstall leaves nothing behind
except the configuration it says it keeps, and that it removes its own
firewall zone and no other. It writes to real absolute paths, so it refuses to
run on an OpenWrt device and needs root; `uci` is stubbed and what the stub was
asked to do is part of what is checked. The version comparison in it is not a
formality: `release.sh` used to package whatever binary happened to be lying in
`dist/`, and three bundles went out carrying a week-old daemon under a new
name. `release.sh` builds now, and refuses to package binaries stamped with a
different version.

**The subscription path is driven end to end.** `bash test/subscription.sh`
starts the real daemon against a scratch config directory, serves a listing
over HTTP, and then adds, refreshes and deletes a subscription while checking
what survives: a renamed node keeps its id and its pinned certificate, a node
the listing dropped is removed along with its membership in any group, a URL
that cannot be read is reported instead of stored, and deleting a subscription
takes its servers and nobody else's. Every one of those is a silent wrong list
when it breaks, never an error. It needs python3 and skips without it.

**Firewall rules can be applied for real.** `XWRT_LIVE_FW=1 go test ./internal/fw/`
loads each mode's rules into the running kernel, reads back what the kernel
made of them, and checks that teardown leaves nothing behind and that
reconnecting does not accumulate duplicates. It also checks the exemptions —
an exempt client by MAC, an exempt destination network — against the kernel's
own listing, including their position: a `return` placed after the rule that
captures the packet is a setting that does nothing, which is invisible in the
configuration and plain in the listing. It needs root and mutates the host's
firewall, so run it in a container or a namespace.

**Every protocol is made to carry data.**
`XWRT_LIVE_CORE=1 XRAY_BIN=/path/to/xray go test ./internal/xray/ -run Matrix`
runs the real core twice — once as a server with the inbound under test, once
as the client we configure from a share link — and then reads a page through
the tunnel. The assertion is the payload, not the JSON: VLESS over TCP, WS,
gRPC, XHTTP, HTTPUpgrade, mKCP and REALITY with Vision flow, VMess over TCP
and WS, Trojan, and Shadowsocks. Transports the installed core has removed
(HTTP/2, QUIC, the mKCP header and seed) are asserted from the other side:
the config must be refused while it is being built, with a sentence naming
the transport, rather than by a core that dies on startup.

Two details in that harness are the whole reason it means anything. The first
is a control that must fail: the server is given a different credential than
the client, and if a page still arrives, the test says so and fails. The
second is why it was needed. The target first listened on `127.0.0.1`, and
every row passed — including the control, because loopback is a private range
and the generated config sends private ranges out through `direct`. Nothing
had gone through a tunnel at all. The target now listens on `203.0.113.1`,
TEST-NET-3: reachable on this machine, reserved by the RFC, and to the routing
rules indistinguishable from the open internet.

Being opt-in has a cost worth knowing about: two of the firewall assertions
had gone stale against a later change and nobody found out, because a test
that is never run is a comment. Run them when the firewall code changes.

### What is still unverified

VLESS over TCP and WS have carried real traffic on a real router; the rest of
the matrix has carried real traffic only between two cores on one machine,
which exercises the configuration and the transport but not the path to a
distant server. The firewall rules are known to load, unload and sit in the
right order in a real kernel, but no packet has been followed through them
from a LAN client to the internet and back except by hand. Try TUN and mixed
modes first on a device you can reach another way.

To run the daemon off-router, point it at a scratch config directory:

```sh
XWRT_CONFDIR=/tmp/xwrt-config go run ./cmd/xwrt daemon -no-restore
XWRT_CONFDIR=/tmp/xwrt-config go run ./cmd/xwrt status
```

## Status

Working: profile, group and subscription management, share link import, config
generation for all supported transports, all four capture modes on both
firewall backends, health-aware failover, routing exceptions, live traffic and
connection monitoring, DNS integration, the CLI, the LuCI app, and builds for
the six architectures listed above.

Not done yet: a signed update mechanism, and per-client default policies
(a rule can already scope to a client, but there is no "this client never uses
the VPN" switch separate from the rule list).

The honest caveat stays: everything is verified against the real tools but
nothing has carried real traffic. Test on a device you can reach over serial or
a second path before relying on it remotely.
