package ui

// MCPServer is one MCP server on the settings screen: Type stdio, http or sse; Target its
// command line or URL.
type MCPServer struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

// MCPSet is the servers of one scope. Error says why they are skipped at launch: a hand edit
// that fails the check.
type MCPSet struct {
	Servers []MCPServer `json:"servers"`
	Error   string      `json:"error,omitempty"`
}

// MCPProject is a project that has its own servers.
type MCPProject struct {
	Project string `json:"project"`
	MCPSet
}

// MCP is what the settings screen shows: the global servers and every project with its own,
// by name.
type MCP struct {
	Global   MCPSet       `json:"global"`
	Projects []MCPProject `json:"projects"`
}
