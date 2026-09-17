package daemon

import (
	"errors"
	"fmt"
	"strings"

	"xwrt/internal/fault"
	"xwrt/internal/model"
)

// A connect failure has three parts worth keeping apart, because they answer
// three different questions:
//
//	message  what failed          → "core did not start listening on port 10808"
//	detail   why, in its words    → the core's own last lines
//	hint     what to do about it  → "install kmod-tun"
//
// A plain error can only carry the first. stepError carries all three plus the
// stage it happened in, so the failure can be filed once, at the top of the
// sequence, without every intermediate return having to log on its own — which
// is how a log ends up with the same failure reported three times at three
// levels of wrapping.
type stepError struct {
	step   model.Step
	source model.Source
	err    error
	detail string
	hint   string

	// code names the failure and args are the values its sentence
	// interpolates, so the interface can render it in another language
	// without matching English text. See model.ErrorEntry.
	code     string
	args     []string
	hintCode string
	hintArgs []string
}

func (s *stepError) Error() string { return s.err.Error() }
func (s *stepError) Unwrap() error { return s.err }

// fail builds a failure attributed to one step of the sequence, from a code in
// the catalog. The English sentence comes from the catalog too, so a failure
// cannot exist with a message but no key to translate it by — which is the way
// translated interfaces normally rot: someone adds one more error in a hurry.
func fail(step model.Step, code string, args ...any) *stepError {
	format, ok := messages[code]
	if !ok {
		// Not reachable through a passing build: TestEveryCodeHasASentence
		// checks the catalog against every call site. Falling back to the key
		// keeps a mistake legible rather than producing an empty error.
		format = code
	}
	se := &stepError{
		step:   step,
		source: model.SourceDaemon,
		err:    fmt.Errorf(format, args...),
		code:   code,
		args:   textArgs(args),
	}

	// Several steps have nothing to add to what went wrong and say so: their
	// message in the catalog is "%w" and the whole sentence comes from further
	// down — the DNS step, the firewall backend. When that deeper failure
	// carries a name of its own, the name travels with it, because the code
	// here would otherwise translate to "%s" and leave the reader with the
	// English sentence inside it.
	if format == "%w" && len(args) == 1 {
		if err, ok := args[0].(error); ok {
			if inner, innerArgs, tagged := fault.CodeOf(err); tagged {
				se.code, se.args = inner, innerArgs
			}
		}
	}
	return se
}

// textArgs renders the interpolated values as plain strings, which is what
// travels in JSON. An error among them becomes its own message: it usually
// came from the kernel or the core and has no translation anywhere.
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

// withDetail attaches the underlying output that explains the failure.
func (s *stepError) withDetail(detail string) *stepError {
	s.detail = strings.TrimRight(detail, "\n")
	return s
}

// withLines attaches a process's recent output as the detail.
func (s *stepError) withLines(lines []string) *stepError {
	return s.withDetail(strings.Join(lines, "\n"))
}

// withHint attaches an actionable next step, from the hint catalog.
func (s *stepError) withHint(code string, args ...any) *stepError {
	if code == "" {
		return s
	}
	format, ok := hints[code]
	if !ok {
		format = code
	}
	s.hint = fmt.Sprintf(format, args...)
	s.hintCode = code
	s.hintArgs = textArgs(args)
	return s
}

// from attributes the failure to a component other than the daemon, so the log
// says "xray" or "tunnel" rather than blaming us for the core's complaint.
func (s *stepError) from(source model.Source) *stepError {
	s.source = source
	return s
}

// record files a failure in the log and the error journal, unwrapping the step
// context when there is any. Errors that come from elsewhere — a store read, a
// validation helper — still get recorded, just without a step.
func (e *Engine) record(err error) {
	if err == nil {
		return
	}
	var se *stepError
	if errors.As(err, &se) {
		e.Log.Fail(Fault{
			Source: se.source, Step: se.step, Err: err,
			Detail: se.detail, Hint: se.hint,
			Code: se.code, Args: se.args,
			HintCode: se.hintCode, HintArgs: se.hintArgs,
		})
		return
	}
	e.Log.Fail(Fault{Source: model.SourceDaemon, Step: model.StepNone, Err: err})
}
