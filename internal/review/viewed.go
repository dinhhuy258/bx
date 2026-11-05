package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
)

// Mark says a file was viewed: the hash of its diff then, and when.
type Mark struct {
	Hash string `json:"hash"`
	At   int64  `json:"at"`
}

// Hash names a file's diff: its hunks, or for a folded file its note and counts. A gone file
// (only comments, no diff) has none.
func Hash(f api.DiffFile) string {
	if f.Status == "gone" {
		return ""
	}
	h := sha256.New()
	if f.Folded != "" {
		fmt.Fprintf(h, "folded\x00%s\x00%d\x00%d\x00", f.Folded, f.Add, f.Del)
	}
	for _, hk := range f.Hunks {
		fmt.Fprintf(h, "@\x00%s\x00", hk.Header)
		for _, r := range hk.Rows {
			fmt.Fprintf(h, "%s\x00%s\x00", r.Kind, r.Text)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// MarkViewed hashes each file and sets its viewed state from marks.
func MarkViewed(files []api.DiffFile, marks map[string]Mark) {
	for i := range files {
		f := &files[i]
		f.Hash, f.Viewed = Hash(*f), ""
		if m, ok := marks[f.Path]; ok && f.Hash != "" {
			f.Viewed = api.FileViewed
			if m.Hash != f.Hash {
				f.Viewed = api.FileChanged
			}
		}
	}
}

// SetViewed applies a mark request to marks, dropping the marks of files no longer in files.
// A file is marked at the hash the client was shown, so one that changed since shows as
// changed rather than viewed.
func SetViewed(marks map[string]Mark, files []api.DiffFile, req api.ReviewViewed, now time.Time) (map[string]Mark, error) {
	var hash string
	known := map[string]bool{}
	for _, f := range files {
		if h := Hash(f); h != "" {
			known[f.Path] = true
			if f.Path == req.Path {
				hash = h
			}
		}
	}
	if req.Viewed && hash == "" {
		return nil, fmt.Errorf("%s isn't a changed file", req.Path)
	}
	out := map[string]Mark{}
	for p, m := range marks {
		if known[p] && p != req.Path {
			out[p] = m
		}
	}
	if req.Viewed {
		if req.Hash != "" {
			hash = req.Hash
		}
		out[req.Path] = Mark{Hash: hash, At: now.Unix()}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
