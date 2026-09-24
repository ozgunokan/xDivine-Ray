package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"xwrt/internal/ucicfg"
)

// Replacing the whole configuration with a document someone edited by hand.
//
// Every other write here changes one thing through a form that knows what it
// is changing. This one takes the lot — servers, groups, rules, subscriptions
// and settings — which is what makes it useful (a backup, a move to another
// router, a bulk edit no form could express) and what makes it the one route
// that can empty a device in a single request.
//
// Three things guard it, in this order, and none of them touches the store
// until all three have passed:
//
//   - the document has to parse, and a key it does not recognise is an error
//     rather than something quietly ignored. A hand-typed "profile" where
//     "profiles" was meant would otherwise delete every server and report
//     success.
//   - the document has to be complete. A key that is simply absent means
//     "delete all of these", which is almost never what a partial paste
//     intended, so an absent key is refused while an explicitly empty list is
//     accepted. "[]" is a decision; nothing at all is usually an accident.
//   - the result has to make sense as a whole: see (*ucicfg.Data).Check.
//
// Saving does not reconnect. A running tunnel keeps running on the
// configuration it was built from, and the interface already says when what is
// saved differs from what is live — which is the right behaviour for an editor
// where the next keystroke might be the one that fixes a typo.

// maxConfigBytes is generous for the thing it holds and small enough that a
// router cannot be pushed over by one request. A thousand servers is well
// under a megabyte.
const maxConfigBytes = 2 << 20

// sections are the top-level keys a complete document carries. Warnings is
// deliberately not among them: it is the loader's own output, reported on the
// way out and ignored on the way in.
var sections = []string{"settings", "profiles", "groups", "rules", "subscriptions"}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxConfigBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("could not read the document: %w", err))
		return
	}

	current, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	next, problems := parseConfig(body, current)
	if len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":    problems[0],
			"problems": problems,
			"saved":    false,
		})
		return
	}

	// A dry run answers "would this be accepted" without accepting it, which
	// is what a Check button in an editor needs and what anyone scripting this
	// should do first.
	if r.URL.Query().Get("check") == "1" {
		writeJSON(w, http.StatusOK, map[string]any{
			"saved":    false,
			"problems": []string{},
			"config":   next,
		})
		return
	}

	if err := s.store.Save(next); err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Errorf("the document was valid but could not be written: %w", err))
		return
	}

	// Read it back rather than echoing what was sent. The store normalises as
	// it writes, and showing the caller their own text would hide that — the
	// editor would then be displaying a document the device does not have.
	saved, err := s.store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"saved":    true,
		"problems": []string{},
		"config":   saved,
	})
}

// parseConfig turns a document into data, or into the list of reasons it is
// not one. It is separate from the handler so it can be tested without a
// store, a socket or a device.
func parseConfig(body []byte, current *ucicfg.Data) (*ucicfg.Data, []string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, []string{"the document is empty"}
	}

	// Which sections are present, before anything is decoded. This is the
	// only way to tell "I deleted every rule" from "I did not include the
	// rules", and those mean opposite things.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, []string{jsonProblem(trimmed, err)}
	}

	var missing []string
	for _, key := range sections {
		if _, ok := raw[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, []string{fmt.Sprintf(
			"the document has no %s. A whole configuration is expected here, "+
				"and a missing section would delete everything in it — if you "+
				"meant that, send it as an empty list. Load the current "+
				"configuration first and edit that.",
			strings.Join(missing, ", "))}
	}

	var next ucicfg.Data
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	// A misspelled key is a typo, not a preference. Ignoring it silently is
	// how a document can look accepted and mean something else.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		return nil, []string{jsonProblem(trimmed, err)}
	}

	// Runtime state belongs to the engine. Enabled says whether the daemon
	// should dial out on its own, and it is written by connect and disconnect
	// — a document carrying yesterday's value would silently reverse a
	// decision made since.
	next.Settings.Enabled = current.Settings.Enabled
	next.Settings.ActiveKind = current.Settings.ActiveKind
	next.Settings.Normalize()
	next.Warnings = nil

	if problems := next.Check(); len(problems) > 0 {
		return nil, problems
	}
	return &next, nil
}

// jsonProblem turns a decoder error into something a person editing a text box
// can act on: which line, and what was wrong with it.
func jsonProblem(body []byte, err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		line, col := lineCol(body, syn.Offset)
		return fmt.Sprintf("line %d, column %d: %v", line, col, syn)
	case errors.As(err, &typ):
		line, col := lineCol(body, typ.Offset)
		return fmt.Sprintf("line %d, column %d: %q should be %s, but a %s was given",
			line, col, typ.Field, typ.Type, typ.Value)
	default:
		// DisallowUnknownFields produces a plain error, and its wording is
		// already the useful part.
		return err.Error()
	}
}

// lineCol converts a byte offset into a position someone can find in an editor.
func lineCol(body []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(body)) {
		offset = int64(len(body))
	}
	line, col := 1, 1
	for _, b := range body[:offset] {
		if b == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}
