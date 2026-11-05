// Package runcfg is a project's run config, ~/.bx/runs/<project>.toml: the runs a story
// runs, how and where. Settings › Runs edits it; bx run-exec runs it.
package runcfg

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// File is a run config.
type File struct {
	Runs []Run `toml:"run"`
	Hide []int `toml:"hide"` // ports the story list leaves out, besides ports.Hidden
	Show []int `toml:"show"` // ports of ports.Hidden it shows
}

// Run is one app of a story.
type Run struct {
	Name    string            `toml:"name"`
	Cmd     string            `toml:"cmd"`
	Dir     string            `toml:"dir"`   // in the story folder; in a workspace, a repo
	Image   string            `toml:"image"` // run in this Docker image; "" on the host
	Build   string            `toml:"build"` // a Dockerfile to build the image from
	Env     map[string]string `toml:"env"`
	Setup   Setup             `toml:"setup"`
	Creates string            `toml:"creates"` // setup is due while this path is missing
	Stop    string            `toml:"stop"`    // run on the host after C-c, in Dir
	Open    string            `toml:"open"`    // the path the link opens, "/login"
	Port    Ports             `toml:"port"`    // the ports it should listen on, for "starting…"
	Cache   []string          `toml:"cache"`   // container paths kept between runs
}

// Docker reports whether the run is in a container.
func (r Run) Docker() bool { return r.Image != "" || r.Build != "" }

// Setup is what a fresh worktree runs once before the run: a command, alone or with an image.
type Setup struct {
	Cmd   string
	Image string
}

// UnmarshalTOML takes "npm ci" or { cmd = "composer install", image = "composer:2" }.
func (s *Setup) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		*s = Setup{Cmd: t}
	case map[string]any:
		for k, x := range t {
			str, ok := x.(string)
			if !ok {
				return fmt.Errorf("setup.%s must be a string", k)
			}
			switch k {
			case "cmd":
				s.Cmd = str
			case "image":
				s.Image = str
			default:
				return fmt.Errorf("setup has no key %q", k)
			}
		}
	default:
		return fmt.Errorf("setup must be a string or a table, got %T", v)
	}
	return nil
}

// Ports takes 3000 or [3000, 3001].
type Ports []int

func (p *Ports) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case int64:
		*p = Ports{int(t)}
	case []any:
		out := Ports{}
		for _, x := range t {
			n, ok := x.(int64)
			if !ok {
				return fmt.Errorf("port must be a number, got %T", x)
			}
			out = append(out, int(n))
		}
		*p = out
	default:
		return fmt.Errorf("port must be a number or a list, got %T", v)
	}
	return nil
}

// SyntaxError is a config that is not TOML, or has a key bx does not know. Line is 1-based,
// 0 when unknown.
type SyntaxError struct {
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

// Parse reads a run config. An empty one is a File with no runs.
func Parse(data []byte) (File, error) {
	var f File
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			return File{}, &SyntaxError{Line: pe.Position.Line, Msg: pe.Message}
		}
		return File{}, &SyntaxError{Msg: err.Error()}
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return File{}, &SyntaxError{Msg: "unknown key " + strings.Join(keys, ", ")}
	}
	return f, nil
}

// FieldError is a refused field of the Run-th run (0-based).
type FieldError struct {
	Run     int    `json:"run"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e FieldError) Error() string { return fmt.Sprintf("run %d: %s: %s", e.Run+1, e.Field, e.Message) }

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	envRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate checks f. repos are a workspace's repositories (nil for a repo project): a run's
// dir must then start with one of them.
func Validate(f File, repos []string) []FieldError {
	var out []FieldError
	bad := func(i int, field, msg string) { out = append(out, FieldError{i, field, msg}) }
	seen := map[string]bool{}
	for i, r := range f.Runs {
		switch {
		case r.Name == "":
			bad(i, "name", "required")
		case seen[r.Name]:
			bad(i, "name", fmt.Sprintf("%q is taken", r.Name))
		case !nameRe.MatchString(r.Name):
			bad(i, "name", "letters, digits, - and _ only")
		}
		seen[r.Name] = true
		if strings.TrimSpace(r.Cmd) == "" {
			bad(i, "cmd", "required")
		}
		if r.Image != "" && r.Build != "" {
			bad(i, "build", "image or build, not both")
		}
		if r.Dir != "" {
			first := strings.Split(filepath.ToSlash(r.Dir), "/")[0]
			switch {
			case !filepath.IsLocal(r.Dir):
				bad(i, "dir", "a folder inside the story")
			case repos != nil && !slices.Contains(repos, first):
				bad(i, "dir", "one of the repos: "+strings.Join(repos, ", "))
			}
		}
		for _, p := range r.Port {
			if p < 1 || p > 65535 {
				bad(i, "port", "1 to 65535")
				break
			}
		}
		if len(r.Cache) > 0 && !r.Docker() {
			bad(i, "cache", "needs image or build")
		}
		names := make([]string, 0, len(r.Env))
		for k := range r.Env {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if !envRe.MatchString(k) {
				bad(i, "env", fmt.Sprintf("%q is not a variable name", k))
				break
			}
		}
		if r.Setup.Image != "" && r.Setup.Cmd == "" {
			bad(i, "setup", "an image needs a command")
		}
	}
	return out
}

// Names are the runs' names, in order.
func (f File) Names() []string {
	out := make([]string, len(f.Runs))
	for i, r := range f.Runs {
		out[i] = r.Name
	}
	return out
}

// Find is the run called name.
func (f File) Find(name string) (Run, bool) {
	i := slices.IndexFunc(f.Runs, func(r Run) bool { return r.Name == name })
	if i < 0 {
		return Run{}, false
	}
	return f.Runs[i], true
}
