package review

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/state"
)

// MaxExplains is how many explanations a story keeps; the oldest finished ones go first.
const MaxExplains = 20

// ExplainFile is a story's explanations and the conversation its last ask used (see
// api.ReviewData.ExplainWith). It is apart from the comment file, so an answer that lands
// never changes the comments' version.
type ExplainFile struct {
	With     int           `json:"with,omitempty"`
	Explains []api.Explain `json:"explains"`
}

// LoadExplains reads an explain file; a missing one is empty, a corrupt one is moved aside
// and a *state.CorruptError returned.
func LoadExplains(path string) (ExplainFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ExplainFile{}, nil
	}
	if err != nil {
		return ExplainFile{}, err
	}
	var f ExplainFile
	if err := json.Unmarshal(data, &f); err != nil {
		return ExplainFile{}, state.Quarantine(path, err)
	}
	return f, nil
}

// SaveExplains writes f atomically.
func SaveExplains(path string, f ExplainFile) error { return writeJSON(path, f) }

// TrimExplains drops the oldest finished explanations past MaxExplains.
func TrimExplains(es []api.Explain) []api.Explain {
	for over := len(es) - MaxExplains; over > 0; over-- {
		i := slices.IndexFunc(es, func(e api.Explain) bool { return !e.Running() })
		if i < 0 {
			break
		}
		es = slices.Delete(slices.Clone(es), i, i+1)
	}
	return es
}

// PlaceAll is Place for comments and explanations together, so a file that is no longer
// changed is added once for both.
func PlaceAll(files []api.DiffFile, cs []api.Comment, es []api.Explain) ([]api.DiffFile, []api.Comment, []api.Explain) {
	all := slices.Clone(cs)
	for _, e := range es {
		all = append(all, e.AsComment())
	}
	files, placed := Place(files, all)
	outE := append([]api.Explain{}, es...) // never nil, so JSON has []
	for i := range outE {
		p := placed[len(cs)+i]
		outE[i].StartLine, outE[i].Line, outE[i].Outdated = p.StartLine, p.Line, p.Outdated
	}
	return files, placed[:len(cs)], outE
}
