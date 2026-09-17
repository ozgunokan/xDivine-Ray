package model

import "strings"

// Level is a log severity.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warning"
	LevelError Level = "error"
)

// Levels lists severities from least to most severe, which is also the order a
// filter uses: asking for "warning" means warning and error.
func Levels() []Level {
	return []Level{LevelDebug, LevelInfo, LevelWarn, LevelError}
}

// Rank returns a comparable severity, higher being more severe.
func (l Level) Rank() int {
	switch l {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	default:
		return 1
	}
}

// AtLeast reports whether l is as severe as min.
func (l Level) AtLeast(min Level) bool { return l.Rank() >= min.Rank() }

// ParseLevel reads a level name, accepting the spellings the core and the
// operator use interchangeably.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, true
	case "info", "information":
		return LevelInfo, true
	case "warn", "warning":
		return LevelWarn, true
	case "error", "err":
		return LevelError, true
	default:
		return LevelInfo, false
	}
}

// Source says which component produced a log entry. Knowing this is usually
// the first step in reading a failure: a core error and a firewall error need
// entirely different fixes.
type Source string

const (
	// SourceDaemon is xwrt itself.
	SourceDaemon Source = "xwrt"
	// SourceCore is the Xray process.
	SourceCore Source = "xray"
	// SourceTunnel is hev-socks5-tunnel.
	SourceTunnel Source = "tunnel"
)

// Step names a stage of the connect sequence. An error carrying one says where
// the sequence stopped, which narrows the cause far more than the message
// alone: "permission denied" means something different in each stage.
type Step string

const (
	StepNone      Step = ""
	StepConfig    Step = "config"    // generating and validating the core config
	StepCore      Step = "core"      // starting the proxy core
	StepTunnel    Step = "tunnel"    // starting the TUN device and its routes
	StepFirewall  Step = "firewall"  // installing capture rules
	StepDNS       Step = "dns"       // pointing resolution at the core
	StepDetect    Step = "detect"    // discovering the device's network layout
	StepTeardown  Step = "teardown"  // undoing the above
	StepSubscribe Step = "subscribe" // fetching a subscription
)

// LogEntry is one line of the daemon's ring buffer.
type LogEntry struct {
	Time    string `json:"time"`
	Level   Level  `json:"level"`
	Source  Source `json:"source"`
	Step    Step   `json:"step,omitempty"`
	Message string `json:"message"`
}

// ErrorEntry is a failure recorded in the error journal. It keeps the same
// shape as a log entry plus whatever context helps explain it, so the journal
// can be read on its own without hunting through the log.
type ErrorEntry struct {
	Time    string `json:"time"`
	Source  Source `json:"source"`
	Step    Step   `json:"step,omitempty"`
	Message string `json:"message"`
	// Detail carries the underlying output that explains the failure: the
	// core's last lines before it died, or the rejected ruleset. It is the
	// part that usually contains the actual cause.
	Detail string `json:"detail,omitempty"`
	// Hint is an actionable next step when the daemon can work one out.
	Hint string `json:"hint,omitempty"`
	// Code names this failure in a way that does not change when its English
	// wording does, and Args carries the values that wording interpolates.
	//
	// They exist so the interface can say the same thing in the reader's own
	// language. Translating Message itself is not an option worth having: it
	// would mean matching English sentences with patterns, which breaks
	// silently the first time one of them is reworded — and an error message
	// that quietly reverts to a language the reader does not speak is worse
	// than one that was never translated, because nobody notices.
	//
	// Message stays in English regardless. It is what goes to syslog and what
	// gets pasted into a bug report, where one language everyone can search
	// for is worth more than the reader's own.
	Code string   `json:"code,omitempty"`
	Args []string `json:"args,omitempty"`
	// HintCode and HintArgs are the same for the hint.
	HintCode string   `json:"hint_code,omitempty"`
	HintArgs []string `json:"hint_args,omitempty"`
	// Repeats counts further occurrences of the same failure collapsed into
	// this entry. A core in a crash loop produces one failure every few
	// seconds; without this the journal would hold nothing else, and the count
	// is the useful part anyway — it says this is a loop, not a one-off.
	Repeats int `json:"repeats,omitempty"`
}

// LastError is the most recent failure, reported in status so a client does
// not have to fetch the journal to show what went wrong.
type LastError struct {
	Time    string `json:"time"`
	Step    Step   `json:"step,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	// Code, Args, HintCode and HintArgs carry the same translation handles as
	// ErrorEntry. Status is the one place most people ever see a failure, so
	// leaving them out here would mean translating the journal and not the
	// screen that actually gets looked at.
	Code     string   `json:"code,omitempty"`
	Args     []string `json:"args,omitempty"`
	HintCode string   `json:"hint_code,omitempty"`
	HintArgs []string `json:"hint_args,omitempty"`
}
