package review

import "github.com/dinhhuy258/bx/internal/api"

// Place puts the comments on the current diff. A comment keeps its line when that line is in
// the diff on its side; else it moves to the nearest line with the same code; else it is
// Outdated. A comment on a file that is no longer changed is Outdated and the file is added
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
			if line, ok := place(files[fi], c); ok {
				c.Line = line
			} else {
				c.Outdated = true
			}
		}
		out[i] = c
	}
	return files, out
}

// place finds the comment's line in f: the same line on its side, or the nearest one with
// the same code. The old side is the removed rows; the new side is every other row.
func place(f api.DiffFile, c api.Comment) (int, bool) {
	best, dist := 0, -1
	for _, h := range f.Hunks {
		for _, r := range h.Rows {
			line := r.New
			if c.Side == "old" {
				if r.Kind != "del" {
					continue
				}
				line = r.Old
			} else if r.Kind == "del" {
				continue
			}
			if line == c.Line {
				return line, true
			}
			if r.Text == c.Code {
				if d := abs(line - c.Line); dist < 0 || d < dist {
					best, dist = line, d
				}
			}
		}
	}
	return best, dist >= 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
