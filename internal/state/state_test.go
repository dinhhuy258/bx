package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectKeepsMCPServers(t *testing.T) {
	p := New("bx", "/p")
	p.MCPServers = json.RawMessage(`{"a":{"command":"x","args":"wrong shape is still kept"}}`)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Project
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("a wrong-shaped mcp_servers must not fail the project: %v", err)
	}
	if string(back.MCPServers) != string(p.MCPServers) {
		t.Fatalf("mcp_servers = %s", back.MCPServers)
	}
	if b, _ := json.Marshal(New("bx", "/p")); strings.Contains(string(b), "mcp_servers") {
		t.Fatalf("an empty field is left out: %s", b)
	}
}
