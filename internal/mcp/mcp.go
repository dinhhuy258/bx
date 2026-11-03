// Package mcp reads, checks, merges and writes the MCP servers bx gives Claude sessions: the
// global ~/.bx/mcp.json and each project's mcp_servers. Both use Claude Code's .mcp.json shape,
// {"mcpServers": {"<name>": {...}}}. A server is kept as written, so fields bx does not check
// (timeouts, oauth, ...) reach Claude unchanged.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Servers maps a server name to its config, as written.
type Servers map[string]json.RawMessage

// Server is one server as the settings screen lists it. Type is stdio, http or sse; Target is
// the command line or the URL.
type Server struct {
	Name, Type, Target string
}

// Error is a document that does not parse or check. Line and Col are 1-based; 0 when the
// place is unknown.
type Error struct {
	Line, Col int
	Msg       string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Col, e.Msg)
	}
	return e.Msg
}

// known are the fields bx checks.
type known struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// Parse reads a document. Blank text, {} and an empty mcpServers are no servers: nil.
func Parse(data []byte) (Servers, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, located(data, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		line, col := lineCol(data, int(dec.InputOffset()))
		return nil, &Error{Line: line, Col: col, Msg: "unexpected text after the closing }"}
	}
	s, err := Check(doc.MCPServers)
	var e *Error
	if errors.As(err, &e) && e.Line == 0 {
		if name, _, ok := strings.Cut(e.Msg, ": "); ok {
			if i := bytes.Index(data, []byte(strconv.Quote(name))); i >= 0 {
				e.Line, e.Col = lineCol(data, i)
			}
		}
	}
	return s, err
}

// Decode checks a project's stored mcp_servers, the inner object. nil is no servers.
func Decode(raw json.RawMessage) (Servers, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, &Error{Msg: "mcp_servers: want an object of servers"}
	}
	return Check(m)
}

// Check checks every server, by name. An error starts with the server's name.
func Check(m map[string]json.RawMessage) (Servers, error) {
	if len(m) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			return nil, &Error{Msg: "a server needs a name"}
		}
		var k known
		if err := json.Unmarshal(m[n], &k); err != nil {
			var te *json.UnmarshalTypeError
			if errors.As(err, &te) && te.Field != "" {
				return nil, &Error{Msg: fmt.Sprintf("%s: %s must be %s", n, te.Field, kind(te.Type))}
			}
			return nil, &Error{Msg: n + ": want an object"}
		}
		switch k.Type {
		case "", "stdio":
			if k.Command == "" {
				return nil, &Error{Msg: n + `: needs a command, or a url with "type": "http"`}
			}
		case "http", "sse":
			if k.URL == "" {
				return nil, &Error{Msg: n + ": needs a url"}
			}
		default:
			return nil, &Error{Msg: fmt.Sprintf("%s: type %q: want stdio, http or sse", n, k.Type)}
		}
	}
	return Servers(m), nil
}

func kind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Slice:
		return "a list of strings"
	case reflect.Map:
		return "an object of strings"
	}
	return "a string"
}

// located turns a decode error into an *Error with the line and column of its offset.
func located(data []byte, err error) error {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return &Error{Msg: "the text ends early: a } or ] is missing"}
	case errors.As(err, &se):
		line, col := lineCol(data, int(se.Offset)-1)
		return &Error{Line: line, Col: col, Msg: se.Error()}
	case errors.As(err, &te):
		if te.Field == "" {
			return &Error{Msg: `want an object: {"mcpServers": {…}}`}
		}
		return &Error{Msg: `mcpServers must be an object of servers`}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return &Error{Msg: fmt.Sprintf("unknown key %s: want \"mcpServers\"", strings.TrimPrefix(err.Error(), "json: unknown field "))}
	}
	return &Error{Msg: err.Error()}
}

// lineCol is the 1-based line and column of byte off in data.
func lineCol(data []byte, off int) (int, int) {
	off = min(max(off, 0), len(data))
	line := 1 + bytes.Count(data[:off], []byte("\n"))
	return line, off - bytes.LastIndexByte(data[:off], '\n')
}

// Summaries lists the servers by name.
func Summaries(s Servers) []Server {
	out := make([]Server, 0, len(s))
	for n, raw := range s {
		var k known
		_ = json.Unmarshal(raw, &k)
		sv := Server{Name: n, Type: k.Type, Target: k.URL}
		if sv.Type == "" {
			sv.Type = "stdio"
		}
		if sv.Type == "stdio" {
			sv.Target = strings.Join(append([]string{k.Command}, k.Args...), " ")
		}
		out = append(out, sv)
	}
	slices.SortFunc(out, func(a, b Server) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Merge is global with project on top. Replaced are the names both have, sorted.
func Merge(global, project Servers) (merged Servers, replaced []string) {
	merged = Servers{}
	for n, v := range global {
		merged[n] = v
	}
	for n, v := range project {
		if _, ok := merged[n]; ok {
			replaced = append(replaced, n)
		}
		merged[n] = v
	}
	slices.Sort(replaced)
	return merged, replaced
}

// Encode writes s as a document, indented by two spaces. nil is {"mcpServers": {}}.
func Encode(s Servers) []byte {
	if s == nil {
		s = Servers{}
	}
	b, _ := json.MarshalIndent(struct {
		MCPServers Servers `json:"mcpServers"`
	}{s}, "", "  ")
	return append(b, '\n')
}

// ReadFile parses the document at path. A missing file is no servers.
func ReadFile(path string) (Servers, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// WriteFile writes data to path atomically, readable only by its owner: a server's env may
// hold a token.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
