// Package claudesync copies Claude Code's skills, agents, commands and rules from the Mac to
// the bx server. The Mac always wins: a file it has is added or overwritten, a file bx synced
// before and the Mac no longer has is deleted, and a file only the server has, that bx never
// synced, is kept. The server remembers what bx synced in a record: a JSON list of paths
// relative to the Claude config dir, e.g. "skills/brainstorm/SKILL.md".
package claudesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Folders are the folders of the Claude config dir that bx syncs, in display order.
var Folders = []string{"skills", "agents", "commands", "rules"}

// Limits of one apply: per file and in total, decoded.
const (
	MaxFile  = 1 << 20
	MaxTotal = 20 << 20
)

// ErrInvalid marks a bad folder or path; ErrTooLarge a file or request over the limits.
var (
	ErrInvalid  = errors.New("invalid")
	ErrTooLarge = errors.New("too large")
)

// ValidFolders checks that folders is non-empty and each one is in Folders.
func ValidFolders(folders []string) error {
	if len(folders) == 0 {
		return fmt.Errorf("no folders: %w", ErrInvalid)
	}
	for _, f := range folders {
		if !slices.Contains(Folders, f) {
			return fmt.Errorf("unknown folder %q: %w", f, ErrInvalid)
		}
	}
	return nil
}

// ValidPath checks a synced path: relative, slash-separated, clean, a file below one of folders,
// with no empty, "..", or hidden (dot) segment and no backslash or NUL.
func ValidPath(p string, folders []string) error {
	bad := fmt.Errorf("bad path %q: %w", p, ErrInvalid)
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return bad
	}
	segs := strings.Split(p, "/")
	if len(segs) < 2 || !slices.Contains(folders, segs[0]) || !slices.Contains(Folders, segs[0]) {
		return bad
	}
	for _, s := range segs {
		if s == "" || strings.HasPrefix(s, ".") { // also "." and ".."
			return bad
		}
	}
	return nil
}

// under reports whether p's first segment is one of folders.
func under(p string, folders []string) bool {
	top, _, _ := strings.Cut(p, "/")
	return slices.Contains(folders, top)
}

// File is one file of a scan. Src is its absolute path on the Mac (empty from ScanServer).
type File struct {
	Path   string
	SHA256 string
	Size   int64
	Exec   bool
	Src    string
}

// Scan lists the files of folders under dir on the Mac, sorted by path. It follows symlinked
// files and directories (a directory already on the current path is skipped, so a loop ends),
// and skips names starting with "." at any depth, broken links and non-regular files.
func Scan(dir string, folders []string) ([]File, error) {
	var out []File
	for _, f := range folders {
		top := filepath.Join(dir, f)
		fi, err := os.Stat(top)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			continue
		}
		real, err := filepath.EvalSymlinks(top)
		if err != nil {
			return nil, err
		}
		if err := scanDir(top, f, map[string]bool{real: true}, &out); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// scanDir adds the files below abs (rel is its slash path) to out. seen holds the real paths
// of the directories from the top folder down to abs.
func scanDir(abs, rel string, seen map[string]bool, out *[]File) error {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, r := filepath.Join(abs, e.Name()), rel+"/"+e.Name()
		fi, err := os.Stat(p) // follows a symlink
		if err != nil {
			continue // a broken link
		}
		switch {
		case fi.IsDir():
			real, err := filepath.EvalSymlinks(p)
			if err != nil || seen[real] {
				continue
			}
			seen[real] = true
			err = scanDir(p, r, seen, out)
			delete(seen, real)
			if err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if err := ValidPath(r, Folders); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			sum, size, err := hashFile(p)
			if err != nil {
				return err
			}
			*out = append(*out, File{Path: r, SHA256: sum, Size: size, Exec: fi.Mode()&0o100 != 0, Src: p})
		}
	}
	return nil
}

// ScanServer lists the files of folders under dir on the server, by path. It never follows a
// symlink: a symlinked top folder, directory or file is skipped, as are hidden names and
// non-regular files.
func ScanServer(dir string, folders []string) (map[string]File, error) {
	out := map[string]File{}
	for _, f := range folders {
		top := filepath.Join(dir, f)
		fi, err := os.Lstat(top)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() { // a symlink too
			continue
		}
		err = filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == top {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if ValidPath(rel, folders) != nil {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			sum, size, err := hashFile(p)
			if err != nil {
				return err
			}
			out[rel] = File{Path: rel, SHA256: sum, Size: size, Exec: info.Mode()&0o100 != 0}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// LoadRecord reads the paths bx synced. A missing record is empty.
func LoadRecord(p string) ([]string, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// SaveRecord writes the record, sorted, atomically.
func SaveRecord(p string, paths []string) error {
	paths = slices.Clone(paths)
	if paths == nil {
		paths = []string{}
	}
	sort.Strings(paths)
	b, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p, append(b, '\n'), 0o644)
}

// writeAtomic writes a temp file in p's folder and renames it over p.
func writeAtomic(p string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".bx-sync-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
