// Package claudesync copies Claude Code's skills, agents, commands and rules from the Mac to
// the bx server, item by item (see ItemOf). Within the items of a sync the Mac always wins: a
// file it has is added or overwritten, a file bx synced before and the Mac no longer has is
// deleted, and a file only the server has, that bx never synced, is kept. Other items are not
// touched. The server remembers what bx synced in a record: a JSON list of paths relative to
// the Claude config dir, e.g. "skills/brainstorm/SKILL.md".
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

// ValidPath checks a synced path: relative, slash-separated, clean, a file below one of
// Folders, with no empty, "..", or hidden (dot) segment and no backslash or NUL.
func ValidPath(p string) error {
	bad := fmt.Errorf("bad path %q: %w", p, ErrInvalid)
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return bad
	}
	segs := strings.Split(p, "/")
	if len(segs) < 2 || !slices.Contains(Folders, segs[0]) {
		return bad
	}
	for _, s := range segs {
		if s == "" || strings.HasPrefix(s, ".") { // also "." and ".."
			return bad
		}
	}
	return nil
}

// ItemOf is the item a path belongs to, its first two segments: a skill folder
// ("skills/a/b/c.md" is "skills/a"), or an entry of agents, commands or rules, a file
// ("agents/x.md") or a sub-folder ("agents/sub/y.md" is "agents/sub").
func ItemOf(p string) string {
	segs := strings.SplitN(p, "/", 3)
	if len(segs) < 2 {
		return p
	}
	return segs[0] + "/" + segs[1]
}

// ValidItem checks an item: a valid path of exactly two segments.
func ValidItem(it string) error {
	if err := ValidPath(it); err != nil || strings.Count(it, "/") != 1 {
		return fmt.Errorf("bad item %q: %w", it, ErrInvalid)
	}
	return nil
}

// File is one file of a scan. Src is its absolute path on the Mac (empty from ScanServer).
type File struct {
	Path   string
	SHA256 string
	Size   int64
	Exec   bool
	Src    string
}

// MacScan is a scan of the Mac. Links holds the items whose top entry ("skills/a",
// "agents/x.md") is a symlink. Warnings holds, per item, the files skipped for a bad name.
type MacScan struct {
	Files    []File
	Links    map[string]bool
	Warnings map[string]string
}

// Scan lists the files of Folders under dir on the Mac, sorted by path. It follows symlinked
// files and directories (a directory already on the current path is skipped, so a loop ends),
// and skips names starting with "." at any depth, broken links and non-regular files. A file
// whose path isn't valid (a backslash in its name) is skipped with a warning on its item.
func Scan(dir string) (MacScan, error) {
	sc := MacScan{Links: map[string]bool{}, Warnings: map[string]string{}}
	skipped := map[string][]string{}
	for _, f := range Folders {
		top := filepath.Join(dir, f)
		fi, err := os.Stat(top)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return MacScan{}, err
		}
		if !fi.IsDir() {
			continue
		}
		real, err := filepath.EvalSymlinks(top)
		if err != nil {
			return MacScan{}, err
		}
		if err := scanDir(top, f, map[string]bool{real: true}, &sc, skipped); err != nil {
			return MacScan{}, err
		}
	}
	for it, rels := range skipped {
		w := "skipped " + rels[0] + ": bad name"
		if len(rels) > 1 {
			w += fmt.Sprintf(" (+%d more)", len(rels)-1)
		}
		sc.Warnings[it] = w
	}
	sort.Slice(sc.Files, func(i, j int) bool { return sc.Files[i].Path < sc.Files[j].Path })
	return sc, nil
}

// scanDir adds the files below abs (rel is its slash path) to sc, and the files it skips for
// a bad name to skipped by item. seen holds the real paths of the directories from the top
// folder down to abs.
func scanDir(abs, rel string, seen map[string]bool, sc *MacScan, skipped map[string][]string) error {
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
		if !strings.Contains(rel, "/") && e.Type()&fs.ModeSymlink != 0 {
			sc.Links[r] = true
		}
		switch {
		case fi.IsDir():
			real, err := filepath.EvalSymlinks(p)
			if err != nil || seen[real] {
				continue
			}
			seen[real] = true
			err = scanDir(p, r, seen, sc, skipped)
			delete(seen, real)
			if err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if ValidPath(r) != nil {
				it := ItemOf(r)
				skipped[it] = append(skipped[it], r)
				continue
			}
			sum, size, err := hashFile(p)
			if err != nil {
				return err
			}
			sc.Files = append(sc.Files, File{Path: r, SHA256: sum, Size: size, Exec: fi.Mode()&0o100 != 0, Src: p})
		}
	}
	return nil
}

// ScanServer lists the files of Folders under dir on the server, by path. It never follows a
// symlink: a symlinked top folder, directory or file is skipped, as are hidden names and
// non-regular files.
func ScanServer(dir string) (map[string]File, error) {
	out := map[string]File{}
	for _, f := range Folders {
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
			if ValidPath(rel) != nil {
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
