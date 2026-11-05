package ports

import (
	"os"
	"slices"
	"strings"
)

// Fake is a /proc made of maps: Files (path → content) and Links (path → target). A folder
// is every path under it.
type Fake struct {
	Files map[string]string
	Links map[string]string
}

func (f Fake) ReadFile(name string) ([]byte, error) {
	if s, ok := f.Files[name]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

func (f Fake) ReadDir(name string) ([]string, error) {
	seen := map[string]bool{}
	for _, m := range []map[string]string{f.Files, f.Links} {
		for k := range m {
			if rest, ok := strings.CutPrefix(k, name+"/"); ok {
				seen[strings.SplitN(rest, "/", 2)[0]] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil, os.ErrNotExist
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out, nil
}

func (f Fake) Readlink(name string) (string, error) {
	if s, ok := f.Links[name]; ok {
		return s, nil
	}
	return "", os.ErrNotExist
}
