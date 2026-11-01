// Package settings is the registry of the settings bx serve offers on the settings screen and
// the store of their values in ~/.bx/settings.json. A setting lives only here, never in
// config.toml. The client draws the screen from Sections, so a new setting is one Field.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind is how a field is edited. Only choice exists; toggle and text come when a setting needs them.
type Kind string

const Choice Kind = "choice"

// Field is one setting. Key is the section id, a dot, then the field id. For a choice, ""
// first means no value: the program's own default.
type Field struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Help    string   `json:"help,omitempty"` // shown under the fields when the cursor is on it
	Kind    Kind     `json:"kind"`
	Choices []string `json:"choices,omitempty"`
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
}}

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

// Validate checks value for key. "" (the default) is always valid.
func Validate(key, value string) error {
	f, ok := field(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	if value == "" || f.Kind != Choice || slices.Contains(f.Choices, value) {
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
		q := strings.TrimSuffix(path, ".json") + ".corrupt-" + time.Now().Format("20060102-150405") + ".json"
		if rerr := os.Rename(path, q); rerr != nil {
			return nil, fmt.Errorf("settings file %s is corrupt and could not be moved: %v (%w)", path, rerr, err)
		}
		logf("settings: %s is corrupt (moved to %s): %v · using the defaults", path, q, err)
		return s, nil
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
