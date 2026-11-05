package review

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/state"
)

// File is a story's comment file. Version goes up on every save of the comments; a viewed
// mark doesn't change it, so marking a file never refuses another device's comments.
type File struct {
	Version  int             `json:"version"`
	Comments []api.Comment   `json:"comments"`
	Viewed   map[string]Mark `json:"viewed,omitempty"` // by path
}

// Load reads a comment file. A missing file is an empty review at version 0. A corrupt one
// is moved aside and a *state.CorruptError returned.
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
func Save(path string, f File) error { return writeJSON(path, f) }

// writeJSON writes v as indented JSON atomically: a temp file in the same folder, fsync, rename.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
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
