// Package uploads stores the files sent from the phone for a session, at
// <bx root>/uploads/<project>/<num>/<YYYYMMDD-HHMMSS>-<safe name>: outside every repository,
// so worktrees never get untracked files. It never follows a symlink: every folder on the way
// must be a real directory, or nothing is written or removed. Dir, MakeDir and Remove take the
// base folder, so the session artifacts (<bx root>/artifacts) use them too.
package uploads

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxFile is the largest file Save accepts: 20 MB.
const MaxFile = 20 << 20

const (
	maxName  = 100 // bytes of a safe name
	maxExt   = 16  // bytes of an extension SafeName keeps when it shortens a name
	maxTries = 100
)

var (
	ErrTooLarge = errors.New("too large")
	ErrInvalid  = errors.New("invalid")
)

// Dir is the folder of one session's uploads: base/<project>/<num>.
func Dir(base, project string, num int) string {
	return filepath.Join(base, project, strconv.Itoa(num))
}

// SafeName keeps the last path element of name, replaces every rune outside [A-Za-z0-9._-]
// with _, drops leading dots and caps it at 100 bytes, keeping a short extension. An empty
// result is "file".
func SafeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := strings.TrimLeft(b.String(), ".")
	if len(s) > maxName {
		ext := filepath.Ext(s)
		if len(ext) > maxExt || len(ext) == len(s) {
			ext = ""
		}
		s = s[:maxName-len(ext)] + ext
	}
	if s == "" {
		return "file"
	}
	return s
}

// validProject refuses a project name that could leave base: empty, with a separator, or dotted.
func validProject(project string) error {
	if project == "" || strings.ContainsAny(project, `/\`) || strings.HasPrefix(project, ".") {
		return fmt.Errorf("project %q: %w", project, ErrInvalid)
	}
	return nil
}

// Save writes content as a new file in the session's folder and returns its absolute path.
// It never replaces a file: a name taken already gets -2, -3, … before its extension.
func Save(base, project string, num int, name string, content []byte, now time.Time) (string, error) {
	if err := validProject(project); err != nil {
		return "", err
	}
	if num <= 0 {
		return "", fmt.Errorf("session %d: %w", num, ErrInvalid)
	}
	safe := SafeName(name)
	if len(content) > MaxFile {
		return "", fmt.Errorf("%s: %d bytes, over 20 MB: %w", safe, len(content), ErrTooLarge)
	}
	dir, err := MakeDir(base, project, num)
	if err != nil {
		return "", err
	}
	final, err := publish(dir, now.Format("20060102-150405")+"-"+safe, content)
	if err != nil {
		return "", err
	}
	return filepath.Abs(final)
}

// MakeDir creates base, base/project and base/project/num (mode 0700), checking each is a
// real directory, and returns base/project/num.
func MakeDir(base, project string, num int) (string, error) {
	if err := validProject(project); err != nil {
		return "", err
	}
	if num <= 0 {
		return "", fmt.Errorf("session %d: %w", num, ErrInvalid)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if err := realDir(base, base); err != nil {
		return "", err
	}
	dir := base
	for _, part := range []string{project, strconv.Itoa(num)} {
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		if err := realDir(base, dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// realDir refuses p unless it is a directory itself, not a symlink to one.
func realDir(base, p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a directory, refusing", display(base, p))
	}
	return nil
}

// display names p from the uploads folder on, e.g. uploads/home-network.
func display(base, p string) string {
	rel, err := filepath.Rel(filepath.Dir(base), p)
	if err != nil {
		return p
	}
	return rel
}

// publish writes content to a temp file in dir and links it to the first free name.
// Link fails rather than overwrite, so two uploads in the same second never replace each other.
func publish(dir, name string, content []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= maxTries; i++ {
		final := filepath.Join(dir, name)
		if i > 1 {
			final = filepath.Join(dir, stem+"-"+strconv.Itoa(i)+ext)
		}
		err := os.Link(tmp.Name(), final)
		if err == nil {
			return final, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("%s: %d files by that name already", name, maxTries)
}

// Remove deletes a session's uploads. A missing folder is not an error; a symlink or a file
// on the way is, and nothing is removed.
func Remove(base, project string, num int) error {
	if err := validProject(project); err != nil {
		return err
	}
	for _, p := range []string{base, filepath.Join(base, project), Dir(base, project, num)} {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err := realDir(base, p); err != nil {
			return err
		}
	}
	return os.RemoveAll(Dir(base, project, num))
}
