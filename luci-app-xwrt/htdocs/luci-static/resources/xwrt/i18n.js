'use strict';
'require baseclass';

// The interface in two languages, chosen by the language the router's web
// interface is already set to.
//
// LuCI's own translations are compiled catalogs (.lmo) produced by the OpenWrt
// buildsystem from .po files. xwrt is installed from a tarball, with no
// buildsystem anywhere near it, so there is nothing to compile them with and
// nothing to install them into. The catalog therefore lives here, as data, and
// is applied by wrapping the global _() that every view already calls.
//
// The wrapping is deliberate and narrow: _() is asked first, so a real .lmo
// catalog — if this app is ever built the OpenWrt way — wins over this file and
// nothing here has to be removed. Only strings LuCI does not know are looked up
// below, and a string that is in neither is returned as written, which is why
// the source text is English: an untranslated string is then simply English
// rather than a missing one.
//
// Failures used to be excluded from all of this, on the grounds that they are
// the machine's own words and half-translating them leaves an error box in two
// languages at once. That reasoning was right about the risk and wrong about
// the conclusion: an error is the one screen a reader most needs to
// understand, and telling someone whose interface is in Turkish that their
// certificate pin no longer matches — in English — helps nobody.
//
// What made it safe was giving every failure a code (see internal/daemon/
// messages.go). The daemon sends `code`, the values that code interpolates,
// and its English sentence; ERRORS and HINTS below translate by code, never by
// matching English text, so rewording a message on the daemon side cannot
// silently drop a translation. A code with no entry here falls back to the
// English sentence, and a Go test refuses to let a code ship without one.
//
// Still not translated, on purpose: the `detail` block. That is the core's or
// the kernel's own output, quoted verbatim — it is what gets searched for and
// pasted into a bug report, and a translated copy of it would be a different
// string from the one the reader can look up.
var TR = {
	"fastest server, with failover":
		"en hızlı sunucu, yedeklemeli",
	"steadiest server, with failover":
		"en kararlı sunucu, yedeklemeli",
	"random, no failover":
		"rastgele, yedekleme yok",
	"in turn, no failover":
		"sırayla, yedekleme yok",
	"The tunnel could not be measured: the core is not answering on its SOCKS port.":
		"Tünel ölçülemedi: çekirdek SOCKS portunda yanıt vermiyor.",
	"Only the tunnel could be measured; the direct connection failed, so there is nothing to compare it with.":
		"Yalnızca tünel ölçülebildi; doğrudan bağlantı başarısız olduğu için karşılaştıracak bir değer yok.",
	"Of the %d connections tried through the tunnel, %d failed outright. The %d ms is the timing of the ones that worked.":
		"Tünel üzerinden denenen %d bağlantıdan %d tanesi tamamen başarısız oldu. %d ms, çalışanların süresidir.",
	"Of the %d connections tried through the tunnel, %d failed outright.":
		"Tünel üzerinden denenen %d bağlantının %d tanesi tamamen başarısız oldu.",
	"Usually %d ms, but some connections reach %d ms. The path to the server is clean, so packets are being lost beyond it — the trouble is on the server's own network, not on this device.":
		"Genelde %d ms, ancak bazı bağlantılar %d ms'ye kadar çıkıyor. Sunucuya giden hat düzgün, yani paketler sunucunun ötesinde kayboluyor — sorun sunucunun kendi ağında, bu cihazda değil.",
	"Usually %d ms, but some connections reach %d ms: packets are being lost and sent again.":
		"Genelde %d ms, ancak bazı bağlantılar %d ms'ye kadar çıkıyor: paketler kaybolup yeniden gönderiliyor.",
	"The tunnel adds %d ms to every connection, which is a lot: the server is probably far away or busy.":
		"Tünel her bağlantıya %d ms ekliyor, bu fazla: sunucu muhtemelen uzak ya da yoğun.",
	"Healthy: the tunnel adds %d ms and behaves consistently.":
		"Sağlıklı: tünel %d ms ekliyor ve tutarlı davranıyor.",
	"Through the tunnel: %d ms, and steady. There is nothing to compare it with, because the router's own traffic goes through the tunnel too.":
		"Tünelden: %d ms, ve kararlı. Karşılaştıracak bir değer yok, çünkü routerin kendi trafiği de tünelden geçiyor.",
	"Through the tunnel: usually %d ms, reaching %d ms. There is nothing to compare it with, because the router's own traffic goes through the tunnel too.":
		"Tünelden: genelde %d ms, %d ms'ye kadar çıkıyor. Karşılaştıracak bir değer yok, çünkü routerin kendi trafiği de tünelden geçiyor.",
	"Start on boot":
		"Otomatik başlat",
	"on":
		"açık",
	"off":
		"kapalı",
	"the tunnel will not come up on its own":
		"tünel kendiliğinden açılmaz",
	"Only the tunnel was measured.":
		"Yalnızca tünel ölçüldü.",
	"The router's own traffic is proxied too, so the connection this test would use as its unproxied reference goes through the tunnel as well. Measuring it anyway would show the tunnel twice under two names. Turn that setting off for a moment to get the comparison.":
		"Routerin kendi trafiği de tünelden geçiyor; yani bu testin karşılaştırma ölçüsü olarak kullanacağı doğrudan bağlantı da tünelden gidiyor. Yine de ölçseydik, tüneli iki farklı isim altında iki kez göstermiş olurduk. Karşılaştırmayı görmek için o ayarı bir süreliğine kapatın.",
	"configuration":
		"yapılandırma",
	"proxy core":
		"proxy çekirdeği",
	"TUN device":
		"TUN aygıtı",
	"firewall rules":
		"güvenlik duvarı kuralları",
	"DNS":
		"DNS",
	"network detection":
		"ağ algılama",
	"disconnect":
		"bağlantı kesme",
	"subscription":
		"abonelik",
	"service":
		"servis",
	"core":
		"çekirdek",
	"tunnel":
		"tünel",
	"bypass the VPN":
		"VPN'i atla",
	"force through the VPN":
		"VPN'den geçmeye zorla",
	"block":
		"engelle",
	"domains":
		"alan adları",
	"addresses":
		"adresler",
	"clients":
		"istemciler",
	"protocols":
		"protokoller",
	"port":
		"port",
	"source port":
		"kaynak port",
	"No entries match these filters.":
		"Bu filtrelere uyan kayıt yok.",
	"repeated %d more times":
		"%d kez daha yinelendi",
	"No errors have occurred since the service started.":
		"Servis başladığından beri hiçbir hata oluşmadı.",
	"Could not clear: %s":
		"Temizlenemedi: %s",
	"Level":
		"Düzey",
	"everything, including debug":
		"her şey, debug dahil",
	"info and above":
		"bilgi ve üstü",
	"warnings and errors":
		"uyarılar ve hatalar",
	"errors only":
		"yalnızca hatalar",
	"Component":
		"Bileşen",
	"all components":
		"tüm bileşenler",
	"Step":
		"Adım",
	"all steps":
		"tüm adımlar",
	"Log":
		"Günlük",
	"Kept in memory only; nothing is written to flash. Warnings and errors are also copied to the system log, where they survive a restart of the service — read them with <code>logread -e xwrt</code>.":
		"Yalnızca bellekte tutulur, flash belleğe hiçbir şey yazılmaz. Uyarılar ve hatalar ayrıca sistem günlüğüne kopyalanır; servis yeniden başlasa bile orada kalır — <code>logread -e xwrt</code> ile okuyabilirsiniz.",
	"Errors":
		"Hatalar",
	"Kept longer than the stream below; each one carries the output underneath it and what to do about it.":
		"Aşağıdaki akıştan daha uzun süre saklanır; her hatanın altındaki çıktıyı ve ne yapılması gerektiğini içerir.",
	"%d error records deleted.":
		"%d hata kaydı silindi.",
	"Clear":
		"Temizle",
	"Stream":
		"Akış",
	"Jump to the end":
		"En alta git",
	"%d lines deleted. The core writes a line for every connection, so the stream fills up again immediately.":
		"%d satır silindi. Çekirdek her bağlantı için bir satır yazdığından akış hemen yeniden dolar.",
	"Clear the stream":
		"Akışı temizle",
	"Lowest ping — fastest server, with failover":
		"En düşük ping — en hızlı sunucu, yedeklemeli",
	"Least loaded — steadiest server, with failover":
		"En az yük — en kararlı sunucu, yedeklemeli",
	"Random — spreads the load, no failover":
		"Rastgele — yükü dağıtır, yedekleme yok",
	"In turn — uses the servers one after another, no failover":
		"Sırayla — sunucuları sırasıyla kullanır, yedekleme yok",
	"Add a few servers first.":
		"Önce birkaç sunucu ekleyin.",
	"Edit group":
		"Grubu düzenle",
	"New group":
		"Yeni grup",
	"Name":
		"Ad",
	"Strategy":
		"Strateji",
	"Only the failover strategies notice that a server has gone down. Random and in-turn keep sending connections to a dead one.":
		"Sunucunun düştüğünü yalnızca yedeklemeli stratejiler fark eder. Rastgele ve sırayla seçenekleri, ölü bir sunucuya bağlantı göndermeyi sürdürür.",
	"Health check":
		"Sağlık yoklaması",
	"every 30 seconds":
		"30 saniyede bir",
	"every minute (recommended)":
		"dakikada bir (önerilen)",
	"every 2 minutes":
		"2 dakikada bir",
	"every 3 minutes":
		"3 dakikada bir",
	"every 5 minutes":
		"5 dakikada bir",
	"Each member is asked for a page with an empty body. This interval is also how long a server that has died keeps being used, so it is the worst case for how long you are offline. It costs nothing worth counting; the failover strategies are the only ones that use it.":
		"Her üyeden gövdesi boş bir sayfa istenir. Bu aralık aynı zamanda ölen bir sunucunun kullanılmaya devam etme süresidir; yani internetsiz kalacağın en kötü süre budur. Maliyeti sayılmaya değmez; yalnızca yedeklemeli stratejiler kullanır.",
	"Members":
		"Üyeler",
	"Cancel":
		"Vazgeç",
	"Select at least one server.":
		"En az bir sunucu seçin.",
	"Group":
		"Grup",
	"The group could not be saved: %s":
		"Grup kaydedilemedi: %s",
	"Save":
		"Kaydet",
	"Create":
		"Oluştur",
	"Delete the group \"%s\"? The servers in it are kept.":
		"\"%s\" grubu silinsin mi? İçindeki sunucular korunur.",
	"Could not delete: %s":
		"Silinemedi: %s",
	"Add config":
		"Config ekle",
	"Paste one or more configs, one per line.":
		"Bir ya da daha çok config'i, her satıra bir tane olacak şekilde yapıştırın.",
	"There is nothing to import.":
		"İçe aktarılacak bir şey yok.",
	"%d servers added, %d skipped.":
		"%d sunucu eklendi, %d tanesi atlandı.",
	"Could not import: %s":
		"İçe aktarılamadı: %s",
	"Import":
		"İçe aktar",
	"Optional name":
		"İsteğe bağlı ad",
	"Add subscription":
		"Abonelik ekle",
	"Address (URL)":
		"Adres (URL)",
	"An address (URL) is required.":
		"Bir adres (URL) gerekli.",
	"Fetching…":
		"Alınıyor…",
	"Downloading the server list":
		"Sunucu listesi indiriliyor",
	"Subscription failed: %s":
		"Abonelik başarısız: %s",
	"Add":
		"Ekle",
	"Connecting…":
		"Bağlanılıyor…",
	"Starting the core and applying the rules":
		"Çekirdek başlatılıyor ve kurallar uygulanıyor",
	"Could not connect: %s":
		"Bağlanılamadı: %s",
	"measuring…":
		"ölçülüyor…",
	"unreachable":
		"erişilemiyor",
	"error":
		"hata",
	"Reading the certificate…":
		"Sertifika okunuyor…",
	"Connecting to %s":
		"%s sunucusuna bağlanılıyor",
	"%s will now be accepted only with this certificate.":
		"%s artık yalnızca bu sertifikayla kabul edilecek.",
	"Fingerprint":
		"Parmak izi",
	"Issued by %s, valid until %s":
		"%s tarafından verildi, %s tarihine kadar geçerli",
	"No public authority vouches for this certificate. On a self-signed server that is normal — but check that the issuer above is the party you expect: if something is sitting between you and the server right now, its certificate is the one that would be pinned.":
		"Bu sertifikaya kefil olan genel bir otorite yok. Kendinden imzalı bir sunucuda bu normaldir — ama yukarıdaki vericinin beklediğiniz taraf olduğunu doğrulayın: şu anda bağlantının arasına giren bir şey varsa, sabitlenen onun sertifikası olurdu.",
	"The certificate is publicly trusted, but it was issued for a name other than the SNI this profile uses (%s). That is a common arrangement; the pinning itself is what does the work.":
		"Sertifika genel olarak güvenilir, ancak bu profilin kullandığı SNI'dan (%s) başka bir ad için verilmiş. Bu olağan bir düzendir; işi yürüten şey sabitlemenin kendisidir.",
	"The certificate is publicly trusted and matches the SNI in use.":
		"Sertifika genel olarak güvenilir ve kullanılan SNI ile eşleşiyor.",
	"Full chain":
		"Tam zincir",
	"Certificate":
		"Sertifika",
	"Issuer":
		"Sertifikayı veren",
	"Issued by %s, expires %s":
		"%s tarafından verildi, %s tarihinde doluyor",
	"OK":
		"Tamam",
	"Certificate pinned":
		"Sertifika sabitlendi",
	"Could not read the certificate: %s":
		"Sertifika okunamadı: %s",
	"Delete the server \"%s\"?":
		"\"%s\" sunucusu silinsin mi?",
	"Refreshing…":
		"Yenileniyor…",
	"The subscription has %d servers.":
		"Abonelikte %d sunucu var.",
	"Could not refresh: %s":
		"Yenilenemedi: %s",
	"Delete the subscription \"%s\" and every server in it?":
		"\"%s\" aboneliği ve içindeki tüm sunucular silinsin mi?",
	"Connected right now":
		"Şu anda bağlı",
	"manual":
		"elle",
	"Connect":
		"Bağlan",
	"Test":
		"Test et",
	"Certificate pinned. Read it again if the server has renewed it.":
		"Sertifika sabitlendi. Sunucu sertifikayı yenilediyse yeniden okuyun.",
	"Save the server's certificate instead of skipping verification":
		"Doğrulamayı atlamak yerine sunucunun sertifikasını kaydet",
	"Pin again":
		"Yeniden sabitle",
	"Pin the certificate":
		"Sertifikayı sabitle",
	"Delete":
		"Sil",
	"No servers yet. Paste a config to get started.":
		"Henüz sunucu yok. Başlamak için bir config yapıştırın.",
	"Endpoint":
		"Uç nokta",
	"Transport":
		"Taşıma",
	"Source":
		"Kaynak",
	"Latency":
		"Gecikme",
	"(deleted)":
		"(silinmiş)",
	"Edit":
		"Düzenle",
	"No groups. A group holds several servers at once and, with a failover strategy, moves off one that stops answering.":
		"Grup yok. Bir grup aynı anda birden çok sunucuyu tutar ve yedeklemeli bir stratejiyle, yanıt vermeyi bırakan sunucudan çıkar.",
	"never":
		"hiç",
	"Refresh":
		"Yenile",
	"No subscriptions.":
		"Abonelik yok.",
	"Servers":
		"Sunucular",
	"Updated":
		"Güncellendi",
	"Groups":
		"Gruplar",
	"Subscriptions":
		"Abonelikler",
	"Add servers by pasting configs, or from a subscription address.":
		"Config yapıştırarak ya da bir abonelik adresiyle sunucu ekleyin.",
	"e.g. Bank":
		"örn. Banka",
	"Bypass the VPN — go out over the normal connection":
		"VPN'i atla — normal bağlantıdan çık",
	"Block — drop the traffic":
		"Engelle — trafiği düşür",
	"any":
		"fark etmez",
	"Edit rule":
		"Kuralı düzenle",
	"New rule":
		"Yeni kural",
	"Action":
		"Eylem",
	"A bypass rule also has the router resolve these names itself, so the site is reached over the normal connection rather than at an address that only exists at the far end of the tunnel.":
		"Atlama kuralı, bu adların çözümlenmesini de yönlendiriciye yaptırır; böylece siteye, tünelin öbür ucunda bulunan bir adres yerine normal bağlantı üzerinden ulaşılır.",
	"Domains":
		"Alan adları",
	"One per line. A bare name matches that name and its subdomains, and nothing else. Prefixes: full: (that name alone), keyword: (anywhere in the name), regexp:, geosite: (needs the geo data package).":
		"Her satıra bir tane. Düz yazılan bir ad, o adı ve alt alan adlarını kapsar, başka hiçbir şeyi değil. Ön ekler: full: (yalnızca o ad), keyword: (adın herhangi bir yerinde geçen), regexp:, geosite: (geo veri paketi gerekir).",
	"Addresses":
		"Adresler",
	"One network or address per line.":
		"Her satıra bir ağ ya da adres.",
	"Clients":
		"İstemciler",
	"Apply the rule only to these LAN clients. Leave empty for all of them.":
		"Kuralı yalnızca bu LAN istemcilerine uygula. Tümü için boş bırakın.",
	"Ports":
		"Portlar",
	"Protocols":
		"Protokoller",
	"One per line: http, tls, quic or bittorrent. Detected from the first packet of the connection.":
		"Her satıra bir tane: http, tls, quic ya da bittorrent. Bağlantının ilk paketine bakılarak algılanır.",
	"Network":
		"Ağ",
	"The rule was not accepted: %s":
		"Kural kabul edilmedi: %s",
	"Delete the rule \"%s\"?":
		"\"%s\" kuralı silinsin mi?",
	"matches nothing":
		"hiçbir şeyle eşleşmiyor",
	"Move up":
		"Yukarı taşı",
	"Move down":
		"Aşağı taşı",
	"Enable":
		"Etkinleştir",
	"Disable":
		"Devre dışı bırak",
	"No rules. Everything outside the local network goes through the VPN.":
		"Kural yok. Yerel ağ dışındaki her şey VPN'den geçiyor.",
	"Matches":
		"Eşleşenler",
	"Routing rules":
		"Yönlendirme kuralları",
	"Exceptions to the default of sending everything through the VPN. Rules are tried in order and the first match wins, so put the narrow ones above the broad ones.":
		"Her şeyi VPN'den geçiren varsayılan davranışın istisnaları. Kurallar sırayla denenir ve ilk eşleşen kazanır; bu yüzden dar kuralları geniş olanların üstüne koyun.",
	"xDivine-Ray settings":
		"xDivine-Ray ayarları",
	"Changing these settings reconnects the active session.":
		"Bu ayarları değiştirmek etkin oturumu yeniden bağlar.",
	"General":
		"Genel",
	"Routing":
		"Yönlendirme",
	"Advanced":
		"Gelişmiş",
	"Capture mode":
		"Yakalama modu",
	"How client traffic reaches the proxy core. NAT redirection cannot carry UDP, so every mode that proxies UDP uses either TPROXY or the TUN device.":
		"İstemci trafiğinin proxy çekirdeğine nasıl ulaştığı. NAT yönlendirmesi (redirect) UDP taşıyamaz; bu yüzden UDP'yi proxy'leyen her mod ya TPROXY'yi ya da TUN aygıtını kullanır.",
	"Redirect — TCP only (UDP is not proxied, QUIC leaks)":
		"Redirect — yalnızca TCP (UDP proxy'lenmez, QUIC sızar)",
	"Mixed — TCP by redirect, UDP through the TUN device (recommended)":
		"Mixed — TCP redirect ile, UDP TUN üzerinden (önerilen)",
	"TPROXY — TCP and UDP in the kernel, needs the tproxy module":
		"TPROXY — çekirdekte TCP ve UDP, tproxy modülü gerekir",
	"TUN — everything through the tunnel":
		"TUN — her şey tünelden",
	"Proxy the router's own traffic as well":
		"Yönlendiricinin kendi trafiğini de proxy'le",
	"Capture not only forwarded LAN traffic but what the router itself produces. With this on, the device's own business — package updates, the DDNS client, NTP — goes through the tunnel too: sometimes that is the point, and sometimes it is how you lose remote access.":
		"Yalnızca yönlendirilen LAN trafiğini değil, yönlendiricinin kendi ürettiği trafiği de yakala. Açtığınızda paket güncellemeleri, DDNS istemcisi, NTP gibi cihazın kendi işleri de tünelden geçer — bu bazen istenen şeydir, bazen de uzaktan erişiminizi kaybetmenizin sebebi.",
	"Open SOCKS/HTTP to the LAN":
		"SOCKS/HTTP'yi LAN'a aç",
	"Besides transparent capture, let LAN clients use the SOCKS and HTTP inbounds directly.":
		"Şeffaf yakalamanın yanı sıra, LAN istemcilerinin SOCKS ve HTTP girişlerini doğrudan kullanmasına izin ver.",
	"Connect on startup":
		"Açılışta bağlan",
	"Connect to the last used server once the upstream link comes up. With this off the service never dials out on its own — not after a reboot, not after a restart — and you connect when you want to. On a line that has no internet except through the tunnel, leave it on.":
		"Üst bağlantı geldiğinde en son kullanılan sunucuya bağlanır. Bu kapalıyken servis kendiliğinden hiç bağlanmaz — ne yeniden başlatmadan sonra, ne servis yeniden başladığında — bağlantıyı siz istediğinizde kurarsınız. Tünel olmadan internete çıkmayan bir hatta açık bırakın.",
	"Core log level":
		"Çekirdek günlük düzeyi",
	"none":
		"yok",
	"warning":
		"uyarı",
	"info":
		"bilgi",
	"debug":
		"ayrıntılı",
	"DNS handling":
		"DNS yönetimi",
	"Resolving a name takes two steps: the client asks the router, and the router asks its own upstream DNS server. This setting decides the second step — the one that matters. If you are unsure, pick the first; it is the only option that works in every capture mode.":
		"Bir ad çözülürken iki adım vardır: istemci yönlendiriciye sorar, yönlendirici de kendi üst DNS sunucusuna. Bu ayar ikinci adımı belirler — yani asıl önemli olanı. Emin değilseniz ilkini seçin; her yakalama modunda çalışan tek seçenek odur.",
	"Send dnsmasq to the core — works in every mode (recommended)":
		"dnsmasq'ı çekirdeğe yönlendir — her modda çalışır (önerilen)",
	"Capture port 53 only — for clients that set their own DNS":
		"Yalnızca 53. portu yakala — kendi DNS'ini yazan istemciler için",
	"Leave DNS alone — queries go to your ISP":
		"DNS'e dokunma — sorgular operatöre gider",
	"Send dnsmasq to the core":
		"dnsmasq'ı çekirdeğe yönlendir",
	"dnsmasq hands the queries to the core, and the core sends them through the tunnel. Local names (DHCP names, .lan) keep working and no query leaks to your ISP. <strong>Required in TUN mode.</strong>":
		"dnsmasq sorguları çekirdeğe, oradan da tünele verir. Yerel adlar (DHCP isimleri, .lan) çalışmaya devam eder, hiçbir sorgu operatöre sızmaz. <strong>TUN modunda zorunludur.</strong>",
	"Capture port 53 only":
		"Yalnızca 53. portu yakala",
	"This is not a variant of the one above, it does something else entirely: it sends only the clients that have typed in a DNS server of their own (8.8.8.8 and the like) to the core. Clients that ask the router never meet this rule — the router's own address is on the exempt list — so dnsmasq carries on using your ISP's DNS. The result: in redirect and mixed mode your queries go out in the open; <strong>in TUN mode no name resolves at all</strong>, because those queries leave through the tunnel and your ISP's server will not answer a stranger.":
		"Bu, yukarıdakinin bir çeşidi değil, apayrı bir şey yapar: sadece DNS sunucusunu elle yazmış istemcileri (8.8.8.8 gibi) çekirdeğe yönlendirir. Yönlendiriciyi soran istemciler bu kurala hiç uğramaz — yönlendiricinin kendi adresi muaf listesindedir — dolayısıyla dnsmasq operatörün DNS'ini kullanmaya devam eder. Sonucu: redirect ve karma modda sorgularınız açıkta gider; <strong>TUN modunda ise hiçbir isim çözülmez</strong>, çünkü o sorgular tünelin içinden çıkar ve operatörün sunucusu yabancı bir adrese yanıt vermez.",
	"Leave DNS alone":
		"DNS'e dokunma",
	"The router's DNS configuration is left exactly as it is. This is the right choice if you have set up your own resolver (AdGuard, Unbound, DoH) and you are sure it leaves through the tunnel.":
		"Yönlendiricinin DNS yapılandırması olduğu gibi kalır. Kendi çözümleyicinizi (AdGuard, Unbound, DoH) kurduysanız ve onun tünelden çıktığından eminseniz doğru seçim budur.",
	"Upstream DNS server":
		"Üst DNS sunucusu",
	"Queries are sent here over TCP through the proxy.":
		"Sorgular proxy üzerinden TCP ile buraya gönderilir.",
	"DNS inbound port":
		"DNS giriş portu",
	"Exempt networks":
		"Muaf ağlar",
	"Destinations that never go through the proxy. LAN networks and private ranges are added on their own.":
		"Hiçbir zaman proxy'den geçmeyecek hedefler. LAN ağları ve özel aralıklar kendiliğinden eklenir.",
	"Exempt clients":
		"Muaf istemciler",
	"LAN clients to leave outside the transparent proxy, by MAC address.":
		"Şeffaf proxy'nin dışında bırakılacak LAN istemcileri, MAC adresine göre.",
	"LAN device":
		"LAN aygıtı",
	"Leave on automatic unless the Status page shows the wrong device. This is the interface LAN clients arrive on — usually the bridge.":
		"Durum sayfası yanlış aygıtı göstermiyorsa otomatikte bırakın. Bu, LAN istemcilerinin geldiği arayüzdür — genelde köprü (bridge).",
	"automatic":
		"otomatik",
	"WAN device":
		"WAN aygıtı",
	"Leave on automatic unless the Status page shows the wrong device. This is the interface that reaches the internet.":
		"Durum sayfası yanlış aygıtı göstermiyorsa otomatikte bırakın. Bu, internete çıkan arayüzdür.",
	"%s — detected now":
		"%s — şu an algılanan",
	"Resolve IPv6 addresses":
		"IPv6 adreslerini çöz",
	"Leaving this off keeps name resolution on IPv4 only, which avoids the broken IPv6 paths many providers have.":
		"Kapalı bırakmak ad çözümlemesini yalnızca IPv4'te tutar; birçok sağlayıcıdaki bozuk IPv6 yollarından böylece kaçınılır.",
	"Xray binary":
		"Xray dosyası",
	"hev-socks5-tunnel binary":
		"hev-socks5-tunnel dosyası",
	"Required in mixed and TUN modes.":
		"Mixed ve TUN modlarında gerekli.",
	"SOCKS port":
		"SOCKS portu",
	"HTTP proxy port":
		"HTTP proxy portu",
	"Transparent port":
		"Şeffaf port",
	"Core API port":
		"Çekirdek API portu",
	"Local only (loopback); used to read the traffic counters.":
		"Yalnızca yerel (loopback); trafik sayaçlarını okumak için kullanılır.",
	"Service API port":
		"Servis API portu",
	"Local only (loopback); used by this page and the command line tool.":
		"Yalnızca yerel (loopback); bu sayfa ve komut satırı aracı kullanır.",
	"TUN device name":
		"TUN aygıt adı",
	"If you change this, update the xwrt firewall zone to match, or forwarded traffic is dropped.":
		"Bunu değiştirirseniz xwrt güvenlik duvarı bölgesini de aynı şekilde güncelleyin; yoksa yönlendirilen trafik düşer.",
	"TUN address":
		"TUN adresi",
	"TUN MTU":
		"TUN MTU",
	"Socket mark base":
		"Soket işareti (mark) tabanı",
	"Three marks are derived from this: the core's own socket mark, the TPROXY mark, and the mark that steers mixed-mode UDP into the tunnel.":
		"Buradan üç işaret türetilir: çekirdeğin kendi soket işareti, TPROXY işareti ve karma moddaki UDP'yi tünele yönlendiren işaret.",
	"Routing table":
		"Yönlendirme tablosu",
	"The table that holds the TUN default route.":
		"TUN varsayılan rotasını tutan tablo.",
	"below the %d MB this version targets":
		"bu sürümün hedeflediği %d MB'ın altında",
	"group of %d":
		"%d üyeli grup",
	"%s failed":
		"%s başarısız oldu",
	"Open the log":
		"Günlüğü aç",
	"Dismiss":
		"Kapat",
	"not measured":
		"ölçülmedi",
	"%d errors":
		"%d hata",
	"Measurement":
		"Ölçüm",
	"Median":
		"Ortanca",
	"Best":
		"En iyi",
	"Worst":
		"En kötü",
	"Link to the server":
		"Sunucuya bağlantı",
	"router to server only":
		"yalnızca yönlendirici ile sunucu arası",
	"Direct":
		"Doğrudan",
	"to %s, without the tunnel":
		"%s adresine, tünel kullanılmadan",
	"Through the tunnel":
		"Tünelden",
	"to the same address, through the proxy":
		"aynı adrese, proxy üzerinden",
	"The tunnel costs %d ms":
		"Tünelin maliyeti: %d ms",
	"Tunnel error: %s":
		"Tünel hatası: %s",
	"Measured at %s.":
		"%s tarihinde ölçüldü.",
	"Switch to this server":
		"Bu sunucuya geç",
	"Disconnect":
		"Bağlantıyı kes",
	"Connected":
		"Bağlı",
	"The core is not running":
		"Çekirdek çalışmıyor",
	"Not connected":
		"Bağlı değil",
	"Pick a server first.":
		"Önce bir sunucu seçin.",
	"Connected.":
		"Bağlandı.",
	"Disconnecting…":
		"Bağlantı kesiliyor…",
	"Removing the rules":
		"Kurallar kaldırılıyor",
	"Disconnected.":
		"Bağlantı kesildi.",
	"Could not disconnect: %s":
		"Bağlantı kesilemedi: %s",
	"Testing":
		"Test ediliyor",
	"Measuring the connections; this takes a few seconds.":
		"Bağlantılar ölçülüyor; bu birkaç saniye sürer.",
	"Could not run the test: %s":
		"Test çalıştırılamadı: %s",
	"%d servers":
		"%d sunucu",
	"No servers defined":
		"Tanımlı sunucu yok",
	"Status":
		"Durum",
	"Connected to":
		"Bağlanılan",
	"Mode":
		"Mod",
	"Uptime":
		"Çalışma süresi",
	"Upload":
		"Yükleme",
	"Download":
		"İndirme",
	"LAN devices":
		"LAN aygıtları",
	"not detected":
		"algılanmadı",
	"LAN networks":
		"LAN ağları",
	"WAN device":
		"WAN aygıtı",
	"WAN gateway":
		"WAN ağ geçidi",
	"Firewall":
		"Güvenlik duvarı",
	"Memory":
		"Bellek",
	"Storage":
		"Depolama",
	"%d MB free":
		"%d MB boş",
	"Version":
		"Sürüm",
	"core %s":
		"çekirdek %s",
	"xDivine-Ray":
		"xDivine-Ray",
	"A VPN manager that works out this device's network layout while it runs.":
		"Bu cihazın ağ yapısını çalışma anında algılayan VPN yöneticisi.",
	"Connection":
		"Bağlantı",
	"Speed and latency test":
		"Hız ve gecikme testi",
	"Connects to the same address both directly and through the tunnel, and shows the difference. This is how you see where the delay comes from.":
		"Aynı adrese hem doğrudan hem de tünelden bağlanır ve aradaki farkı gösterir. Gecikmenin nereden geldiğini böyle görürsünüz.",
	"Run the test":
		"Testi çalıştır",
	"Detected network":
		"Algılanan ağ",
	"These values are detected at connect time. Fill one in from Settings only if it is wrong.":
		"Bu değerler bağlanma anında algılanır. Yalnızca bir değer yanlışsa Ayarlar'dan elle girin.",
	"waiting for a measurement…":
		"ölçüm bekleniyor…",
	"as of %s":
		"%s itibarıyla",
	"now":
		"şimdi",
	"Byte counters are on. Volumes appear as new connections are made.":
		"Bayt sayaçları açıldı. Hacimler, yeni bağlantılar kuruldukça görünür.",
	"Could not turn on the byte counters: %s":
		"Bayt sayaçları açılamadı: %s",
	"unknown":
		"bilinmiyor",
	"No active connections from the LAN.":
		"LAN'dan etkin bağlantı yok.",
	"Client":
		"İstemci",
	"Flows":
		"Akışlar",
	"Sent":
		"Gönderilen",
	"Received":
		"Alınan",
	"Nothing active.":
		"Etkin bir şey yok.",
	"Destination":
		"Hedef",
	"Protocol":
		"Protokol",
	"Sent / received":
		"Gönderilen / alınan",
	"Connection tracking is not available on this device.":
		"Bu cihazda bağlantı izleme kullanılamıyor.",
	"The core is tracking connections but not counting bytes, which is why the volumes below show “—”. The flow counts are right either way.":
		"Çekirdek bağlantıları izliyor ama baytları saymıyor; bu yüzden aşağıdaki hacimler “—” görünüyor. Akış sayıları her durumda doğrudur.",
	"Turn on the byte counters":
		"Bayt sayaçlarını aç",
	"Active flows":
		"Etkin akışlar",
	"%d of the %d tracked connections come from the local network.":
		"İzlenen bağlantıların %d / %d tanesi yerel ağdan geliyor.",
	"Traffic":
		"Trafik",
	"The throughput going through the proxy, and the connections from the local network the core is watching right now.":
		"Proxy üzerinden geçen aktarım hızı ve çekirdeğin yerel ağdan şu anda izlediği bağlantılar.",
	"Throughput":
		"Aktarım hızı",
	"The counters come from the proxy core, so the traffic here is the traffic that goes through the VPN. Traffic a rule sends out directly is not counted here.":
		"Sayaçlar proxy çekirdeğinden gelir; yani buradaki trafik VPN'den geçen trafiktir. Bir kuralın doğrudan çıkardığı trafik burada sayılmaz.",
	// Editing a server.
	"Edit server":
		"Sunucuyu düzenle",
	"Address":
		"Adres",
	"Port":
		"Port",
	"UUID":
		"UUID",
	"Password":
		"Şifre",
	"Encryption method":
		"Şifreleme yöntemi",
	"Flow":
		"Akış (flow)",
	"Security":
		"Güvenlik",
	"SNI (server name)":
		"SNI (sunucu adı)",
	"TLS fingerprint (uTLS)":
		"TLS parmak izi (uTLS)",
	"None":
		"Hiçbiri",
	"h3 belongs to QUIC and is dropped on any other transport.":
		"h3, QUIC'e aittir; başka bir taşımada listeden çıkarılır.",
	"REALITY public key":
		"REALITY genel anahtarı",
	"REALITY short ID":
		"REALITY kısa kimliği",
	"Path":
		"Yol (path)",
	"Host header":
		"Host başlığı",
	"gRPC service name":
		"gRPC servis adı",
	"Header camouflage":
		"Başlık kamuflajı",
	"mKCP seed":
		"mKCP tohumu",
	"Note":
		"Not",
	"This server came from a subscription. Refreshing that subscription will overwrite your changes.":
		"Bu sunucu bir abonelikten geldi. O aboneliği yenilediğinizde değişiklikleriniz üzerine yazılır.",
	"Protocol: %s. Changing it is not offered — paste a new config instead.":
		"Protokol: %s. Bunu değiştirmek sunulmuyor — bunun yerine yeni bir config yapıştırın.",
	"Multiplexing (mux)":
		"Çoğullama (mux)",
	"Carries several TCP connections over one. Usually slower; UDP does not need it.":
		"Birden çok TCP bağlantısını tek bağlantıdan taşır. Genelde yavaşlatır; UDP'nin buna ihtiyacı yok.",
	"A name and an address are required.":
		"Ad ve adres zorunlu.",
	"The port has to be between 1 and 65535.":
		"Port 1 ile 65535 arasında olmalı.",
	"Saved.":
		"Kaydedildi.",
	"Saved, but not applied yet: the connection is still running with the previous rules.":
		"Kaydedildi, ama henüz uygulanmadı: bağlantı hâlâ önceki kurallarla çalışıyor.",
	"Apply now":
		"Şimdi uygula",
	"Applied.":
		"Uygulandı.",
	"Could not apply: %s":
		"Uygulanamadı: %s",
	"Could not save: %s":
		"Kaydedilemedi: %s",

	// The About page.
	"About":
		"Hakkında",
	"xDivine-Ray is a transparent proxy manager for OpenWrt: it detects the router's own network layout at run time, so the same package works on a device it has never seen before.":
		"xDivine-Ray, OpenWrt için bir şeffaf proxy yöneticisidir: yönlendiricinin ağ yapısını çalışma anında algılar, böylece aynı paket daha önce hiç görmediği bir cihazda da çalışır.",
	"This installation":
		"Bu kurulum",
	"Proxy core":
		"Proxy çekirdeği",
	"Author":
		"Geliştirici",
	"Project":
		"Proje",
	"Source, releases and the issue tracker.":
		"Kaynak kod, sürümler ve hata bildirimi.",
	"Support the work on this project.":
		"Bu projeye verilen emeğe destek olun.",
	"Buy me a coffee":
		"Bana bir kahve ısmarla",
	"Thank you for your donations.":
		"Bağışlarınız için teşekkürler.",
	"They pay for the hardware this is tested on and the time that goes into it.":
		"Bağışlar, bu işin üzerinde test edildiği donanımı ve ona ayrılan zamanı karşılıyor.",
	"Updates":
		"Güncellemeler",
	"Questions, new releases and the people who use this.":
		"Sorular, yeni sürümler ve bunu kullanan insanlar.",
	"Version %s is available.":
		"%s sürümü çıktı.",
	"You are running %s.":
		"Sizde %s çalışıyor.",
	"%s is the newest version, and it is the one running.":
		"%s en güncel sürüm, ve çalışan da o.",
	"The release page could not be reached.":
		"Sürüm sayfasına ulaşılamadı.",
	"No check has been made yet.":
		"Henüz kontrol yapılmadı.",
	"Last checked: %s":
		"Son kontrol: %s",
	"Check now":
		"Şimdi kontrol et",
	"Install %s":
		"%s sürümünü kur",
	"Install %s now?\n\nThe tunnel goes down while the service restarts, and comes back by itself. If the new version cannot start, the previous one is put back automatically.":
		"%s şimdi kurulsun mu?\n\nServis yeniden başlarken tünel düşer ve kendiliğinden geri gelir. Yeni sürüm başlayamazsa bir önceki otomatik olarak geri yüklenir.",
	"Installing. This page will lose contact with the daemon for a moment; reload it in a minute.":
		"Kuruluyor. Bu sayfa bir süreliğine daemon ile bağlantısını kaybedecek; bir dakika sonra yenileyin.",
	"Installing %s. The service restarts as part of this, so this page will lose contact with it for a moment.":
		"%s kuruluyor. Servis bu sırada yeniden başlıyor, bu yüzden sayfa bir süreliğine bağlantısını kaybedecek.",
	"Progress is written to %s.":
		"İlerleme %s dosyasına yazılıyor.",
	"Release notes":
		"Sürüm notları",
	"Check for updates":
		"Güncellemeleri kontrol et",
	"Ask once a day whether a newer version has been released, and say so on the About page. Nothing is ever installed without you pressing the button; this only decides whether the question is asked at all.":
		"Günde bir kez yeni bir sürüm çıkmış mı diye sorar ve Hakkında sayfasında söyler. Siz düğmeye basmadan hiçbir şey kurulmaz; bu yalnızca sorunun sorulup sorulmayacağına karar verir.",
	"Update source":
		"Güncelleme kaynağı",
	"The GitHub repository whose releases are offered, as owner/name. Change it only if you install from a fork.":
		"Sürümleri önerilecek GitHub deposu, owner/name biçiminde. Yalnızca bir fork'tan kuruyorsanız değiştirin.",
	"%s is available":
		"%s çıktı",
	"Built on":
		"Üzerine kurulduğu projeler",
	"the proxy core: the protocols, the routing and the TLS.":
		"proxy çekirdeği: protokoller, yönlendirme ve TLS.",
	"the TUN device, in mixed and TUN modes.":
		"karma ve TUN modlarındaki TUN aygıtı.",
	"the system this runs on, and the interface it lives in.":
		"üzerinde çalıştığı sistem ve içinde yaşadığı arayüz.",

	// Menu entries. The titles come from menu.d, which the server renders
	// before any of this runs; translateMenu() below replaces them in place.
	"Rules":
		"Kurallar",
	"Settings":
		"Ayarlar"
};

var CATALOGS = { tr: TR };

// The menu is rendered by the server from menu.d, which has no access to this
// catalog, so its entries are translated in place. Only xwrt's own entries are
// touched, and only when their text is still the English the JSON file shipped.
var MENU = {
	status: 'Status',
	profiles: 'Servers',
	rules: 'Rules',
	traffic: 'Traffic',
	settings: 'Settings',
	logs: 'Log',
	about: 'About'
};

var LANG = null;

// language returns 'tr' or 'en'. The router's web interface language comes
// first — that is the setting the user actually chose — then the language the
// page was rendered in, then the browser's.
function detect() {
	var raw = '';
	try {
		if (typeof L != 'undefined' && L.env)
			raw = L.env.lang || L.env.language || '';
	} catch (e) {}
	try {
		if (!raw && typeof document != 'undefined' && document.documentElement)
			raw = document.documentElement.getAttribute('lang') || '';
	} catch (e) {}
	try {
		if (!raw && typeof navigator != 'undefined')
			raw = navigator.language ||
				(navigator.languages && navigator.languages[0]) || '';
	} catch (e) {}

	raw = String(raw).toLowerCase().replace(/_/g, '-');
	return raw.indexOf('tr') === 0 ? 'tr' : 'en';
}

function language() {
	if (LANG === null)
		LANG = detect();
	return LANG;
}

// --- failures, by code -------------------------------------------------------
//
// Each entry matches a key in internal/daemon/messages.go. The placeholders
// are positional: the first %s takes the first value the daemon sent, and so
// on. They are all %s here even where the English says %d or %q, because the
// values arrive already rendered as text — a quoted mode name keeps its quotes
// in the Turkish sentence by having them written into it.

var ERRORS_TR = {
	'config.read':             'yapılandırma okunamadı: %s',
	'config.read_startup':     'açılışta yapılandırma okunamadı: %s',
	'config.nothing_selected': 'bağlanmak için hiçbir şey seçilmemiş',
	'config.no_target':        '"%s" kimliğinde sunucu ya da grup yok',
	'config.group_invalid':    '%s grubu: %s',
	'config.group_empty':      '%s grubunda kullanılabilir üye yok',
	'config.profile_invalid':  '%s sunucusu: %s',
	'config.rundir':           '%s çalışma dizini oluşturulamadı: %s',
	'config.mode_unknown':     'bilinmeyen yakalama modu "%s"',
	'config.tproxy_missing':   '"%s" modu TPROXY istiyor ama bu çekirdekte tproxy desteği yok',
	'update.failed':           'güncelleme kurulamadı: %s',
	'update.bad_source':       'güncelleme kaynağı %s bir owner/name deposu değil',
	'update.unreachable':      'sürüm sayfasına ulaşılamadı: %s',
	'update.no_releases':      '%s deposunda hiç sürüm yok, ya da depo adı yanlış',
	'update.rate_limited':     'sürüm sayfası isteği reddetti; bu adresten çok fazla istek yapılmış',
	'update.http':             'sürüm sayfası %s yanıtı verdi',
	'update.unreadable':       'sürüm sayfası okunamayan bir yanıt gönderdi',
	'update.draft':            'en yeni sürüm hâlâ taslak durumunda',
	'update.no_bundle':        'bu sürümde %s için paket yok',
	'update.no_checksums':     'bu sürümde %s dosyası yok, bu yüzden indirilen doğrulanamaz',
	'update.not_listed':       '%s, %s içinde listelenmemiş',
	'update.checksum_mismatch': '%s sağlama toplamıyla uyuşmuyor; kurulmak yerine silindi',
	'update.download_failed':  '%s indirilemedi: %s',
	'update.not_installable':  '%s çıktı, ama %s için doğrulanabilir bir paketi yok',
	'config.build':            'çekirdek yapılandırması üretilemedi: %s',
	'config.render':           'çekirdek yapılandırması yazıya dökülemedi: %s',
	'config.write':            'çekirdek yapılandırması diske yazılamadı: %s',
	'config.rejected':         'çekirdek üretilen yapılandırmayı kabul etmedi',
	'config.port_in_use':      '%s portu zaten kullanımda, bu yüzden %s başlayamıyor',

	'core.exited':             '%s',
	'core.start':              'çekirdek başlatılamadı (%s): %s',
	'core.cannot_run':         '%s adresindeki proxy çekirdeği çalıştırılamıyor: %s',
	'core.exited_immediately': 'çekirdek başlar başlamaz kapandı',
	'core.not_listening':      'çekirdek %s portunu %s içinde dinlemeye başlamadı',
	'core.no_data':            'çekirdek başladı ama tünelden hiç veri geçmiyor: %s',

	'tunnel.start':          '%s',
	'tunnel.firewall_drops': 'tünel ayakta ama güvenlik duvarı içinden geçen her şeyi düşürür',

	'fw.no_backend': '%s',
	'fw.none_found': 'desteklenen bir güvenlik duvarı bulunamadı: nftables ya da iptables kurun',
	'fw.needs_ip':   'bu mod, politika yönlendirmesi için `ip` komutuna ihtiyaç duyuyor ama kurulu değil: opkg install ip-full (ya da apk add ip-full)',
	'fw.apply':      '%s ile yakalama kuralları uygulanamadı: %s',
	'fw.reapply':    'güvenlik duvarı yeniden yüklendikten sonra yakalama kuralları geri konulamadı: %s',

	'dns.no_dnsmasq':         'bu cihazda dnsmasq çalışmıyor, oysa dnsmasq DNS modunun ayarladığı şey o',
	'dns.core_not_answering': 'çekirdek %s portunda DNS yanıtı vermiyor; dnsmasq oraya yönlendirilseydi tüm ağda ad çözümlemesi dururdu, bu yüzden önceki çözümleyici ayarı geri konuldu: %s',

	'dns.apply':     '%s',
	'detect.no_wan': '90 saniye sonunda hâlâ üst bağlantı yok, otomatik bağlanma yapılmadı'
};

var HINTS_TR = {
	'hint.config_syntax':  '/etc/config/xwrt dosyasında sözdizimi hatası olabilir, ona bakın',
	'hint.pick_target':    'önce bir sunucu ya da grup seçin',
	'hint.group_members':  'gruba sunucu ekleyin ya da tek bir sunucuya bağlanın',
	'hint.mode_values':    'modu şunlardan biri yapın: redirect, mixed, tproxy, tun',
	'hint.tproxy_install': 'kmod-nft-tproxy (ya da kmod-ipt-tproxy) kurun, ya da UDP\'yi TUN aygıtından taşıyan mixed moduna geçin',

	'hint.core_restarting': 'çekirdek kendiliğinden yeniden başlatılıyor; kapanmayı sürdürüyorsa yukarıdaki satırlarda reddettiği ayar yazılıdır',
	'hint.install_xray':    'xray paketini kurun ya da xray_bin ayarını kendi yoluna getirin',
	'hint.install_xray_at': 'xray-core paketini kurun ya da başka bir yerdeyse xray_bin ayarını o dosyaya yöneltin',
	'hint.core_last_lines': 'sebebini çekirdeğin yukarıdaki son satırları söylüyor; arka planda yeniden başlatılıyor, yani düzeltme yeniden bağlanınca etkili olur',
	'hint.core_no_port':    'çekirdek başladı ama portunu hiç açmadı; yukarıdaki satırlar çekirdeğin kendi çıktısıdır',
	'hint.rejected_config': 'reddedilen yapılandırma %s dosyasında; kabul etmediği ayarın adını çekirdeğin yukarıdaki kendi mesajı veriyor',
	'hint.port_change':     'ayarlardan %s değerini değiştirin ya da %s adresini dinleyen şeyi durdurun',

	'hint.pinned_cert':       'bu sunucu profilinde sertifika sabitlenmiş; sunucu sertifikasını yenilediyse sabitlenen parmak izi artık tutmaz ve her bağlantı reddedilir. Güncel sertifikayı okumak için `xwrt fetch-cert %s` çalıştırıp yeniden bağlanın. Değilse hesabın hâlâ geçerli ve sunucunun ayakta olduğunu kontrol edin',
	'hint.group_no_data':     'grubun hiçbir üyesi veri taşıyamadı; hesapların hâlâ geçerli ve sunucuların ayakta olduğunu kontrol edin',
	'hint.check_credentials': 'kimlik bilgilerinin hâlâ geçerli ve sunucunun ayakta olduğunu kontrol edin; sebebi genelde çekirdeğin yukarıdaki son satırları söyler',

	'hint.tun_kmod':           'kmod-tun kurun',
	'hint.tun_hev':            'hev-socks5-tunnel kurun, ya da tünel gerektirmeyen redirect modunu kullanın',
	'hint.tun_no_device':      'hev-socks5-tunnel başladı ama aygıtı hiç oluşturmadı; yukarıdaki satırlara ve /dev/net/tun\'un kullanılabilir olduğuna bakın',
	'hint.tun_ip_full':        'ip-full paketini kurun',
	'hint.tunnel_firewall':    'güvenlik duvarını yukarıda yazdığı gibi düzeltin, ya da TCP\'nin buna bağlı olmadığı mixed moduna geçin',
	'hint.fw_install_tools':   'nftables (fw4) ya da iptables (fw3) komut satırı araçlarını kurun',
	'hint.fw_rolled_back':     'kurallar geri alındı, yani cihaz proxy\'siz çalışmayı sürdürüyor; çekirdeğin reddettiği kuralın adı yukarıdaki mesajda',
	'hint.fw_reconnect':       'trafik artık yakalanmıyor; web arayüzünden yeniden bağlanın ya da `xwrt connect` çalıştırın',
	'hint.dns_modes':          'port 53\'ü güvenlik duvarında yakalamak için dns_mode ayarını \'redirect\', DNS\'e hiç dokunmamak için \'off\' yapın',
	'hint.wan_keeps_trying': 'WAN arayüzünün henüz ağ geçidi yok; xwrt denemeye devam ediyor ve üst bağlantı gelir gelmez bağlanacak'
};

var FAILURE_CATALOGS = { tr: { errors: ERRORS_TR, hints: HINTS_TR }, en: null };

// interpolate fills the positional placeholders. It walks the format once
// rather than calling replace per argument, so a value that itself contains a
// percent sign — a node named "TR-1 · 20%", say — cannot be read as a
// placeholder for the next one.
function interpolate(format, args) {
	args = args || [];
	var out = '', i = 0, n = 0;
	while (i < format.length) {
		if (format[i] === '%' && i + 1 < format.length) {
			var c = format[i + 1];
			if (c === '%') { out += '%'; i += 2; continue; }
			if ('sdqvw'.indexOf(c) >= 0) {
				out += (n < args.length) ? String(args[n]) : '';
				n++; i += 2; continue;
			}
		}
		out += format[i++];
	}
	return out;
}

// failureText renders one failure line in the reader's language.
//
// fallback is the daemon's own English sentence and is what comes back
// whenever this file has nothing better: an unknown code, an English
// interface, or a daemon older than this page.
function failureText(code, args, fallback) {
	var cat = FAILURE_CATALOGS[language()];
	if (!cat || !code)
		return fallback || '';
	var format = cat.errors[code] || cat.hints[code];
	if (!format)
		return fallback || '';
	return interpolate(format, args);
}

// The _() that was in place before this module loaded. LuCI's own, normally;
// its answer is preferred over the catalog so that a compiled catalog, or a
// string LuCI already knows, is never overridden by this file.
var inherited = null;
try {
	if (typeof window != 'undefined' && typeof window._ == 'function' &&
	    !window._.xwrtTranslate)
		inherited = window._;
} catch (e) {}

function translate(s, ctx) {
	var out = s;
	if (inherited) {
		try { out = inherited(s, ctx); } catch (e) { out = s; }
	}
	if (out !== s)
		return out;
	var tbl = CATALOGS[language()];
	return (tbl && tbl[s]) || out;
}
translate.xwrtTranslate = true;

try {
	if (typeof window != 'undefined')
		window._ = translate;
} catch (e) {}

function translateMenu() {
	if (language() === 'en')
		return;
	var links;
	try {
		links = document.querySelectorAll('a[href*="xdivine-ray/"]');
	} catch (e) {
		return;
	}
	Array.prototype.forEach.call(links || [], function(a) {
		var href = a.getAttribute('href') || '';
		Object.keys(MENU).forEach(function(leaf) {
			if (href.indexOf('xdivine-ray/' + leaf) < 0)
				return;
			var want = MENU[leaf];
			if ((a.textContent || '').trim() !== want)
				return;
			a.textContent = translate(want);
		});
	});
}

try {
	if (typeof document != 'undefined') {
		if (document.readyState === 'loading')
			document.addEventListener('DOMContentLoaded', translateMenu);
		else
			translateMenu();
	}
} catch (e) {}

return baseclass.extend({
	// use forces a language; detection is otherwise automatic. The render test
	// drives both languages through this.
	use: function(lang) { LANG = (lang === 'tr') ? 'tr' : 'en'; },
	language: language,
	translate: translate,
	menuTitles: MENU,
	catalog: function(lang) { return CATALOGS[lang || language()] || null; },
	failureText: failureText,
	// failureCatalog is what the catalog test reads: it checks these keys
	// against the daemon's own, so a failure cannot ship untranslated.
	failureCatalog: function(lang) { return FAILURE_CATALOGS[lang || language()] || null; }
});
