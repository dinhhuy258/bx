package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

func putDoc(t *testing.T, ts *httptest.Server, path, text string) (api.Result, int) {
	t.Helper()
	b, _ := json.Marshal(api.MCPDoc{Text: text})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var res api.Result
	_ = json.NewDecoder(r.Body).Decode(&res)
	return res, r.StatusCode
}

func TestMCPRoutesAreTUIOnly(t *testing.T) {
	_, _, ts := newServer(t)
	for _, path := range []string{"/api/mcp", "/api/mcp/doc"} {
		if _, code := get[api.MCP](t, ts, path); code != http.StatusForbidden {
			t.Errorf("GET %s from the web = %d", path, code)
		}
	}
	if _, code := putDoc(t, ts, "/api/mcp/doc", "{}"); code != http.StatusForbidden {
		t.Errorf("PUT from the web = %d", code)
	}
}

func TestMCPDocSaveAndList(t *testing.T) {
	_, _, ts := newServer(t)
	if res, code := putDoc(t, ts, "/api/mcp/doc?tui=1", `{"mcpServers": {"github": {"command": "gh"}}}`); code != 200 {
		t.Fatalf("global: %d %+v", code, res)
	}
	if res, code := putDoc(t, ts, "/api/mcp/doc?tui=1&project=home-network", `{"mcpServers": {"pw": {"command": "npx", "args": ["pw"], "env": {"T": "secret-token"}}}}`); code != 200 {
		t.Fatalf("project: %d %+v", code, res)
	}
	res, code := putDoc(t, ts, "/api/mcp/doc?tui=1&project=home-network", "{\n\"mcpServers\": {\n}x")
	if code != 400 || res.Line != 3 || !strings.HasPrefix(res.Error, "line 3, column") {
		t.Fatalf("bad doc: %d %+v", code, res)
	}
	m, code := get[api.MCP](t, ts, "/api/mcp?tui=1")
	if code != 200 || len(m.Global.Servers) != 1 || len(m.Projects) != 1 || m.Projects[0].Project != "home-network" ||
		m.Projects[0].Servers[0].Target != "npx pw" {
		t.Fatalf("list: %d %+v", code, m)
	}
	doc, _ := get[api.MCPDoc](t, ts, "/api/mcp/doc?tui=1&project=home-network")
	if !strings.Contains(doc.Text, `"secret-token"`) || !strings.HasPrefix(doc.Text, "{\n  \"mcpServers\"") {
		t.Fatalf("doc = %q", doc.Text)
	}
	if empty, _ := get[api.MCPDoc](t, ts, "/api/mcp/doc?tui=1&project=nope"); empty.Text != "" {
		t.Fatalf("unknown project: %q", empty.Text)
	}
	r, err := http.Get(ts.URL + "/api/projects/home-network")
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	raw.ReadFrom(r.Body)
	r.Body.Close()
	if strings.Contains(raw.String(), "secret-token") || strings.Contains(raw.String(), "mcp_servers") {
		t.Fatal("the snapshot reaches the web client: it must not carry MCP servers")
	}
	if _, code := putDoc(t, ts, "/api/mcp/doc?tui=1&project=home-network", ""); code != 200 {
		t.Fatal("empty text removes")
	}
	if m, _ := get[api.MCP](t, ts, "/api/mcp?tui=1"); len(m.Projects) != 0 {
		t.Fatalf("after remove: %+v", m.Projects)
	}
}
