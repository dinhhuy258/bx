// Package state persists bx records per project: stories and sessions.
// It never stores derived data (live status, diff numbers). See the design doc.
package state

import (
	"encoding/json"
	"strconv"
	"time"
)

// CurrentVersion is written to every file and checked on read.
const CurrentVersion = 2

// Project is the content of ~/.bx/state/<project>.json.
type Project struct {
	Version  int               `json:"version"`
	Name     string            `json:"-"`
	Path     string            `json:"path"`
	Stories  map[string]*Story `json:"stories"`
	Sessions map[int]*Session  `json:"-"`
	NextNum  int               `json:"next_num"`
}

// Story is a branch with a worktree. The worktree path is derived from root, project and story.
type Story struct {
	Branch string `json:"branch"`
}

// Session is one Claude Code process. Story "" means the chats group. Model, Effort and
// PermissionMode are the settings it started with ("" = Claude's default); resume reuses them.
type Session struct {
	Story          string    `json:"-"`
	UUID           string    `json:"uuid"`
	Title          string    `json:"title"`
	Done           bool      `json:"done"`
	Created        time.Time `json:"created"`
	Model          string    `json:"model,omitempty"`
	Effort         string    `json:"effort,omitempty"`
	PermissionMode string    `json:"permission_mode,omitempty"`
}

// IsChat reports whether the session belongs to the chats group.
func (s *Session) IsChat() bool { return s.Story == "" }

// New returns an empty project record.
func New(name, path string) *Project {
	return &Project{Version: CurrentVersion, Name: name, Path: path, Stories: map[string]*Story{}, Sessions: map[int]*Session{}, NextNum: 1}
}

// AddSession assigns the next number and stores the session. Returns the number.
func (p *Project) AddSession(s *Session) int {
	n := p.NextNum
	p.NextNum++
	p.Sessions[n] = s
	return n
}

// SessionsOf lists session numbers of a story ("" = chats) in ascending order.
func (p *Project) SessionsOf(story string) []int {
	var out []int
	for n, s := range p.Sessions {
		if s.Story == story {
			out = append(out, n)
		}
	}
	sortInts(out)
	return out
}

// wire types: sessions are keyed by number as JSON strings, and story is null for chats.
type sessionWire struct {
	Story          *string   `json:"story"`
	UUID           string    `json:"uuid"`
	Title          string    `json:"title"`
	Done           bool      `json:"done"`
	Created        time.Time `json:"created"`
	Model          string    `json:"model,omitempty"`
	Effort         string    `json:"effort,omitempty"`
	PermissionMode string    `json:"permission_mode,omitempty"`
}

type projectWire struct {
	Version  int                     `json:"version"`
	Path     string                  `json:"path"`
	Stories  map[string]*Story       `json:"stories"`
	Sessions map[string]*sessionWire `json:"sessions"`
	NextNum  int                     `json:"next_num"`
}

// MarshalJSON writes the wire shape from the design doc.
func (p *Project) MarshalJSON() ([]byte, error) {
	w := projectWire{Version: CurrentVersion, Path: p.Path, Stories: p.Stories, Sessions: map[string]*sessionWire{}, NextNum: p.NextNum}
	if w.Stories == nil {
		w.Stories = map[string]*Story{}
	}
	for n, s := range p.Sessions {
		var story *string
		if s.Story != "" {
			st := s.Story
			story = &st
		}
		w.Sessions[strconv.Itoa(n)] = &sessionWire{Story: story, UUID: s.UUID, Title: s.Title, Done: s.Done, Created: s.Created,
			Model: s.Model, Effort: s.Effort, PermissionMode: s.PermissionMode}
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads the wire shape. Name is set by Load.
func (p *Project) UnmarshalJSON(b []byte) error {
	var w projectWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	p.Version, p.Path, p.NextNum = w.Version, w.Path, w.NextNum
	p.Stories = w.Stories
	if p.Stories == nil {
		p.Stories = map[string]*Story{}
	}
	p.Sessions = map[int]*Session{}
	for k, sw := range w.Sessions {
		n, err := strconv.Atoi(k)
		if err != nil {
			return err
		}
		s := &Session{UUID: sw.UUID, Title: sw.Title, Done: sw.Done, Created: sw.Created,
			Model: sw.Model, Effort: sw.Effort, PermissionMode: sw.PermissionMode}
		if sw.Story != nil {
			s.Story = *sw.Story
		}
		p.Sessions[n] = s
	}
	if p.NextNum < 1 {
		p.NextNum = 1
	}
	return nil
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
