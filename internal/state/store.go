package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// CorruptError is returned when the file does not parse. The file has been moved aside.
type CorruptError struct {
	Path       string
	Quarantine string
	Err        error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("state file %s is corrupt (moved to %s): %v", e.Path, e.Quarantine, e.Err)
}
func (e *CorruptError) Unwrap() error { return e.Err }

// Load reads path. A missing file yields an empty project. A corrupt file is renamed
// <name>.corrupt-<timestamp>.json and a *CorruptError is returned; bx never starts from
// an empty store silently.
func Load(path, name string) (*Project, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(name, ""), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, quarantine(path, err)
	}
	if probe.Version > CurrentVersion {
		return nil, fmt.Errorf("state file %s has version %d, this bx reads up to %d", path, probe.Version, CurrentVersion)
	}
	data, err = migrate(probe.Version, data)
	if err != nil {
		return nil, quarantine(path, err)
	}
	p := &Project{}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, quarantine(path, err)
	}
	p.Name = name
	p.Version = CurrentVersion
	return p, nil
}

// migrate upgrades older shapes to CurrentVersion. Version 0 (no field) is treated as 1.
func migrate(version int, data []byte) ([]byte, error) {
	switch version {
	case 0, 1, 2:
		// v1 → v2 only removed layout fields (window, list_pane, selected, folded, pane);
		// unmarshalling drops unknown keys, so the bytes need no change.
		return data, nil
	}
	return nil, fmt.Errorf("no migration from version %d", version)
}

func quarantine(path string, cause error) error {
	q := strings.TrimSuffix(path, ".json") + ".corrupt-" + time.Now().Format("20060102-150405") + ".json"
	if err := os.Rename(path, q); err != nil {
		return fmt.Errorf("state file %s is corrupt and could not be moved: %v (%w)", path, err, cause)
	}
	return &CorruptError{Path: path, Quarantine: q, Err: cause}
}

// Save writes p atomically: temp file in the same folder, fsync, rename.
func Save(path string, p *Project) error {
	p.Version = CurrentVersion
	data, err := json.MarshalIndent(p, "", "  ")
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
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Locker holds <path>.lock with flock. One `bx list` per project.
type Locker struct{ f *os.File }

// Lock takes an exclusive, non-blocking lock. It fails when another live process holds it.
func Lock(path string) (*Locker, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another bx process holds %s.lock", path)
	}
	f.Truncate(0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return &Locker{f: f}, nil
}

// Unlock releases the lock.
func (l *Locker) Unlock() {
	if l == nil || l.f == nil {
		return
	}
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	l.f = nil
}

// ListProjects returns the project names that have a state file in dir.
func ListProjects(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json") && !strings.Contains(n, ".corrupt-") {
			out = append(out, strings.TrimSuffix(n, ".json"))
		}
	}
	return out, nil
}
