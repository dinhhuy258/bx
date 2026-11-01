package claudesync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Plan is what a sync would do on the server: paths to add, update and delete, and the number
// of files that are the same on both sides (unchanged) or only on the server and never synced
// by bx (kept).
type Plan struct {
	Add, Update, Delete []string
	Unchanged, Kept     int
}

// Changes is the number of files the plan writes or deletes.
func (p Plan) Changes() int { return len(p.Add) + len(p.Update) + len(p.Delete) }

// MakePlan compares the Mac's files with the server's and the record, within folders only.
func MakePlan(mac []File, server map[string]File, record []string, folders []string) Plan {
	p := Plan{Add: []string{}, Update: []string{}, Delete: []string{}}
	onMac := map[string]bool{}
	for _, f := range mac {
		if !under(f.Path, folders) {
			continue
		}
		onMac[f.Path] = true
		s, ok := server[f.Path]
		switch {
		case !ok:
			p.Add = append(p.Add, f.Path)
		case s.SHA256 != f.SHA256 || s.Exec != f.Exec:
			p.Update = append(p.Update, f.Path)
		default:
			p.Unchanged++
		}
	}
	rec := map[string]bool{}
	for _, r := range record {
		if !under(r, folders) {
			continue
		}
		rec[r] = true
		if _, ok := server[r]; ok && !onMac[r] {
			p.Delete = append(p.Delete, r)
		}
	}
	for s := range server {
		if under(s, folders) && !onMac[s] && !rec[s] {
			p.Kept++
		}
	}
	sort.Strings(p.Add)
	sort.Strings(p.Update)
	sort.Strings(p.Delete)
	return p
}

// Write is one file to add or update.
type Write struct {
	Path    string
	Exec    bool
	Content []byte
}

// Result counts what Apply did.
type Result struct{ Added, Updated, Deleted int }

// Apply writes files and deletes deletes under dir, the server's Claude config dir, for
// folders. paths is every Mac file of folders. Nothing is touched when an argument is invalid
// (ErrInvalid) or over the limits (ErrTooLarge). Apply stops at the first file it can't write
// or delete, but always saves the record at recordPath to match the disk: for folders, the
// paths of the Mac and of the old record that are regular files on disk now (an old entry is
// only there when its delete failed, so the next sync retries it); other folders' entries are
// kept as they were.
func Apply(dir, recordPath string, folders, paths []string, files []Write, deletes []string) (Result, error) {
	var res Result
	if err := ValidFolders(folders); err != nil {
		return res, err
	}
	var total int
	for _, f := range files {
		if err := ValidPath(f.Path, folders); err != nil {
			return res, err
		}
		if len(f.Content) > MaxFile {
			return res, fmt.Errorf("%s: %d bytes, over 1 MB: %w", f.Path, len(f.Content), ErrTooLarge)
		}
		total += len(f.Content)
	}
	if total > MaxTotal {
		return res, fmt.Errorf("total %d bytes, over 20 MB: %w", total, ErrTooLarge)
	}
	for _, p := range slices.Concat(paths, deletes) {
		if err := ValidPath(p, folders); err != nil {
			return res, err
		}
	}
	record, err := LoadRecord(recordPath)
	if err != nil {
		return res, err
	}
	rec := map[string]bool{}
	for _, r := range record {
		rec[r] = true
	}
	for _, d := range deletes {
		if !rec[d] {
			return res, fmt.Errorf("delete %s: not synced by bx: %w", d, ErrInvalid)
		}
	}
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return res, err
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			return res, err
		}
	}

	err = apply(dir, files, deletes, &res)

	next := []string{}
	keep := map[string]bool{}
	for _, r := range record {
		if !under(r, folders) {
			next = append(next, r)
		} else {
			keep[r] = true
		}
	}
	for _, p := range paths {
		keep[p] = true
	}
	for _, f := range files {
		keep[f.Path] = true
	}
	for p := range keep {
		if under(p, folders) && regularNoLink(dir, p) {
			next = append(next, p)
		}
	}
	if serr := SaveRecord(recordPath, next); serr != nil {
		err = errors.Join(err, fmt.Errorf("save record: %w", serr))
	}
	return res, err
}

func apply(dir string, files []Write, deletes []string, res *Result) error {
	for _, f := range files {
		if err := writeFile(dir, f, res); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	for _, d := range deletes {
		if err := deleteFile(dir, d, res); err != nil {
			return fmt.Errorf("delete %s: %w", d, err)
		}
	}
	return nil
}

func writeFile(dir string, f Write, res *Result) error {
	if err := checkNoSymlink(dir, f.Path); err != nil {
		return err
	}
	target := filepath.Join(dir, filepath.FromSlash(f.Path))
	_, err := os.Lstat(target)
	existed := err == nil
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if f.Exec {
		mode = 0o755
	}
	if err := writeAtomic(target, f.Content, mode); err != nil {
		return err
	}
	if existed {
		res.Updated++
	} else {
		res.Added++
	}
	return nil
}

// deleteFile removes rel, then its empty parent folders up to, not including, the top folder.
func deleteFile(dir, rel string, res *Result) error {
	if err := checkNoSymlink(dir, rel); err != nil {
		return err
	}
	target := filepath.Join(dir, filepath.FromSlash(rel))
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	res.Deleted++
	for d := path.Dir(rel); strings.Contains(d, "/"); d = path.Dir(d) {
		abs := filepath.Join(dir, filepath.FromSlash(d))
		if fi, err := os.Lstat(abs); err != nil || !fi.IsDir() {
			break
		}
		if os.Remove(abs) != nil { // not empty
			break
		}
	}
	return nil
}

// checkNoSymlink refuses rel when an existing part of it below dir is a symlink, a parent is
// not a folder, or the target is not a regular file.
func checkNoSymlink(dir, rel string) error {
	segs := strings.Split(rel, "/")
	cur := dir
	for i, s := range segs {
		cur = filepath.Join(cur, s)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("%s: refusing to write through a symlink", strings.Join(segs[:i+1], "/"))
		case i < len(segs)-1 && !fi.IsDir():
			return fmt.Errorf("%s: not a folder", strings.Join(segs[:i+1], "/"))
		case i == len(segs)-1 && !fi.Mode().IsRegular():
			return fmt.Errorf("%s: not a regular file", rel)
		}
	}
	return nil
}

// regularNoLink reports whether rel is a regular file under dir with no symlink on the way.
func regularNoLink(dir, rel string) bool {
	if checkNoSymlink(dir, rel) != nil {
		return false
	}
	fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil && fi.Mode().IsRegular()
}
