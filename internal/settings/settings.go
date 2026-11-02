// Package settings is the registry of the settings bx serve offers on the settings screen and
// the store of their values in ~/.bx/settings.json. A setting lives only here, never in
// config.toml. The client draws the screen from Sections, so a new setting is one Field.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind is how a field is edited: a choice from a list, or a line of text. Toggle comes when a
// setting needs it.
type Kind string

const (
	Choice Kind = "choice"
	Text   Kind = "text" // an http:// or https:// URL; "" is off
)

// Field is one setting. Key is the section id, a dot, then the field id. For a choice, ""
// first means no value: the program's own default. A choice with a Default has no "": no value
// means Default. A TUIOnly field and its value are left out of what the web client gets (see
// Visible).
type Field struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Help    string   `json:"help,omitempty"` // shown under the fields when the cursor is on it
	Kind    Kind     `json:"kind"`
	Choices []string `json:"choices,omitempty"`
	Default string   `json:"default,omitempty"`
	TUIOnly bool     `json:"-"`
}

// Section is a group of fields, one entry on the left of the screen.
type Section struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Help   string  `json:"help,omitempty"`
	Fields []Field `json:"fields"`
}

// Sections is every setting, in screen order.
var Sections = []Section{{
	ID: "claude", Title: "Claude", Help: "For new sessions. Running and resumed ones keep what they started with.",
	Fields: []Field{
		{Key: "claude.model", Label: "Model", Kind: Choice, Help: "Model: --model. default = Claude's own",
			Choices: []string{"", "opus", "sonnet", "haiku", "fable"}},
		{Key: "claude.effort", Label: "Effort", Kind: Choice, Help: "Effort: --effort. default = Claude's own",
			Choices: []string{"", "low", "medium", "high", "xhigh", "max"}},
		{Key: "claude.permission_mode", Label: "Permission mode", Kind: Choice, Help: "Permission mode: --permission-mode. default = Claude's own",
			Choices: []string{"", "acceptEdits", "auto", "plan", "manual", "dontAsk", "bypassPermissions"}},
	},
}, {
	ID: "notifications", Title: "Notifications",
	Help: "Push messages through ntfy when a session needs you. The URLs are not shown on the phone.",
	Fields: []Field{
		{Key: "notifications.enabled", Label: "Enabled", Kind: Choice, Help: "Send notifications to ntfy. Turn off to pause them; the URLs stay set.",
			Choices: []string{"on", "off"}, Default: "on"},
		{Key: "notifications.ntfy_url", Label: "ntfy URL", Kind: Text, TUIOnly: true, Help: "ntfy URL: the topic bx posts to when a session needs you. Empty = notifications off"},
		{Key: "notifications.open_url", Label: "Open URL", Kind: Text, TUIOnly: true, Help: "Open URL: the web client a notification's tap opens, e.g. https://bx.example.ts.net. Empty = the serve.listen address"},
	},
}}

// Visible returns the sections and values a client may see. The TUI (tui) gets everything; the
// web client does not get the TUIOnly fields or their values (the ntfy URL holds a secret topic),
// nor a section left with no field.
func Visible(tui bool, values map[string]string) ([]Section, map[string]string) {
	if tui {
		return Sections, values
	}
	var secs []Section
	out := map[string]string{}
	for _, s := range Sections {
		var fields []Field
		for _, f := range s.Fields {
			if f.TUIOnly {
				continue
			}
			fields = append(fields, f)
			if v, ok := values[f.Key]; ok {
				out[f.Key] = v
			}
		}
		if len(fields) > 0 {
			s.Fields = fields
			secs = append(secs, s)
		}
	}
	return secs, out
}

// TUIOnlyKey reports whether key is a TUIOnly field.
func TUIOnlyKey(key string) bool {
	f, ok := field(key)
	return ok && f.TUIOnly
}

func field(key string) (Field, bool) {
	for _, s := range Sections {
		for _, f := range s.Fields {
			if f.Key == key {
				return f, true
			}
		}
	}
	return Field{}, false
}

// Validate checks value for key. "" (the default) is always valid. A text value must be an
// absolute http:// or https:// URL; its error does not repeat the value, which may be a secret.
func Validate(key, value string) error {
	f, ok := field(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	if value == "" {
		return nil
	}
	if f.Kind == Text {
		u, err := url.Parse(value)
		if err != nil || strings.ContainsAny(value, " \t\r\n") || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s: want an http:// or https:// URL", key)
		}
		return nil
	}
	if f.Kind != Choice || slices.Contains(f.Choices, value) {
		return nil
	}
	return fmt.Errorf("%s %q: want one of %s", key, value, strings.Join(slices.DeleteFunc(slices.Clone(f.Choices), func(c string) bool { return c == "" }), ", "))
}

// Store holds the values, only the keys that were changed, behind a mutex. A nil Store has no
// values, so every Get is "".
type Store struct {
	path   string
	mu     sync.Mutex
	values map[string]string
}

// Open loads path. A missing file has no values. A corrupt file is moved aside to
// <name>.corrupt-<timestamp>.json and an unknown key or bad value is dropped; both are logged,
// and the defaults are used, so bx serve still starts.
func Open(path string, logf func(format string, args ...any)) (*Store, error) {
	s, err := load(path, logf)
	var ce *corruptError
	if !errors.As(err, &ce) {
		return s, err
	}
	q := strings.TrimSuffix(path, ".json") + ".corrupt-" + time.Now().Format("20060102-150405") + ".json"
	if rerr := os.Rename(path, q); rerr != nil {
		return nil, fmt.Errorf("settings file %s is corrupt and could not be moved: %v (%w)", path, rerr, ce.err)
	}
	logf("settings: %s is corrupt (moved to %s): %v · using the defaults", path, q, ce.err)
	return &Store{path: path, values: map[string]string{}}, nil
}

// Read loads path like Open but never moves or writes it: a corrupt file is an error. It is
// for short-lived readers such as the bx notify hook, which must leave the file to bx serve.
func Read(path string) (*Store, error) {
	return load(path, func(string, ...any) {})
}

// corruptError is a settings file that is not JSON.
type corruptError struct {
	path string
	err  error
}

func (e *corruptError) Error() string {
	return fmt.Sprintf("settings file %s is corrupt: %v", e.path, e.err)
}

// load reads path: no values when it is missing, a *corruptError when it is not JSON, and a
// bad key or value dropped and logged.
func load(path string, logf func(format string, args ...any)) (*Store, error) {
	s := &Store{path: path, values: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, &corruptError{path, err}
	}
	for k, v := range raw {
		if err := Validate(k, v); err != nil {
			logf("settings: %s: %v · ignored", path, err)
			continue
		}
		if v != "" {
			s.values[k] = v
		}
	}
	return s, nil
}

// Get returns the value of key, or "" for the default.
func (s *Store) Get(key string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

// Values returns a copy of every value that is set.
func (s *Store) Values() map[string]string {
	out := map[string]string{}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.values {
		out[k] = v
	}
	return out
}

// Set checks every value, then writes them all to the file and to memory, or none. "" puts a
// key back to its default.
func (s *Store) Set(changes map[string]string) error {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys) // the same error for the same request
	for _, k := range keys {
		if err := Validate(k, changes[k]); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]string{}
	for k, v := range s.values {
		next[k] = v
	}
	for k, v := range changes {
		if v == "" {
			delete(next, k)
		} else {
			next[k] = v
		}
	}
	if err := write(s.path, next); err != nil {
		return err
	}
	s.values = next
	return nil
}

// write saves values atomically: temp file in the same folder, fsync, rename.
func write(path string, values map[string]string) error {
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
