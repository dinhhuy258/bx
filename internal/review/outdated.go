package review

import (
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
)

// Place puts the comments on the current diff. A comment keeps its lines when they are in one
// hunk of the diff on its side; else it moves to the nearest lines with the same code; else it
// is Outdated. A comment on a file that is no longer changed is Outdated and the file is added
// as "gone", so it still shows. Comments on a folded file are not checked. cs is not changed.
func Place(files []api.DiffFile, cs []api.Comment) ([]api.DiffFile, []api.Comment) {
	byPath := make(map[string]int, len(files))
	for i, f := range files {
		byPath[f.Path] = i
	}
	out := make([]api.Comment, len(cs))
	for i, c := range cs {
		c.Outdated = false
		fi, ok := byPath[c.File]
		switch {
		case !ok:
			c.Outdated = true
			byPath[c.File] = len(files)
			files = append(files, api.DiffFile{Path: c.File, Status: "gone", Folded: "no longer changed"})
		case files[fi].Status == "gone":
			c.Outdated = true
		case files[fi].Folded != "":
		default:
			if first, last, ok := place(files[fi], c); ok {
				c.Line = last
				if c.StartLine > 0 {
					c.StartLine = first
				}
			} else {
				c.Outdated = true
			}
		}
		out[i] = c
	}
	return files, out
}

// place finds the comment's lines in f: the same lines on its side inside one hunk, or the
// nearest run of lines inside one hunk with the same code. The old side is the removed rows;
// the new side is every other row. It returns the first and the last line.
func place(f api.DiffFile, c api.Comment) (int, int, bool) {
	n := c.Line - c.First() + 1
	code := strings.Split(c.Code, "\n")
	best, dist := 0, -1
	for _, h := range f.Hunks {
		var lines []int
		var texts []string
		for _, r := range h.Rows {
			if (c.Side == "old") != (r.Kind == "del") {
				continue
			}
			line := r.New
			if c.Side == "old" {
				line = r.Old
			}
			lines, texts = append(lines, line), append(texts, r.Text)
		}
		for i := 0; i+n <= len(lines); i++ {
			if lines[i+n-1]-lines[i] != n-1 { // the run skips lines (removed rows apart)
				continue
			}
			if lines[i] == c.First() {
				return lines[i], lines[i+n-1], true
			}
			if len(code) == n && slices.Equal(texts[i:i+n], code) {
				if d := abs(lines[i] - c.First()); dist < 0 || d < dist {
					best, dist = lines[i], d
				}
			}
		}
	}
	return best, best + n - 1, dist >= 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
