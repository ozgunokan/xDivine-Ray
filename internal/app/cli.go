// Package app holds the two entry points, the CLI and the daemon.
//
// They live in one package, and ship as one binary, because two Go binaries
// that share almost all of their code cost twice the flash for no benefit.
// On a 16 MB device that difference decides whether the package fits.
package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// Version is set at build time with -ldflags "-X xwrt/internal/app.Version=...".
var Version = "dev"

// RunCLI executes one CLI command and returns the process exit code. argv is
// the full argument list with the program name already removed.
func RunCLI(argv []string) int {
	if len(argv) < 1 {
		usage()
		return 2
	}
	args := argv[1:]
	var err error

	switch argv[0] {
	case "status":
		err = get("/api/status")
	case "env":
		err = get("/api/env")
	case "config":
		err = get("/api/config")
	case "list", "profiles":
		err = get("/api/profiles")
	case "subs", "subscriptions":
		err = get("/api/subscriptions")
	case "groups":
		err = get("/api/groups")
	case "rules":
		err = get("/api/rules")
	case "traffic":
		err = get("/api/traffic")
	case "connections", "conns":
		limit := "50"
		if len(args) > 0 {
			limit = args[0]
		}
		err = get("/api/connections?limit=" + limit)
	case "enable-accounting":
		err = post("/api/connections/accounting", nil)
	case "logs", "log":
		err = runLogs(args)
	case "errors":
		err = runLogErrors(args)
	case "clear-error":
		err = runClearError()
	case "clear-errors":
		err = runClearErrors()
	case "clear-log":
		err = runClearLog()
	case "selftest", "self-test":
		err = runSelfTest(args)

	case "connect":
		id := ""
		if len(args) > 0 {
			id = args[0]
		}
		err = post("/api/connect", map[string]string{"profile_id": id})
	case "disconnect":
		err = post("/api/disconnect", nil)
	case "reapply-fw":
		err = post("/api/firewall/reapply", nil)

	case "update":
		// `xwrt update` asks; `xwrt update install` installs. Two words rather
		// than a flag, because one of them replaces the running binary and
		// restarts the service, and that should not be a keystroke away from
		// the one that only looks.
		if len(args) > 0 && (args[0] == "install" || args[0] == "-i") {
			err = post("/api/update/install", nil)
			break
		}
		err = post("/api/update/check", nil)

	case "import":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt import <share-link> | xwrt import -  (read from stdin)")
			break
		}
		payload := args[0]
		if payload == "-" {
			var b []byte
			b, err = io.ReadAll(os.Stdin)
			if err != nil {
				break
			}
			payload = string(b)
		}
		err = post("/api/profiles/import", map[string]string{"uri": payload})

	case "delete", "del":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt delete <profile-id>")
			break
		}
		err = request(http.MethodDelete, "/api/profiles/"+args[0], nil)

	case "fetch-cert":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt fetch-cert <profile-id>")
			break
		}
		err = runFetchCert(args[0])

	case "ping":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt ping <profile-id>")
			break
		}
		err = get("/api/profiles/" + args[0] + "/ping")

	case "sub-add":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt sub-add <url> [name]")
			break
		}
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		err = post("/api/subscriptions", map[string]string{"url": args[0], "name": name})

	case "sub-refresh":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt sub-refresh <subscription-id>")
			break
		}
		err = post("/api/subscriptions/"+args[0]+"/refresh", nil)

	case "sub-del":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt sub-del <subscription-id>")
			break
		}
		err = request(http.MethodDelete, "/api/subscriptions/"+args[0], nil)

	case "group-add":
		if len(args) < 2 {
			err = fmt.Errorf("usage: xwrt group-add <name> <strategy> <profile-id>...\n" +
				"  strategies: leastPing (failover), leastLoad, random, roundRobin")
			break
		}
		err = post("/api/groups", map[string]any{
			"name":     args[0],
			"strategy": args[1],
			"members":  args[2:],
		})

	case "rule-add":
		if len(args) < 3 {
			err = fmt.Errorf("usage: xwrt rule-add <name> <action> <matcher>...\n" +
				"  actions:  direct (keep off the VPN), proxy (force through), block\n" +
				"  matchers: domain=<name>  ip=<cidr>  src=<client>  port=<spec>\n" +
				"            proto=<http|tls|quic|bittorrent>  net=<tcp|udp|tcp,udp>\n" +
				"  example:  xwrt rule-add 'Bank' direct domain=bank.com domain=gov.tr")
			break
		}
		err = addRule(args[0], args[1], args[2:])

	case "rule-del":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt rule-del <rule-id>")
			break
		}
		err = request(http.MethodDelete, "/api/rules/"+args[0], nil)

	case "group-del":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt group-del <group-id>")
			break
		}
		err = request(http.MethodDelete, "/api/groups/"+args[0], nil)

	case "set":
		err = setSettings(args)

	case "rpc-list":
		err = rpcList()
	case "rpc":
		if len(args) == 0 {
			err = fmt.Errorf("usage: xwrt rpc <method>  (arguments as JSON on stdin)")
			break
		}
		err = rpcCall(args[0])

	case "version", "-v", "--version":
		fmt.Println(Version)
		return 0

	case "help", "-h", "--help":
		usage()
		return 0

	default:
		err = fmt.Errorf("unknown command %q", argv[0])
	}

	if err != nil {
		// Errors are JSON too, so the rpcd shim can pass them straight to LuCI.
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]string{"error": err.Error()})
		return 1
	}
	return 0
}

// addRule turns key=value matcher arguments into a rule. Repeating a key adds
// another entry to that matcher, which is how a rule covers several domains.
func addRule(name, action string, matchers []string) error {
	rule := map[string]any{
		"name":    name,
		"action":  action,
		"enabled": true,
	}
	lists := map[string][]string{}

	for _, m := range matchers {
		key, val, ok := strings.Cut(m, "=")
		if !ok || val == "" {
			return fmt.Errorf("expected key=value, got %q", m)
		}
		switch key {
		case "domain":
			lists["domains"] = append(lists["domains"], val)
		case "ip":
			lists["ips"] = append(lists["ips"], val)
		case "src", "source":
			lists["sources"] = append(lists["sources"], val)
		case "proto", "protocol":
			lists["protocols"] = append(lists["protocols"], val)
		case "port":
			rule["port"] = val
		case "sport", "source_port":
			rule["source_port"] = val
		case "net", "network":
			rule["network"] = val
		default:
			return fmt.Errorf("unknown matcher %q; use domain, ip, src, port, "+
				"sport, proto or net", key)
		}
	}
	for k, v := range lists {
		rule[k] = v
	}
	return post("/api/rules", rule)
}

// setSettings applies key=value pairs to the settings object, reading the
// current values first so unspecified fields are preserved.
func setSettings(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: xwrt set key=value [key=value ...]")
	}
	body, err := fetch(http.MethodGet, "/api/config", nil)
	if err != nil {
		return err
	}
	var cfg struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return err
	}
	if cfg.Settings == nil {
		cfg.Settings = map[string]any{}
	}
	for _, kv := range args {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", kv)
		}
		v, err := coerce(key, val)
		if err != nil {
			return err
		}
		cfg.Settings[key] = v
	}
	return request(http.MethodPut, "/api/settings", cfg.Settings)
}

// coerce turns a command line string into the JSON type the settings field
// expects, so `xwrt set socks_port=1080 allow_lan=true` does the right thing.
//
// The type comes from the field, not from the look of the value. Guessing from
// the value is what this used to do, and it made `xwrt set dns_mode=off`
// impossible: "off" looks like a boolean, dns_mode is a string whose values
// are dnsmasq, redirect and off, and the daemon rejected the request with a
// sentence about Go types. Our own advice, printed next to a DNS failure, was
// to run exactly that command.
//
// It also refuses a key that is not a setting. The old behaviour was to send
// it, have the daemon ignore it, and report success — so a typo looked like a
// change that had been made.
func coerce(key, v string) (any, error) {
	// Three fields belong to the engine: they say what is connected right now,
	// and the daemon keeps them whatever a caller sends. Accepting them here
	// would print a success and change nothing, which is the most expensive
	// kind of answer — the reader goes looking for the reason it did not work
	// everywhere except at the command they typed.
	if owner, ok := engineOwned[key]; ok {
		return nil, fmt.Errorf("%s is set by the daemon, not by hand; use %s", key, owner)
	}
	field, ok := settingsFields()[key]
	if !ok {
		return nil, fmt.Errorf("%q is not a setting; run `xwrt config` to see them", key)
	}
	switch field.Kind() {
	case reflect.Bool:
		switch strings.ToLower(v) {
		case "true", "yes", "on", "1":
			return true, nil
		case "false", "no", "off", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%s takes true or false, not %q", key, v)
	case reflect.Int:
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("%s takes a number, not %q", key, v)
		}
		return n, nil
	case reflect.Slice:
		// A list field: comma separated, empty string means an empty list.
		if strings.TrimSpace(v) == "" {
			return []string{}, nil
		}
		parts := strings.Split(v, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts, nil
	default:
		return v, nil
	}
}

// engineOwned names the fields the daemon maintains itself, and the command
// that actually changes each one.
var engineOwned = map[string]string{
	"active":      "`xwrt connect <id>`",
	"active_kind": "`xwrt connect <id>`",
	"enabled":     "`xwrt connect` and `xwrt disconnect`",
}

// settingsFields maps a setting's name to its type, read from the struct the
// daemon unmarshals into. Reading it rather than listing it here means a
// setting added later is understood by this command without anyone
// remembering to come back.
func settingsFields() map[string]reflect.Type {
	out := map[string]reflect.Type{}
	t := reflect.TypeOf(model.Settings{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = t.Field(i).Type
	}
	return out
}

func get(path string) error { return request(http.MethodGet, path, nil) }
func post(path string, body any) error {
	return request(http.MethodPost, path, body)
}

func request(method, path string, body any) error {
	out, err := fetch(method, path, body)
	if err != nil {
		return err
	}
	os.Stdout.Write(out)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		fmt.Println()
	}
	// A change that is stored but not running is worth a sentence. The rule is
	// in the config file and does nothing at all until the core is rebuilt with
	// it, and the only thing worse than that is not being told.
	if bytes.Contains(out, []byte(`"reconnect_required":true`)) {
		fmt.Fprintln(os.Stderr,
			"note: saved, but not applied yet — run 'xwrt connect' to rebuild "+
				"the connection with it")
	}
	return nil
}

func fetch(method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, "http://"+apiAddr()+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach xwrtd on %s: %w "+
			"(is the service running? /etc/init.d/xwrt start)", apiAddr(), err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(out, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// apiAddr reads the port straight from UCI so the CLI keeps working when the
// operator moves the API off its default port.
func apiAddr() string {
	port := 8787
	if v := ucicfg.New().GetOption("xwrt", "main", "api_port"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			port = n
		}
	}
	return "127.0.0.1:" + strconv.Itoa(port)
}

func usage() {
	fmt.Fprint(os.Stderr, `xwrt - control the xwrt daemon

  xwrt status                    connection state, mode and traffic counters
  xwrt env                       detected LAN/WAN devices and firewall stack
  xwrt config                    full configuration as JSON
  xwrt list                      configured profiles
  xwrt groups                    configured groups
  xwrt logs [n] [level=…]        recent log lines, debug left out; filter by
                                 level, source or step, e.g. xwrt logs 50 error
                                 or xwrt logs debug
  xwrt errors [n]                failures only, with cause and suggested fix
  xwrt clear-error               dismiss the failure shown in status
  xwrt clear-errors              empty the failure journal
  xwrt clear-log                 empty the log as well as the journal
  xwrt selftest [n] [host:port]  measure the same address directly and through
                                 the tunnel, and say where the latency is
  xwrt traffic                   live throughput history
  xwrt connections [limit]       active flows, by LAN client
  xwrt enable-accounting         turn on kernel byte counters for the above

  xwrt connect [id]              connect to a profile or a group
                                 (defaults to the stored selection)
  xwrt disconnect                disconnect and remove all rules
  xwrt reapply-fw                reinstall capture rules after a firewall reload
  xwrt update                    ask whether a newer release exists
  xwrt update install            download it, verify it and install it

  xwrt import <link>             add profiles from a share link ('-' reads stdin)
  xwrt delete <profile-id>       remove a profile
  xwrt fetch-cert <profile-id>   pin the server's certificate (replaces
                                 allowInsecure, which new cores removed)
  xwrt ping <profile-id>         TCP reachability check of the server

  xwrt group-add <name> <strategy> <profile-id>...
                                 leastPing gives failover; random and
                                 roundRobin only spread load
  xwrt group-del <group-id>      remove a group

  xwrt rules                     routing exceptions, in match order
  xwrt rule-add <name> <action> <matcher>...
                                 direct keeps traffic off the VPN;
                                 matchers: domain= ip= src= port= proto= net=
  xwrt rule-del <rule-id>        remove a rule

  xwrt sub-add <url> [name]      add and fetch a subscription
  xwrt sub-refresh <sub-id>      refetch a subscription
  xwrt sub-del <sub-id>          remove a subscription and its profiles

  xwrt set key=value ...         change settings, e.g. mode=tun proxy_udp=true
`)
}
