// Package claudeplugin is the Claude Code plugin bx gives every session it starts
// (--plugin-dir): the bx:artifact skill. It ships inside the bx binary and bx serve writes it
// out at start, so upgrading bx upgrades the plugin and nothing is installed into ~/.claude.
package claudeplugin

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:plugin
var files embed.FS

// Write puts the plugin in dir. A file whose content already matches is left alone; any other
// is replaced atomically.
func Write(dir string) error {
	return fs.WalkDir(files, "plugin", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(p, "plugin"), "/")))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		want, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if got, err := os.ReadFile(target); err == nil && bytes.Equal(got, want) {
			return nil
		}
		return writeAtomic(target, want)
	})
}

// writeAtomic writes a temp file next to p and renames it over p.
func writeAtomic(p string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), "."+path.Base(filepath.ToSlash(p))+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644)
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
