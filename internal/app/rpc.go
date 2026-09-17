package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

// The rpc subcommands implement OpenWrt's rpcd plugin protocol so LuCI can
// reach the daemon over ubus. Keeping the JSON handling here rather than in the
// shell shim means the shim is two lines and there is only one place where the
// API shape is known.

// rpcMethod maps an ubus method onto the HTTP API.
type rpcMethod struct {
	// Signature is what `rpcd list` advertises: argument names mapped to a
	// sample value whose type rpcd uses for validation.
	Signature map[string]any
	// Call performs the request. args is the decoded stdin payload.
	Call func(args map[string]any) ([]byte, error)
}

func rpcMethods() map[string]rpcMethod {
	simpleGet := func(path string) func(map[string]any) ([]byte, error) {
		return func(map[string]any) ([]byte, error) {
			return fetch(http.MethodGet, path, nil)
		}
	}
	simplePost := func(path string) func(map[string]any) ([]byte, error) {
		return func(map[string]any) ([]byte, error) {
			return fetch(http.MethodPost, path, nil)
		}
	}

	return map[string]rpcMethod{
		"status": {Signature: map[string]any{}, Call: simpleGet("/api/status")},
		"env":    {Signature: map[string]any{}, Call: simpleGet("/api/env")},
		"config": {Signature: map[string]any{}, Call: simpleGet("/api/config")},

		"logs": {
			Signature: map[string]any{"limit": 0, "level": "str", "source": "str", "step": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				q := url.Values{}
				limit := 200
				if v, ok := args["limit"]; ok {
					if n := int(toFloat(v)); n > 0 {
						limit = n
					}
				}
				q.Set("limit", strconv.Itoa(limit))
				for _, key := range []string{"level", "source", "step"} {
					if v := str(args[key]); v != "" {
						q.Set(key, v)
					}
				}
				return fetch(http.MethodGet, "/api/logs?"+q.Encode(), nil)
			},
		},
		"errors": {
			Signature: map[string]any{"limit": 0},
			Call: func(args map[string]any) ([]byte, error) {
				limit := 50
				if v, ok := args["limit"]; ok {
					if n := int(toFloat(v)); n > 0 {
						limit = n
					}
				}
				return fetch(http.MethodGet, "/api/logs/errors?limit="+strconv.Itoa(limit), nil)
			},
		},
		"clear_errors": {
			Signature: map[string]any{},
			Call: func(map[string]any) ([]byte, error) {
				return fetch(http.MethodDelete, "/api/logs/errors", nil)
			},
		},
		"clear_log": {
			Signature: map[string]any{},
			Call: func(map[string]any) ([]byte, error) {
				return fetch(http.MethodDelete, "/api/logs", nil)
			},
		},
		// Three, matching the three costs: what is known, asking again, and
		// installing.
		"update": {
			Signature: map[string]any{},
			Call: func(map[string]any) ([]byte, error) {
				return fetch(http.MethodGet, "/api/update", nil)
			},
		},
		"update_check": {
			Signature: map[string]any{},
			Call: func(map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/update/check", nil)
			},
		},
		"update_install": {
			Signature: map[string]any{},
			Call: func(map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/update/install", nil)
			},
		},
		"selftest": {
			Signature: map[string]any{"rounds": 0, "target": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				q := url.Values{}
				if n := int(toFloat(args["rounds"])); n > 0 {
					q.Set("rounds", strconv.Itoa(n))
				}
				if v := str(args["target"]); v != "" {
					q.Set("target", v)
				}
				return fetch(http.MethodPost, "/api/selftest?"+q.Encode(), nil)
			},
		},
		"clear_error": {
			Signature: map[string]any{},
			Call: func(map[string]any) ([]byte, error) {
				return fetch(http.MethodDelete, "/api/logs/error", nil)
			},
		},

		"connect": {
			// One field for both kinds of target: profile and group IDs come
			// from the same generator, so the daemon resolves either.
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/connect", map[string]any{
					"id": str(args["id"]),
				})
			},
		},
		"disconnect": {Signature: map[string]any{}, Call: simplePost("/api/disconnect")},
		"reapply_fw": {Signature: map[string]any{}, Call: simplePost("/api/firewall/reapply")},

		"import": {
			Signature: map[string]any{"uri": "str", "subscription_id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/profiles/import", map[string]any{
					"uri":             str(args["uri"]),
					"subscription_id": str(args["subscription_id"]),
				})
			},
		},
		"add_profile": {
			Signature: map[string]any{"profile": map[string]any{}},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/profiles", args["profile"])
			},
		},
		"update_profile": {
			Signature: map[string]any{"id": "str", "profile": map[string]any{}},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodPut, "/api/profiles/"+id, args["profile"])
			},
		},
		"delete_profile": {
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodDelete, "/api/profiles/"+id, nil)
			},
		},
		"fetch_cert": {
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodPost, "/api/profiles/"+id+"/fetch-cert", nil)
			},
		},
		"ping": {
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodGet, "/api/profiles/"+id+"/ping", nil)
			},
		},

		"groups":  {Signature: map[string]any{}, Call: simpleGet("/api/groups")},
		"rules":   {Signature: map[string]any{}, Call: simpleGet("/api/rules")},
		"traffic": {Signature: map[string]any{}, Call: simpleGet("/api/traffic")},
		"connections": {
			Signature: map[string]any{"limit": 0},
			Call: func(args map[string]any) ([]byte, error) {
				limit := 100
				if n := int(toFloat(args["limit"])); n > 0 {
					limit = n
				}
				return fetch(http.MethodGet,
					"/api/connections?limit="+strconv.Itoa(limit), nil)
			},
		},
		"enable_accounting": {
			Signature: map[string]any{},
			Call:      simplePost("/api/connections/accounting"),
		},
		"add_rule": {
			Signature: map[string]any{"rule": map[string]any{}},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/rules", args["rule"])
			},
		},
		"update_rule": {
			Signature: map[string]any{"id": "str", "rule": map[string]any{}},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodPut, "/api/rules/"+id, args["rule"])
			},
		},
		"delete_rule": {
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodDelete, "/api/rules/"+id, nil)
			},
		},
		"replace_rules": {
			// Reordering is a change to the list, not to one entry, because
			// the core takes the first match.
			Signature: map[string]any{"rules": []any{}},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPut, "/api/rules", args["rules"])
			},
		},
		"add_group": {
			Signature: map[string]any{"group": map[string]any{}},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/groups", args["group"])
			},
		},
		"update_group": {
			Signature: map[string]any{"id": "str", "group": map[string]any{}},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodPut, "/api/groups/"+id, args["group"])
			},
		},
		"delete_group": {
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodDelete, "/api/groups/"+id, nil)
			},
		},

		"settings": {
			Signature: map[string]any{"settings": map[string]any{}},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPut, "/api/settings", args["settings"])
			},
		},

		"sub_add": {
			Signature: map[string]any{"url": "str", "name": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				return fetch(http.MethodPost, "/api/subscriptions", map[string]any{
					"url":  str(args["url"]),
					"name": str(args["name"]),
				})
			},
		},
		"sub_refresh": {
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodPost, "/api/subscriptions/"+id+"/refresh", nil)
			},
		},
		"sub_del": {
			Signature: map[string]any{"id": "str"},
			Call: func(args map[string]any) ([]byte, error) {
				id := str(args["id"])
				if id == "" {
					return nil, fmt.Errorf("id is required")
				}
				return fetch(http.MethodDelete, "/api/subscriptions/"+id, nil)
			},
		},
	}
}

// rpcList prints the method signatures rpcd asks for at startup.
func rpcList() error {
	sig := map[string]any{}
	for name, m := range rpcMethods() {
		sig[name] = m.Signature
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(sig)
}

// rpcCall runs one method, reading its arguments as JSON from stdin.
//
// Errors are reported as a JSON object rather than a non-zero exit, because
// rpcd turns a failed plugin into an opaque ubus error and LuCI would have
// nothing useful to show the user.
func rpcCall(method string) error {
	m, ok := rpcMethods()[method]
	if !ok {
		return fmt.Errorf("unknown method %q", method)
	}
	args := map[string]any{}
	if raw, err := io.ReadAll(os.Stdin); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}
	out, err := m.Call(args)
	if err != nil {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(map[string]string{"error": err.Error()})
	}
	// Arrays are wrapped: ubus results must be objects.
	if len(out) > 0 && out[0] == '[' {
		os.Stdout.WriteString(`{"result":`)
		os.Stdout.Write(trimTrailingNewline(out))
		os.Stdout.WriteString("}\n")
		return nil
	}
	os.Stdout.Write(out)
	return nil
}

func trimTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}
