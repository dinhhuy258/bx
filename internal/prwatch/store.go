package prwatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// File is a story's PRs as last seen, and when each thread was last sent to Claude.
// Heads are the origin branch heads by repo folder ("" in a repo project) at that look,
// to notice a push.
type File struct {
	Me        string            `json:"me,omitempty"`
	CheckedAt int64             `json:"checked_at,omitempty"`
	PRs       []ui.PRInfo       `json:"prs"`
	Sent      map[string]int64  `json:"sent,omitempty"` // by thread id
	Heads     map[string]string `json:"heads,omitempty"`
}

// Data is the reply of GET …/pr: the PRs with each thread's SentAt filled in.
func (f File) Data() ui.PRData {
	out := ui.PRData{Me: f.Me, CheckedAt: f.CheckedAt, PRs: make([]ui.PRInfo, len(f.PRs))}
	for i, p := range f.PRs {
		p.Threads = append([]ui.PRThread(nil), p.Threads...)
		for j := range p.Threads {
			p.Threads[j].SentAt = f.Sent[p.Threads[j].ID]
		}
		out.PRs[i] = p
	}
	return out
}

// MarkSent stamps the threads ids as sent at.
func (f *File) MarkSent(ids []string, at int64) {
	if f.Sent == nil {
		f.Sent = map[string]int64{}
	}
	for _, id := range ids {
		f.Sent[id] = at
	}
}

// Load reads a PR file. A missing file is empty; a corrupt one is moved aside and a
// *state.CorruptError returned.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, state.Quarantine(path, err)
	}
	return f, nil
}

// Save writes f atomically: a temp file in the same folder, fsync, rename.
func Save(path string, f File) error {
	data, err := json.MarshalIndent(f, "", "  ")
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
	name := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
