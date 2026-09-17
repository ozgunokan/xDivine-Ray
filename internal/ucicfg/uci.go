// Package ucicfg reads and writes OpenWrt UCI configuration.
//
// Configuration lives in UCI rather than in a database on purpose: it is what
// LuCI already knows how to edit, it survives sysupgrade via /etc/config, and
// it keeps the daemon free of an embedded SQL engine (which on a 64 MB
// mips router is the difference between fitting and not fitting).
//
// Two backends are supported. On a real OpenWrt box the `uci` binary is used,
// so staged changes and LuCI edits interleave correctly. Anywhere else the
// config file is parsed and rewritten directly, which keeps the package
// testable off-router.
package ucicfg

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Section is one `config <type> '<name>'` block.
type Section struct {
	Name    string
	Type    string
	Anon    bool
	Options map[string]string
	Lists   map[string][]string
	order   []string
}

// Get returns an option value, or the empty string.
func (s *Section) Get(opt string) string {
	if s == nil {
		return ""
	}
	return s.Options[opt]
}

// List returns a list option, or nil.
func (s *Section) List(opt string) []string {
	if s == nil {
		return nil
	}
	return s.Lists[opt]
}

// Set records an option, remembering first-seen order for serialization.
func (s *Section) Set(opt, val string) {
	if s.Options == nil {
		s.Options = map[string]string{}
	}
	if _, seen := s.Options[opt]; !seen {
		s.order = append(s.order, opt)
	}
	s.Options[opt] = val
}

// Package is a parsed UCI package.
type Package struct {
	Name     string
	Sections []*Section
}

// Find returns the named section, or nil.
func (p *Package) Find(name string) *Section {
	for _, s := range p.Sections {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// OfType returns every section of the given type, in file order.
func (p *Package) OfType(typ string) []*Section {
	var out []*Section
	for _, s := range p.Sections {
		if s.Type == typ {
			out = append(out, s)
		}
	}
	return out
}

// Parse reads UCI syntax, as produced by `uci export` and as stored in
// /etc/config/<package>.
func Parse(name, text string) *Package {
	pkg := &Package{Name: name}
	var cur *Section
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	anon := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kw, rest := splitKeyword(line)
		switch kw {
		case "package":
			if v := firstToken(rest); v != "" {
				pkg.Name = v
			}
		case "config":
			typ, secName := twoTokens(rest)
			cur = &Section{
				Type:    typ,
				Name:    secName,
				Options: map[string]string{},
				Lists:   map[string][]string{},
			}
			if cur.Name == "" {
				cur.Anon = true
				cur.Name = fmt.Sprintf("@%s[%d]", typ, anon)
				anon++
			}
			pkg.Sections = append(pkg.Sections, cur)
		case "option":
			if cur == nil {
				continue
			}
			opt, val := twoTokens(rest)
			if opt != "" {
				cur.Set(opt, val)
			}
		case "list":
			if cur == nil {
				continue
			}
			opt, val := twoTokens(rest)
			if opt == "" {
				continue
			}
			if _, seen := cur.Lists[opt]; !seen {
				cur.order = append(cur.order, opt)
			}
			cur.Lists[opt] = append(cur.Lists[opt], val)
		}
	}
	return pkg
}

// String serializes the package back to UCI syntax.
func (p *Package) String() string {
	var b strings.Builder
	b.WriteString("package " + p.Name + "\n\n")
	for _, s := range p.Sections {
		if s.Anon {
			b.WriteString("config " + s.Type + "\n")
		} else {
			b.WriteString("config " + s.Type + " " + quote(s.Name) + "\n")
		}
		seen := map[string]bool{}
		emit := func(opt string) {
			if seen[opt] {
				return
			}
			seen[opt] = true
			if vals, ok := s.Lists[opt]; ok {
				for _, v := range vals {
					b.WriteString("\tlist " + opt + " " + quote(v) + "\n")
				}
				return
			}
			if v, ok := s.Options[opt]; ok {
				b.WriteString("\toption " + opt + " " + quote(v) + "\n")
			}
		}
		for _, opt := range s.order {
			emit(opt)
		}
		// Anything added without going through Set still gets written, sorted
		// so output stays deterministic.
		var rest []string
		for opt := range s.Options {
			if !seen[opt] {
				rest = append(rest, opt)
			}
		}
		for opt := range s.Lists {
			if !seen[opt] {
				rest = append(rest, opt)
			}
		}
		sort.Strings(rest)
		for _, opt := range rest {
			emit(opt)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// UCI is a handle to the configuration backend.
type UCI struct {
	bin     string // path to the uci binary, empty when unavailable
	confDir string // /etc/config, or a test directory
}

// New returns a UCI handle, preferring the uci binary when present.
func New() *UCI {
	u := &UCI{confDir: "/etc/config"}
	if p, err := exec.LookPath("uci"); err == nil {
		u.bin = p
	}
	if d := os.Getenv("XWRT_CONFDIR"); d != "" {
		u.confDir = d
		// An explicit config dir means tests or a non-OpenWrt host: never
		// shell out to a system-wide uci that would read /etc/config.
		u.bin = ""
	}
	return u
}

// UsesBinary reports whether the uci binary backend is active.
func (u *UCI) UsesBinary() bool { return u.bin != "" }

func (u *UCI) path(pkg string) string { return filepath.Join(u.confDir, pkg) }

// Load reads and parses a package. A missing package yields an empty one.
func (u *UCI) Load(pkg string) (*Package, error) {
	if u.bin != "" {
		out, err := u.run("export", pkg)
		if err != nil {
			// `uci export` fails when the package does not exist yet.
			if _, statErr := os.Stat(u.path(pkg)); os.IsNotExist(statErr) {
				return &Package{Name: pkg}, nil
			}
			return nil, err
		}
		return Parse(pkg, out), nil
	}
	data, err := os.ReadFile(u.path(pkg))
	if os.IsNotExist(err) {
		return &Package{Name: pkg}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(pkg, string(data)), nil
}

// Save writes the package back. With the uci binary this replaces the package
// section by section and commits; otherwise the file is rewritten atomically.
func (u *UCI) Save(pkg *Package) error {
	if u.bin == "" {
		if err := os.MkdirAll(u.confDir, 0o755); err != nil {
			return err
		}
		tmp := u.path(pkg.Name) + ".tmp"
		if err := os.WriteFile(tmp, []byte(pkg.String()), 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, u.path(pkg.Name))
	}

	// Replace the package atomically from uci's point of view: stage a full
	// rewrite, then commit once. Reverting first drops any half-finished edits
	// left behind by an earlier failure.
	_, _ = u.run("revert", pkg.Name)
	old, err := u.Load(pkg.Name)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, s := range pkg.Sections {
		keep[s.Name] = true
	}
	for _, s := range old.Sections {
		if !keep[s.Name] && !s.Anon {
			if _, err := u.run("delete", pkg.Name+"."+s.Name); err != nil {
				return err
			}
		}
	}
	for _, s := range pkg.Sections {
		if s.Anon {
			continue // anonymous sections are not used by this daemon
		}
		if _, err := u.run("set", pkg.Name+"."+s.Name+"="+s.Type); err != nil {
			return err
		}
		prev := old.Find(s.Name)
		if prev != nil {
			for opt := range prev.Options {
				if _, still := s.Options[opt]; !still {
					_, _ = u.run("delete", pkg.Name+"."+s.Name+"."+opt)
				}
			}
			for opt := range prev.Lists {
				if _, still := s.Lists[opt]; !still {
					_, _ = u.run("delete", pkg.Name+"."+s.Name+"."+opt)
				}
			}
		}
		for opt, val := range s.Options {
			if _, err := u.run("set", pkg.Name+"."+s.Name+"."+opt+"="+val); err != nil {
				return err
			}
		}
		for opt, vals := range s.Lists {
			_, _ = u.run("delete", pkg.Name+"."+s.Name+"."+opt)
			for _, v := range vals {
				if _, err := u.run("add_list", pkg.Name+"."+s.Name+"."+opt+"="+v); err != nil {
					return err
				}
			}
		}
	}
	_, err = u.run("commit", pkg.Name)
	return err
}

// GetOption reads a single option from any package (network, firewall, ...).
// It returns the empty string when the option is unset.
func (u *UCI) GetOption(pkg, sec, opt string) string {
	key := pkg + "." + sec + "." + opt
	if u.bin != "" {
		out, err := u.run("-q", "get", key)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	p, err := u.Load(pkg)
	if err != nil {
		return ""
	}
	return p.Find(sec).Get(opt)
}

func (u *UCI) run(args ...string) (string, error) {
	cmd := exec.Command(u.bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("uci %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

func splitKeyword(line string) (kw, rest string) {
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i+1:])
}

func firstToken(s string) string {
	t, _ := twoTokens(s)
	return t
}

// twoTokens splits a UCI value line into at most two unquoted tokens.
func twoTokens(s string) (string, string) {
	first, rest := nextToken(s)
	second, _ := nextToken(rest)
	return first, second
}

func nextToken(s string) (tok, rest string) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", ""
	}
	switch s[0] {
	case '\'', '"':
		q := s[0]
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == q {
				return b.String(), s[i+1:]
			}
			if s[i] == '\\' && q == '"' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
				continue
			}
			b.WriteByte(s[i])
		}
		return b.String(), ""
	default:
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			return s, ""
		}
		return s[:i], s[i+1:]
	}
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
