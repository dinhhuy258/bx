package client

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

const labDoc = "{\n  \"mcpServers\": {\n    \"pw\": {\"command\": \"npx\"}\n  }\n}\n"

type editCall struct {
	path, text string
	line       int
}

// mcpHarness opens settings › MCP with lab having one server. The editor replaces the file's
// text with what typed returns for it (nil leaves it as it is) and records every call.
func mcpHarness(t *testing.T, typed func(string) string) (*harness, *[]editCall) {
	t.Helper()
	h := start(t, 120, 30, "running", "running")
	h.be.mcp = api.MCP{Projects: []ui.MCPProject{{Project: "lab", MCPSet: ui.MCPSet{Servers: []ui.MCPServer{{Name: "pw", Type: "stdio", Target: "npx"}}}}}}
	h.be.mcpDocs = map[string]string{"lab": labDoc, "home-network": "{\n  \"mcpServers\": {}\n}\n"}
	calls := &[]editCall{}
	h.m.edit = func(path string, line int, done func(error) tea.Msg) tea.Cmd {
		b, _ := os.ReadFile(path)
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("temp file mode %v", fi.Mode())
		}
		*calls = append(*calls, editCall{path, string(b), line})
		if typed != nil {
			os.WriteFile(path, []byte(typed(string(b))), 0o600)
		}
		return func() tea.Msg { return done(nil) }
	}
	h.press("M-p", "j", "j", "j", "Enter") // open MCP
	return h, calls
}

func TestSettingsMCPEditsAProject(t *testing.T) {
	h, calls := mcpHarness(t, func(s string) string { return strings.Replace(s, "npx", "pnpx", 1) })
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "project servers · 1 of 2 projects have their own") || !strings.Contains(v, "lab") {
		t.Fatalf("MCP section:\n%s", v)
	}
	h.press("j", "j") // Global, lab
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "⏎ edit in $EDITOR") {
		t.Fatalf("key bar:\n%s", v)
	}
	h.press("Enter")
	if len(*calls) != 1 || (*calls)[0].text != labDoc || (*calls)[0].line != 0 {
		t.Fatalf("editor calls = %+v", *calls)
	}
	if got := strings.Join(h.be.mcpSaves, "|"); got != "lab="+strings.Replace(labDoc, "npx", "pnpx", 1) {
		t.Fatalf("saves = %q", got)
	}
	if _, err := os.Stat((*calls)[0].path); !os.IsNotExist(err) {
		t.Fatal("the temp file must be removed")
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "lab: MCP servers saved · new sessions use them") {
		t.Fatalf("flash:\n%s", v)
	}
}

func TestSettingsMCPUnchangedSendsNothing(t *testing.T) {
	h, _ := mcpHarness(t, nil)
	h.press("j", "j", "Enter")
	if len(h.be.mcpSaves) != 0 || !strings.Contains(ansi.Strip(h.m.View().Content), "lab: no changes") {
		t.Fatalf("saves = %q", h.be.mcpSaves)
	}
}

func TestSettingsMCPKeepsARefusedDraft(t *testing.T) {
	h, calls := mcpHarness(t, func(s string) string { return s + "oops" })
	h.be.mcpErr = &api.Result{Error: "line 6, column 1: unexpected text after the closing }", Line: 6}
	h.press("j", "j", "Enter")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "✗ not saved  line 6, column 1") || !strings.Contains(v, "⏎ reopens your draft") {
		t.Fatalf("refused save:\n%s", v)
	}
	h.press("Enter") // reopens the draft at line 6, without a GET
	if len(*calls) != 2 || (*calls)[1].line != 6 || (*calls)[1].text != labDoc+"oops" {
		t.Fatalf("reopen = %+v", (*calls)[1:])
	}
	h.press("BSpace") // discards the draft, asks nothing
	if v := ansi.Strip(h.m.View().Content); strings.Contains(v, "✗ not saved") || !strings.Contains(v, "lab: draft discarded") {
		t.Fatalf("after discard:\n%s", v)
	}
}

func TestSettingsMCPAddsAndRemoves(t *testing.T) {
	h, calls := mcpHarness(t, func(string) string { return `{"mcpServers": {"a": {"command": "b"}}}` })
	h.press("j", "j", "j", "Enter") // + add project servers
	v := ansi.Strip(h.m.View().Content)
	if h.m.pickKind != pickMCPProject || !strings.Contains(v, "MCP servers for…") || !strings.Contains(v, "home-network") {
		t.Fatalf("picker:\n%s", v)
	}
	if strings.Contains(ansi.Strip(h.m.picker.Render(120, 10)), "lab") {
		t.Fatal("the picker leaves out projects that have servers")
	}
	h.press("Enter")
	if len(*calls) != 1 || !strings.Contains(h.be.mcpSaves[0], "home-network=") {
		t.Fatalf("add: calls %+v saves %q", *calls, h.be.mcpSaves)
	}
	h.press("k") // lab
	h.press("BSpace")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "Remove MCP servers?") || !strings.Contains(v, "1 server of lab") {
		t.Fatalf("remove asks:\n%s", v)
	}
	h.press("y")
	if got := h.be.mcpSaves[len(h.be.mcpSaves)-1]; got != "lab=" {
		t.Fatalf("remove saves empty text, got %q", got)
	}
}

func TestSettingsCloseDropsMCPDrafts(t *testing.T) {
	h, _ := mcpHarness(t, func(s string) string { return s + "x" })
	h.be.mcpErr = &api.Result{Error: "bad"}
	h.press("j", "j", "Enter", "Esc", "Esc") // back to the list, then close
	if h.m.settings != nil || len(h.m.mcpDrafts) != 0 {
		t.Fatalf("close keeps drafts: %v", h.m.mcpDrafts)
	}
}
