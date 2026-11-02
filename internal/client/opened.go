package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

// opened is what this client keeps between runs: the projects it has open, in name order, the
// one shown last, and the Claude sync items selected (sorted; nil until the first plan seeds
// it, empty when nothing is selected). It lives in <root>/client.json on the client machine;
// the server never sees it.
type opened struct {
	Projects    []string `json:"projects"`
	Current     string   `json:"current,omitempty"`
	SyncInclude []string `json:"sync_include,omitzero"`
	SyncExclude []string `json:"sync_exclude,omitempty"` // the old unselected list, read only to seed SyncInclude once
}

func openedFile(root string) string { return filepath.Join(root, "client.json") }

// loadOpened reads the saved projects. A missing or broken file is an empty list.
func loadOpened(root string) opened {
	var o opened
	if data, err := os.ReadFile(openedFile(root)); err == nil {
		_ = json.Unmarshal(data, &o)
	}
	return o
}

func (o opened) save(root string) error {
	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(openedFile(root), append(data, '\n'), 0o644)
}

// add puts name in the list, keeping name order. It reports whether the list changed.
func (o *opened) add(name string) bool {
	i, found := slices.BinarySearch(o.Projects, name)
	if found {
		return false
	}
	o.Projects = slices.Insert(o.Projects, i, name)
	return true
}

// remove takes name off the list and returns the project to show instead: the next one in name
// order, else the one before, else "".
func (o *opened) remove(name string) string {
	i, found := slices.BinarySearch(o.Projects, name)
	if !found {
		return ""
	}
	o.Projects = slices.Delete(o.Projects, i, i+1)
	switch {
	case i < len(o.Projects):
		return o.Projects[i]
	case i > 0:
		return o.Projects[i-1]
	}
	return ""
}

// keep drops the projects the server no longer has. It reports whether the list changed.
func (o *opened) keep(exists func(string) bool) bool {
	n := len(o.Projects)
	o.Projects = slices.DeleteFunc(o.Projects, func(p string) bool { return !exists(p) })
	return len(o.Projects) != n
}

// seedSync sets the sync selection the first time: the items bx synced before (synced) minus
// those of the old unselected list, which it then drops. It reports whether it seeded.
func (o *opened) seedSync(synced []string) bool {
	if o.SyncInclude != nil {
		return false
	}
	o.SyncInclude = []string{}
	for _, it := range synced {
		if !slices.Contains(o.SyncExclude, it) {
			o.SyncInclude = append(o.SyncInclude, it)
		}
	}
	slices.Sort(o.SyncInclude)
	o.SyncExclude = nil
	return true
}

// keepSync drops the selected items that no longer exist. It reports whether the list changed.
func (o *opened) keepSync(exists func(string) bool) bool {
	n := len(o.SyncInclude)
	o.SyncInclude = slices.DeleteFunc(o.SyncInclude, func(it string) bool { return !exists(it) })
	return len(o.SyncInclude) != n
}
