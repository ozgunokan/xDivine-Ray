#!/bin/bash
# Abonelik yolunun uçtan uca testi: gerçek daemon, gerçek HTTP, gerçek depolama.
#
# Bu, birim testlerin göremediği yeri kapatır. Bir aboneliğin ömrü üç olaydan
# ibarettir — eklenir, yenilenir, silinir — ve üçünün de doğru davranması
# ancak indirme, çözme, eşleştirme ve depolama birlikte çalıştığında görülür.
# Aralarından biri bozulduğunda ortaya çıkan belirti her zaman aynıdır: liste
# sessizce yanlış olur. Hata verilmez, sunucu eksik kalır, kimse fark etmez.
#
# Gereksinim: python3 (yerel HTTP sunucusu ve JSON okumak için). Yalnızca
# geliştirme makinesinde çalışır; routerda çalıştırmak için değildir.
#
#   bash test/subscription.sh
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
BIN="$WORK/xwrt"
PORT=18099
API=18787
export XWRT_CONFDIR="$WORK/cfg"

# The daemon and the CLI both read the API port out of the configuration, so
# writing it here moves both off the default in one line.
#
# This is not tidiness. The test used to leave the port at 8787, and a daemon
# already running on this machine — a real one, or one left over from another
# test — would answer instead. Every assertion then ran against somebody else's
# servers and subscriptions: some passed, some failed, and the failures read as
# bugs in the code under test rather than as the test talking to the wrong
# process. That happened, and it cost an hour.
mkdir -p "$XWRT_CONFDIR"
printf "package xwrt\n\nconfig xwrt 'main'\n\toption api_port '%s'\n" "$API" \
	> "$XWRT_CONFDIR/xwrt"

fail=0
ok()   { echo "ok   $*"; }
bad()  { echo "FAIL $*" >&2; fail=1; }
check() { # check <açıklama> <beklenen> <gerçek>
	if [ "$2" = "$3" ]; then ok "$1"; else bad "$1: beklenen [$2], gelen [$3]"; fi
}

cleanup() {
	[ -f "$WORK/daemon.pid" ] && kill "$(cat "$WORK/daemon.pid")" 2>/dev/null
	[ -f "$WORK/http.pid" ] && kill "$(cat "$WORK/http.pid")" 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

command -v python3 >/dev/null || { echo "skip python3 yok"; exit 0; }

# And if something is on either port anyway, stop rather than measure it.
#
# Both halves of this test are a process listening on a fixed port, and both
# can be left behind by a run that was interrupted. A leftover listing server
# is the worse one: it answers, it 404s because its directory is long gone, and
# every assertion fails in a way that reads like broken parsing. That happened.
busy() {
	python3 -c "
import socket, sys
s = socket.socket()
sys.exit(0 if s.connect_ex(('127.0.0.1', $1)) == 0 else 1)
"
}
for p in "$API" "$PORT"; do
	if busy "$p"; then
		echo "127.0.0.1:$p dolu — baska bir surec orada." >&2
		echo "muhtemelen yarida kalmis bir testten kalma; once onu kapat." >&2
		exit 1
	fi
done

go build -o "$BIN" "$ROOT/cmd/xwrt" || exit 1

# --- yayınlanacak listeler ---------------------------------------------------
# İlk liste. İlk düğümün adındaki yüzde işareti bilerek duruyor: sağlayıcıların
# yarısı düğüm adına yük yüzdesi yazar ve bir URL ayrıştırıcısı için % bir
# kaçış dizisinin başlangıcıdır. Bu satır bir kez sessizce atlanmıştı.
mkdir -p "$WORK/www"
cat > "$WORK/list1" <<'EOF'
vless://11111111-1111-1111-1111-111111111111@a.example:443?type=tcp&encryption=none&security=tls&sni=a.example#TR-1 · 20%
vless://22222222-2222-2222-2222-222222222222@b.example:443?type=ws&path=%2Fws&encryption=none&security=tls&sni=b.example#DE-2
trojan://parola@c.example:8443?security=tls&type=tcp&sni=c.example#NL-3
EOF
# İkinci liste: ilk düğüm yeniden adlandırıldı, üçüncüsü düştü, yenisi eklendi.
cat > "$WORK/list2" <<'EOF'
vless://11111111-1111-1111-1111-111111111111@a.example:443?type=tcp&encryption=none&security=tls&sni=a.example#TR-1 · 87% · yeni ad
vless://22222222-2222-2222-2222-222222222222@b.example:443?type=ws&path=%2Fws&encryption=none&security=tls&sni=b.example#DE-2
vless://44444444-4444-4444-4444-444444444444@d.example:443?type=tcp&encryption=none&security=tls&sni=d.example#FR-4
EOF
base64 -w0 "$WORK/list1" > "$WORK/www/sub.txt"

# Started without a subshell and without cd, so the pid recorded here is
# python3's own. Through `( cd … && python3 … & )` it was the subshell's, and
# the cleanup killed that instead — leaving a listing server on this port whose
# directory had been deleted. The next run then got a 404 for every request and
# failed as though the parser were broken.
python3 -m http.server "$PORT" --bind 127.0.0.1 --directory "$WORK/www" \
	>/dev/null 2>&1 &
echo $! > "$WORK/http.pid"

# --- daemon ------------------------------------------------------------------
"$BIN" daemon -no-restore >"$WORK/daemon.log" 2>&1 &
echo $! > "$WORK/daemon.pid"
sleep 3

jq() { python3 -c "import json,sys;$1"; }
profiles() { "$BIN" list; }

# --- ekleme ------------------------------------------------------------------
added=$("$BIN" sub-add "http://127.0.0.1:$PORT/sub.txt" "test" |
	jq 'print(json.load(sys.stdin).get("count"))')
check "listedeki üç sunucunun üçü de eklendi (addaki % dahil)" "3" "$added"

SUB=$("$BIN" config | jq 'print(json.load(sys.stdin)["subscriptions"][0]["id"])')
TR=$(profiles | jq '
[print(p["id"]) for p in json.load(sys.stdin) if p["address"] == "a.example"]')
DE=$(profiles | jq '
[print(p["id"]) for p in json.load(sys.stdin) if p["address"] == "b.example"]')
NL=$(profiles | jq '
[print(p["id"]) for p in json.load(sys.stdin) if p["address"] == "c.example"]')

# Elle eklenmiş bir sunucu: silmenin kapsamı bununla ölçülür.
"$BIN" import 'vless://99999999-9999-9999-9999-999999999999@elle.example:443?type=tcp&encryption=none&security=tls&sni=elle.example#ELLE' >/dev/null

# Sertifikayı elle sabitle ve iki üyeli bir grup kur: yenilemenin koruması
# gereken iki şey bunlar.
profiles | python3 -c "
import json,sys,urllib.request
p = [x for x in json.load(sys.stdin) if x['id'] == '$TR'][0]
p['pinned_cert'] = 'aa' * 32
urllib.request.urlopen(urllib.request.Request(
    'http://127.0.0.1:$API/api/profiles/$TR', data=json.dumps(p).encode(),
    method='PUT', headers={'Content-Type': 'application/json'})).read()
"
"$BIN" group-add grup leastPing "$TR" "$NL" >/dev/null

# --- yenileme ----------------------------------------------------------------
base64 -w0 "$WORK/list2" > "$WORK/www/sub.txt"
"$BIN" sub-refresh "$SUB" >/dev/null

after=$(profiles)
id_now=$(echo "$after" | jq '
[print(p["id"]) for p in json.load(sys.stdin) if p["address"] == "a.example"]')
check "yeniden adlandırılan sunucu kimliğini korudu" "$TR" "$id_now"

name_now=$(echo "$after" | jq '
[print(p["name"]) for p in json.load(sys.stdin) if p["address"] == "a.example"]')
check "adı yeni listeden alındı" "TR-1 · 87% · yeni ad" "$name_now"

pin_now=$(echo "$after" | jq '
[print(p.get("pinned_cert","")) for p in json.load(sys.stdin) if p["address"] == "a.example"]')
check "elle sabitlenen sertifika korundu" "$(printf 'aa%.0s' $(seq 32))" "$pin_now"

gone=$(echo "$after" | jq '
print(sum(1 for p in json.load(sys.stdin) if p["address"] == "c.example"))')
check "listeden düşen sunucu silindi" "0" "$gone"

new=$(echo "$after" | jq '
print(sum(1 for p in json.load(sys.stdin) if p["address"] == "d.example"))')
check "listeye eklenen sunucu geldi" "1" "$new"

members=$("$BIN" groups | jq '
print(",".join(json.load(sys.stdin)[0]["members"]))')
check "grup üyeliği: duran üye kaldı, düşen üye temizlendi" "$TR" "$members"

# --- bozuk adres -------------------------------------------------------------
err=$("$BIN" sub-add "http://127.0.0.1:9/yok" "bozuk" 2>&1 |
	jq 'print("var" if json.load(sys.stdin).get("error") else "yok")')
check "okunamayan abonelik hata veriyor" "var" "$err"

count=$("$BIN" config | jq 'print(len(json.load(sys.stdin)["subscriptions"]))')
check "okunamayan abonelik kaydedilmedi" "1" "$count"

# --- silme -------------------------------------------------------------------
"$BIN" sub-del "$SUB" >/dev/null
left=$(profiles | jq '
rows = json.load(sys.stdin) or []
print(",".join(sorted(p["address"] for p in rows)))')
check "silme yalnızca aboneliğin sunucularını aldı" "elle.example" "$left"

echo
[ "$fail" = "0" ] && echo "abonelik yolu: ekleme, yenileme, silme — hepsi beklendiği gibi"
exit "$fail"
