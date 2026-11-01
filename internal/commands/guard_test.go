package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardHook(t *testing.T) {
	orig, story := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(orig, "web", ".git"), 0o755)
	os.Symlink(filepath.Join(orig, "web"), filepath.Join(story, "web"))
	for in, want := range map[string]int{
		`{"cwd":"` + story + `","tool_input":{"file_path":"web/main.go"}}`:        2,
		`{"tool_input":{"notebook_path":"` + story + `/web/x.ipynb"}}`:            2,
		`{"cwd":"` + story + `","tool_input":{"file_path":"` + story + `/a.go"}}`: 0,
		`{"tool_input":{}}`: 0,
		`not json`:          0,
	} {
		var errOut bytes.Buffer
		if got := guard(story, strings.NewReader(in), &errOut); got != want {
			t.Errorf("%s: exit %d, want %d (%s)", in, got, want, errOut.String())
		}
		if want == 2 && !strings.Contains(errOut.String(), "press + on the story") {
			t.Errorf("%s: %q", in, errOut.String())
		}
	}
}
