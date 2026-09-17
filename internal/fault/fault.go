// Package fault tags an error with a stable name.
//
// The daemon gives every failure it raises itself a code, so the interface can
// show it in the reader's language (internal/daemon/messages.go). Failures
// raised further down — the DNS step deciding this device has no dnsmasq, the
// firewall step finding neither nftables nor iptables — arrive at the daemon
// as ordinary errors and would come out in English on an otherwise Turkish
// page.
//
// A whole error type would be too much for that. What is needed is a label
// carried alongside the error, which this is: Tag wraps, CodeOf reads it back,
// and the error keeps behaving as itself the whole way — errors.Is and
// errors.As still see through it, and code that never asks about the tag
// cannot tell it is there.
//
// Only failures a person can act on are worth tagging. "write /tmp/x:
// permission denied" is the operating system's sentence about its own state,
// and a translated copy of it would be a sentence nobody can search for.
package fault

import (
	"errors"
	"fmt"
)

type tagged struct {
	code string
	args []string
	err  error
}

func (t *tagged) Error() string { return t.err.Error() }
func (t *tagged) Unwrap() error { return t.err }

// Tag names an error. The args are the values the message interpolates, in the
// order the sentence uses them, so a translation can put them back in its own
// order.
func Tag(err error, code string, args ...any) error {
	if err == nil {
		return nil
	}
	return &tagged{code: code, args: textArgs(args), err: err}
}

// Tagf builds and names an error in one step, for the common case where both
// happen on the same line.
func Tagf(code string, args []any, format string, formatArgs ...any) error {
	return Tag(fmt.Errorf(format, formatArgs...), code, args...)
}

// CodeOf reports the name of the outermost tagged error in the chain, if any.
func CodeOf(err error) (string, []string, bool) {
	var t *tagged
	if errors.As(err, &t) {
		return t.code, t.args, true
	}
	return "", nil, false
}

func textArgs(args []any) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch v := a.(type) {
		case error:
			out = append(out, v.Error())
		case string:
			out = append(out, v)
		default:
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}
