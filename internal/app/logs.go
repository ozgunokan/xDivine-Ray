package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/selftest"
)

// The log commands print text rather than the JSON every other command
// produces. They are the one part of the CLI a person reads directly — usually
// while something is broken and over ssh on a slow link — so readability wins
// over consistency here. `--json` gives the machine-readable form back.

// runLogs implements `xwrt logs [limit] [level=…] [source=…] [step=…] [--json]`.
func runLogs(args []string) error {
	q := url.Values{}
	limit := "200"
	asJSON := false

	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case a == "-f", a == "--follow":
			return fmt.Errorf("following is not supported; use `logread -f` " +
				"for the system log, which carries every warning and error")
		default:
			key, val, ok := strings.Cut(a, "=")
			if !ok {
				if _, err := strconv.Atoi(a); err != nil {
					// A bare word is almost always a level: `xwrt logs error`
					// is what someone types first.
					if _, isLevel := model.ParseLevel(a); isLevel {
						q.Set("level", a)
						continue
					}
					return fmt.Errorf("expected a line count, a level, or key=value, got %q", a)
				}
				limit = a
				continue
			}
			switch key {
			case "limit":
				limit = val
			case "level", "source", "step":
				q.Set(key, val)
			default:
				return fmt.Errorf("unknown filter %q; use level, source or step", key)
			}
		}
	}
	q.Set("limit", limit)

	// Without a level asked for, debug is left out.
	//
	// The core writes a debug line per UDP packet it forwards. On a busy
	// network that is hundreds of lines a second, so `xwrt logs 30` answers
	// the question "what happened" with thirty lines about individual packets
	// and nothing about anything that happened. The lines someone wants —
	// connected, disconnected, the rule that was refused — are minutes
	// upstream by then.
	//
	// They are still there, and the footer says how to see them. `xwrt logs
	// debug` is the whole of it.
	hidDebug := false
	if q.Get("level") == "" {
		q.Set("level", string(model.LevelInfo))
		hidDebug = true
	}

	body, err := fetch(http.MethodGet, "/api/logs?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if asJSON {
		return writeRaw(body)
	}

	var res struct {
		Entries   []model.LogEntry `json:"entries"`
		LastError *model.LastError `json:"last_error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}
	if len(res.Entries) == 0 {
		fmt.Println("no matching log entries")
	}
	for _, e := range res.Entries {
		origin := string(e.Source)
		if e.Step != model.StepNone {
			origin += "/" + string(e.Step)
		}
		fmt.Printf("%s %-7s %-14s %s\n", shortTime(e.Time),
			strings.ToUpper(string(e.Level)), origin, e.Message)
	}
	if hidDebug {
		fmt.Println("\n(debug lines are not shown; `xwrt logs debug` includes them)")
	}
	if res.LastError != nil {
		fmt.Printf("\nlast failure: %s\n", describeLastError(res.LastError))
	}
	return nil
}

// runLogErrors implements `xwrt errors [limit] [--json]`. It reads the error
// journal, which keeps failures long after the main log has scrolled past them.
func runLogErrors(args []string) error {
	limit := "50"
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			continue
		}
		if _, err := strconv.Atoi(a); err != nil {
			return fmt.Errorf("expected a count, got %q", a)
		}
		limit = a
	}

	body, err := fetch(http.MethodGet, "/api/logs/errors?limit="+url.QueryEscape(limit), nil)
	if err != nil {
		return err
	}
	if asJSON {
		return writeRaw(body)
	}

	var res struct {
		Errors []model.ErrorEntry `json:"errors"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}
	if len(res.Errors) == 0 {
		fmt.Println("no errors recorded since the daemon started")
		return nil
	}
	for i, e := range res.Errors {
		if i > 0 {
			fmt.Println()
		}
		origin := string(e.Source)
		if e.Step != model.StepNone {
			origin += "/" + string(e.Step)
		}
		fmt.Printf("%s  [%s]  %s\n", longTime(e.Time), origin, e.Message)
		for _, line := range strings.Split(e.Detail, "\n") {
			if strings.TrimSpace(line) != "" {
				fmt.Printf("    %s\n", line)
			}
		}
		if e.Hint != "" {
			fmt.Printf("  -> %s\n", e.Hint)
		}
	}
	return nil
}

// runClearError dismisses the current failure, which is what the UI's dismiss
// button does. The journal keeps its copy.
func runClearError() error {
	return request(http.MethodDelete, "/api/logs/error", nil)
}

// runClearErrors empties the failure journal.
func runClearErrors() error {
	out, err := fetch(http.MethodDelete, "/api/logs/errors", nil)
	if err != nil {
		return err
	}
	var res struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}
	fmt.Printf("cleared %d failure(s)\n", res.Cleared)
	return nil
}

// runClearLog empties the message stream as well as the journal.
func runClearLog() error {
	out, err := fetch(http.MethodDelete, "/api/logs", nil)
	if err != nil {
		return err
	}
	var res struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}
	// The count matters: the core writes a line per connection, so a stream
	// that was just emptied looks untouched a second later.
	fmt.Printf("cleared %d line(s)\n", res.Cleared)
	return nil
}

// runSelfTest measures the tunnel from the router, which is the same thing the
// button on the status page does — but reachable over ssh, where someone
// debugging a slow connection usually already is.
func runSelfTest(args []string) error {
	q := url.Values{}
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case strings.Contains(a, ":"):
			q.Set("target", a)
		default:
			if _, err := strconv.Atoi(a); err != nil {
				return fmt.Errorf("expected a round count or a host:port target, got %q", a)
			}
			q.Set("rounds", a)
		}
	}

	out, err := fetch(http.MethodPost, "/api/selftest?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if asJSON {
		os.Stdout.Write(out)
		return nil
	}

	var rep selftest.Report
	if err := json.Unmarshal(out, &rep); err != nil {
		return err
	}

	fmt.Printf("target %s\n\n", rep.Target)
	fmt.Printf("%-10s %10s %10s %10s  %s\n", "", "median", "min", "max", "")
	for _, st := range []*selftest.Stat{rep.Server, rep.Direct, rep.Tunnel} {
		if st == nil {
			continue
		}
		note := ""
		if st.Failures > 0 {
			note = fmt.Sprintf("%d/%d failed: %s", st.Failures, st.Attempts, st.Error)
		}
		fmt.Printf("%-10s %7.1f ms %7.1f ms %7.1f ms  %s\n",
			st.Label, st.Median, st.Min, st.Max, note)
	}
	fmt.Printf("\n%s\n", rep.Verdict)
	return nil
}

func describeLastError(e *model.LastError) string {
	out := e.Message
	if e.Step != model.StepNone {
		out = string(e.Step) + ": " + out
	}
	if e.Hint != "" {
		out += " (" + e.Hint + ")"
	}
	return out + "  [" + shortTime(e.Time) + "]"
}

// shortTime renders a timestamp as a clock time, which is what someone reading
// a log alongside a problem they just caused actually wants.
func shortTime(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("15:04:05")
	}
	return ts
}

func longTime(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("2006-01-02 15:04:05")
	}
	return ts
}

func writeRaw(body []byte) error {
	os.Stdout.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

// runFetchCert pins a server's certificate and prints what was pinned.
//
// The chain is printed rather than hidden behind a success message on purpose:
// a pin records whatever answered, so the operator is the last line of defence
// against pinning something that is intercepting the connection. Seeing the
// issuer is how they notice.
func runFetchCert(id string) error {
	body, err := fetch(http.MethodPost, "/api/profiles/"+id+"/fetch-cert", nil)
	if err != nil {
		return err
	}
	var res struct {
		Pin       string `json:"pin"`
		Endpoint  string `json:"endpoint"`
		SNI       string `json:"sni"`
		Trusted   bool   `json:"trusted"`
		NameMatch bool   `json:"name_match"`
		Chain     []struct {
			Subject  string `json:"subject"`
			Issuer   string `json:"issuer"`
			NotAfter string `json:"not_after"`
			SHA256   string `json:"sha256"`
		} `json:"chain"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}

	fmt.Printf("pinned %s (sni %s)\n", res.Endpoint, res.SNI)
	for i, c := range res.Chain {
		label := "leaf"
		if i > 0 {
			label = "ca  "
		}
		fmt.Printf("  %s  %s\n", label, c.Subject)
		fmt.Printf("        issued by %s\n", c.Issuer)
		fmt.Printf("        expires %s, sha256 %s\n", c.NotAfter, c.SHA256)
	}
	fmt.Printf("  pin   %s\n", res.Pin)
	switch {
	case !res.Trusted:
		fmt.Println("\nNo public authority vouches for this certificate. That is normal for " +
			"a self-signed\nserver, but check the issuer above is what you expect: anything " +
			"intercepting this\nconnection right now would have been pinned instead.")
	case !res.NameMatch:
		fmt.Printf("\nThe certificate is valid and publicly trusted, but issued for a "+
			"different name\nthan the SNI in use (%s). That is the usual arrangement "+
			"for this kind of\nserver, and the pin makes it work.\n", res.SNI)
	default:
		fmt.Println("\nThe certificate is publicly trusted and matches the SNI in use.")
	}
	fmt.Println("\nallowInsecure has been turned off for this profile. Connect again to use the pin.")
	return nil
}
