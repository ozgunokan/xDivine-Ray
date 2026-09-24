package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The three lists that have to agree about what the interface may call.
//
// A ubus method exists in three places: the daemon declares it, the LuCI app
// calls it, and an ACL file says whether the session is allowed to. Nothing
// connects them but spelling, and getting it wrong fails in the quietest way
// this project has: the page works perfectly for root, which is who tests it,
// and every button that needs the missing entry returns "access denied" for
// anyone else. Nobody notices until a user who is not root opens the page.
//
// So all three are read here and compared.

const aclPath = "../../luci-app-xwrt/root/usr/share/rpcd/acl.d/luci-app-xwrt.json"
const clientPath = "../../luci-app-xwrt/htdocs/luci-static/resources/xwrt.js"

// aclMethods returns every xwrt method the ACL grants, read or write.
func aclMethods(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(aclPath))
	if err != nil {
		t.Fatalf("the ACL file is not where the package installs it from: %v", err)
	}
	var doc map[string]struct {
		Read struct {
			Ubus map[string][]string `json:"ubus"`
		} `json:"read"`
		Write struct {
			Ubus map[string][]string `json:"ubus"`
		} `json:"write"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the ACL file is not valid JSON, so rpcd would ignore it "+
			"entirely and every call would be denied: %v", err)
	}
	out := map[string]bool{}
	for _, group := range doc {
		for _, m := range group.Read.Ubus["xwrt"] {
			out[m] = true
		}
		for _, m := range group.Write.Ubus["xwrt"] {
			out[m] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("the ACL grants no xwrt methods at all")
	}
	return out
}

// clientCalls returns every method the LuCI app declares an RPC binding for.
func clientCalls(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Clean(clientPath))
	if err != nil {
		t.Fatalf("cannot read the client bindings: %v", err)
	}
	var found []string
	text := string(src)
	for {
		i := strings.Index(text, "method: '")
		if i < 0 {
			break
		}
		text = text[i+len("method: '"):]
		j := strings.IndexByte(text, '\'')
		if j < 0 {
			break
		}
		found = append(found, text[:j])
	}
	if len(found) == 0 {
		t.Fatal("no rpc bindings found; the parser above no longer matches " +
			"how they are written, so this test is checking nothing")
	}
	return found
}

func TestEveryMethodTheInterfaceCallsIsAllowed(t *testing.T) {
	allowed := aclMethods(t)
	var denied []string
	for _, m := range clientCalls(t) {
		if !allowed[m] {
			denied = append(denied, m)
		}
	}
	if len(denied) > 0 {
		sort.Strings(denied)
		t.Fatalf("the interface calls %s, which the ACL does not grant. "+
			"These work for root and fail with \"access denied\" for every "+
			"other session, which is why nobody notices.",
			strings.Join(denied, ", "))
	}
}

func TestEveryMethodTheACLGrantsExists(t *testing.T) {
	methods := rpcMethods()
	var missing []string
	for m := range aclMethods(t) {
		if _, ok := methods[m]; !ok {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("the ACL grants %s, which the daemon does not implement — "+
			"either a typo, or a method that was renamed and left behind",
			strings.Join(missing, ", "))
	}
}

func TestTheConfigEditorsMethodIsWiredAllTheWayThrough(t *testing.T) {
	// The one that prompted the two tests above. It replaces the whole
	// configuration, so it is a write, and a read-only grant would let the
	// page load and fail on save.
	if _, ok := rpcMethods()["put_config"]; !ok {
		t.Fatal("the daemon has no put_config method")
	}
	raw, err := os.ReadFile(filepath.Clean(aclPath))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]struct {
		Write struct {
			Ubus map[string][]string `json:"ubus"`
		} `json:"write"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, group := range doc {
		for _, m := range group.Write.Ubus["xwrt"] {
			if m == "put_config" {
				return
			}
		}
	}
	t.Fatal("put_config is not in the ACL's write list; saving the JSON page " +
		"would be denied for any session that is not root")
}
