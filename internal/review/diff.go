// Package review is the core of diff review: the story's diff, its comments and the prompt
// that sends them to Claude. bx serve is its only user.
package review

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/git"
)

const (
	MaxRows  = 2000 // rows of one file before it is folded
	MaxFiles = 300  // files listed; Total still counts all
)

// Result is a parsed diff: the files listed, how many changed in all, and a note on what is
// left out or what the diff starts from.
type Result struct {
	Files []api.DiffFile
	Total int
	Note  string
}

// Diff is a worktree's changes since its merge-base with base: commits, staged and unstaged
// changes, and untracked files. Untracked files are added with intent-to-add to a copy of the
// index, so the real index is never touched. With no merge-base (base gone or unrelated) the
// diff starts from HEAD and Note says so.
func Diff(ctx context.Context, g *git.Git, dir, base string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	from, note := "HEAD", ""
	if base != "" {
		if mb, err := g.MergeBase(ctx, dir, base); err == nil && mb != "" {
			from = mb
		} else {
			note = "no merge-base with " + base + " · showing uncommitted changes only"
		}
	}
	env, cleanup, err := tempIndex(ctx, g, dir)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	if _, err := g.Output(ctx, dir, env, "add", "-N", "--", "."); err != nil {
		return Result{}, err
	}
	out, err := g.Output(ctx, dir, env, "-c", "core.quotepath=off", "diff", "--no-color", "--no-ext-diff", "-M",
		"--src-prefix=a/", "--dst-prefix=b/", from)
	if err != nil {
		return Result{}, err
	}
	files := Parse(out)
	r := Result{Files: files, Total: len(files), Note: note}
	if len(files) > MaxFiles {
		r.Files = files[:MaxFiles]
		r.Note = joinNote(note, fmt.Sprintf("showing %d of %d files", MaxFiles, len(files)))
	}
	return r, nil
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + " · " + b
}

// tempIndex copies the worktree's index to a temp folder and returns GIT_INDEX_FILE for it.
// With no index yet, the path names a file that does not exist: git starts an empty index
// there, while an empty file would be a corrupt one.
func tempIndex(ctx context.Context, g *git.Git, dir string) ([]string, func(), error) {
	out, err := g.Output(ctx, dir, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return nil, nil, err
	}
	src := strings.TrimSpace(string(out))
	if !filepath.IsAbs(src) {
		src = filepath.Join(dir, src)
	}
	tmp, err := os.MkdirTemp("", "bx-review-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	dst := filepath.Join(tmp, "index")
	data, err := os.ReadFile(src)
	switch {
	case err == nil:
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			cleanup()
			return nil, nil, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		cleanup()
		return nil, nil, err
	}
	return []string{"GIT_INDEX_FILE=" + dst}, cleanup, nil
}

// Parse reads `git diff` output (a/ and b/ prefixes, no colour). Header lines count only
// before a file's first hunk, so an added line "+++ x" stays a row. A file over MaxRows rows
// is folded.
func Parse(out []byte) []api.DiffFile {
	var files []api.DiffFile
	var oldN, newN int
	inHunk := false
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			old, path := gitPaths(rest)
			files = append(files, api.DiffFile{Path: path, Old: old, Status: "modified"})
			inHunk = false
			continue
		}
		if len(files) == 0 {
			continue
		}
		f := &files[len(files)-1]
		if strings.HasPrefix(line, "@@") {
			oldN, newN = hunkStart(line)
			f.Hunks = append(f.Hunks, api.Hunk{Header: line})
			inHunk = true
			continue
		}
		if !inHunk {
			header(f, line)
			continue
		}
		h := &f.Hunks[len(f.Hunks)-1]
		switch {
		case strings.HasPrefix(line, "+"):
			h.Rows = append(h.Rows, api.DiffRow{Kind: "add", New: newN, Text: line[1:]})
			newN++
			f.Add++
		case strings.HasPrefix(line, "-"):
			h.Rows = append(h.Rows, api.DiffRow{Kind: "del", Old: oldN, Text: line[1:]})
			oldN++
			f.Del++
		case strings.HasPrefix(line, " "):
			h.Rows = append(h.Rows, api.DiffRow{Kind: "ctx", Old: oldN, New: newN, Text: line[1:]})
			oldN++
			newN++
		}
		// "\ No newline at end of file" and the final "" are skipped
	}
	for i := range files {
		f := &files[i]
		if f.Status != "renamed" {
			f.Old = ""
		}
		if f.Add+f.Del > MaxRows {
			f.Folded = fmt.Sprintf("%d lines changed · too long to show", f.Add+f.Del)
			f.Hunks = nil
		}
	}
	return files
}

// header reads one line of a file's header: its mode, a rename, the new path or a binary note.
func header(f *api.DiffFile, line string) {
	switch {
	case strings.HasPrefix(line, "new file mode"):
		f.Status = "added"
	case strings.HasPrefix(line, "deleted file mode"):
		f.Status = "deleted"
	case strings.HasPrefix(line, "rename from "):
		f.Old, f.Status = unquote(strings.TrimPrefix(line, "rename from ")), "renamed"
	case strings.HasPrefix(line, "rename to "):
		f.Path, f.Status = unquote(strings.TrimPrefix(line, "rename to ")), "renamed"
	case strings.HasPrefix(line, "+++ "):
		p := unquote(strings.TrimSuffix(strings.TrimPrefix(line, "+++ "), "\t"))
		if rest, ok := strings.CutPrefix(p, "b/"); ok {
			f.Path = rest
		}
	case strings.HasPrefix(line, "Binary files "):
		f.Folded = "binary file"
	}
}

// gitPaths splits the "a/x b/y" of a diff --git line. Quoted paths are unquoted; unquoted
// ones are split in the middle when both halves name the same file (paths with spaces),
// else at the first " b/".
func gitPaths(s string) (old, path string) {
	if strings.HasPrefix(s, `"`) {
		if q, err := strconv.QuotedPrefix(s); err == nil {
			a, _ := strconv.Unquote(q)
			b := unquote(strings.TrimPrefix(s[len(q):], " "))
			return strings.TrimPrefix(a, "a/"), strings.TrimPrefix(b, "b/")
		}
	}
	if half := (len(s) - 1) / 2; len(s)%2 == 1 && s[half] == ' ' {
		a, b := s[:half], s[half+1:]
		if strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") && a[2:] == b[2:] {
			return a[2:], b[2:]
		}
	}
	if i := strings.Index(s, " b/"); i >= 0 {
		return strings.TrimPrefix(s[:i], "a/"), unquote(s[i+3:])
	}
	return s, s
}

func unquote(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// hunkStart reads the first old and new line numbers of "@@ -a,b +c,d @@".
func hunkStart(line string) (int, int) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return 0, 0
	}
	o, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(f[1], "-"), ",", 2)[0])
	n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(f[2], "+"), ",", 2)[0])
	return o, n
}
