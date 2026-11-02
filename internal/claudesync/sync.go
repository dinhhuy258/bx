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

// Item statuses of a plan.
const (
	StatusNew        = "new"         // no file of it on the server
	StatusChanged    = "changed"     // on both, something to add, update or delete
	StatusSame       = "same"        // on both, nothing to do
	StatusRemoved    = "removed"     // bx synced it, the Mac no longer has it, the server does
	StatusServerOnly = "server-only" // only on the server, never synced by bx: always kept
)

// Item is what a sync of one item would do on the server: its paths to add, update and
// delete. Error says why it can't sync (a symlink or a non-regular file on the server).
type Item struct {
	Name, Status        string
	Add, Update, Delete []string
	Error               string
	Synced              bool // bx synced it before: it is in the record
}

// MakePlan compares the Mac's files with the server's and the record, item by item, sorted by
// name. dir is the server's Claude config dir, checked for symlinks on the paths a sync would
// touch. An item bx synced that is gone from both the Mac and the server is left out.
func MakePlan(dir string, mac []File, server map[string]File, record []string) []Item {
	type sides struct {
		mac, server, record []string
	}
	by := map[string]*sides{}
	get := func(p string) *sides {
		it := ItemOf(p)
		if by[it] == nil {
			by[it] = &sides{}
		}
		return by[it]
	}
	onMac := map[string]File{}
	for _, f := range mac {
		onMac[f.Path] = f
		get(f.Path).mac = append(get(f.Path).mac, f.Path)
	}
	for p := range server {
		get(p).server = append(get(p).server, p)
	}
	for _, p := range record {
		get(p).record = append(get(p).record, p)
	}
	out := []Item{}
	for name, sd := range by {
		it := Item{Name: name, Synced: len(sd.record) > 0}
		for _, p := range sd.mac {
			s, ok := server[p]
			switch {
			case !ok:
				it.Add = append(it.Add, p)
			case s.SHA256 != onMac[p].SHA256 || s.Exec != onMac[p].Exec:
				it.Update = append(it.Update, p)
			}
		}
		for _, p := range sd.record {
			if _, ok := server[p]; ok {
				if _, ok := onMac[p]; !ok {
					it.Delete = append(it.Delete, p)
				}
			}
		}
		switch {
		case len(sd.mac) > 0 && len(sd.server) == 0:
			it.Status = StatusNew
		case len(sd.mac) > 0 && len(it.Add)+len(it.Update)+len(it.Delete) > 0:
			it.Status = StatusChanged
		case len(sd.mac) > 0:
			it.Status = StatusSame
		case len(it.Delete) > 0:
			it.Status = StatusRemoved
		case len(sd.server) > 0:
			it.Status = StatusServerOnly
		default:
			continue
		}
		sort.Strings(it.Add)
		sort.Strings(it.Update)
		sort.Strings(it.Delete)
		for _, p := range slices.Concat(it.Add, it.Update, it.Delete) {
			if err := checkNoSymlink(dir, p); err != nil {
				it.Error = err.Error()
				break
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
// items, the scope of the sync. paths is every Mac file of items. Nothing is touched when an
// argument is invalid (ErrInvalid: a path outside items, or a delete bx never synced or the Mac
// still has) or over the limits (ErrTooLarge). Apply stops at the first file it can't write or
// delete, but always saves the record at recordPath to match the disk: for items, the paths of
// the Mac and of the old record that are regular files on disk now (an old entry is only there
// when its delete failed, so the next sync retries it); other items' entries are kept as they
// were.
func Apply(dir, recordPath string, items, paths []string, files []Write, deletes []string) (Result, error) {
	var res Result
	if len(items) == 0 {
		return res, fmt.Errorf("no items: %w", ErrInvalid)
	}
	scope := map[string]bool{}
	for _, it := range items {
		if err := ValidItem(it); err != nil {
			return res, err
		}
		scope[it] = true
	}
	inScope := func(p string) error {
		if err := ValidPath(p); err != nil {
			return err
		}
		if !scope[ItemOf(p)] {
			return fmt.Errorf("%s: not in the synced items: %w", p, ErrInvalid)
		}
		return nil
	}
	var total int
	for _, f := range files {
		if err := inScope(f.Path); err != nil {
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
	onMac := map[string]bool{}
	for _, p := range paths {
		if err := inScope(p); err != nil {
			return res, err
		}
		onMac[p] = true
	}
	for _, d := range deletes {
		if err := inScope(d); err != nil {
			return res, err
		}
		if onMac[d] {
			return res, fmt.Errorf("delete %s: the Mac has it: %w", d, ErrInvalid)
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
		if !scope[ItemOf(r)] {
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
		if regularNoLink(dir, p) {
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
