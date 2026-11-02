package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SlashCommand is one thing that can follow "/" in Claude's prompt. Source is "builtin",
// "skill", "command" or "plugin".
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// builtins are Claude Code's own commands that are useful from a phone. Claude doesn't list
// them anywhere bx can read, so this list is kept by hand.
var builtins = []SlashCommand{
	{"agents", "Manage agent configurations", "builtin"},
	{"clear", "Clear conversation history and free up context", "builtin"},
	{"compact", "Clear history but keep a summary in context", "builtin"},
	{"config", "Open the settings", "builtin"},
	{"context", "Show the current context usage", "builtin"},
	{"cost", "Show the cost and duration of this session", "builtin"},
	{"export", "Export the conversation", "builtin"},
	{"help", "Show help and available commands", "builtin"},
	{"init", "Create a CLAUDE.md with codebase documentation", "builtin"},
	{"mcp", "Manage MCP servers", "builtin"},
	{"memory", "Edit Claude memory files", "builtin"},
	{"model", "Set the AI model", "builtin"},
	{"permissions", "Manage tool permission rules", "builtin"},
	{"resume", "Resume a conversation", "builtin"},
	{"review", "Review a pull request", "builtin"},
	{"rewind", "Restore the code and/or conversation to an earlier point", "builtin"},
	{"status", "Show version, model, account and connectivity", "builtin"},
	{"todos", "List the current todo items", "builtin"},
	{"usage", "Show plan usage limits", "builtin"},
}

// maxDescription keeps one long description from bloating the reply.
const maxDescription = 200

// SlashCommands lists what can follow "/" in a session running in workDir: the built-ins, the
// skills and commands of configDir (~/.claude) and of workDir/.claude, and the skills of the
// plugin in pluginDir, as "<plugin>:<skill>". A project entry replaces a user entry of the same
// name, as in Claude. Any of the folders may be "" or missing; unreadable files are skipped.
// The list is sorted by name.
func SlashCommands(configDir, workDir, pluginDir string) []SlashCommand {
	byName := map[string]SlashCommand{}
	add := func(list []SlashCommand) {
		for _, c := range list {
			byName[c.Name] = c
		}
	}
	add(builtins)
	if configDir != "" {
		add(readCommands(filepath.Join(configDir, "commands")))
		add(readSkills(filepath.Join(configDir, "skills"), "", "skill"))
	}
	if workDir != "" {
		add(readCommands(filepath.Join(workDir, ".claude", "commands")))
		add(readSkills(filepath.Join(workDir, ".claude", "skills"), "", "skill"))
	}
	if pluginDir != "" {
		if name := pluginName(pluginDir); name != "" {
			add(readSkills(filepath.Join(pluginDir, "skills"), name+":", "plugin"))
		}
	}
	out := make([]SlashCommand, 0, len(byName))
	for _, c := range byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// readSkills reads dir/<skill>/SKILL.md; the name is the frontmatter's, else the folder's.
func readSkills(dir, prefix, source string) []SlashCommand {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SlashCommand
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// a skill folder may be a symlink (claudesync links them), so stat through it
		p := filepath.Join(dir, e.Name(), "SKILL.md")
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		fm := frontmatter(p)
		name := fm["name"]
		if name == "" {
			name = e.Name()
		}
		out = append(out, SlashCommand{Name: prefix + name, Description: clip(fm["description"]), Source: source})
	}
	return out
}

// readCommands reads dir/<name>.md; the name is the file's.
func readCommands(dir string) []SlashCommand {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SlashCommand
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") || !strings.EqualFold(filepath.Ext(n), ".md") {
			continue
		}
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, SlashCommand{Name: strings.TrimSuffix(n, filepath.Ext(n)),
			Description: clip(frontmatter(p)["description"]), Source: "command"})
	}
	return out
}

// pluginName is the name in pluginDir/.claude-plugin/plugin.json, "" if it can't be read.
func pluginName(pluginDir string) string {
	data, err := os.ReadFile(filepath.Join(pluginDir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	return m.Name
}

// frontmatter reads the one-line "key: value" pairs between the leading "---" lines of a
// Markdown file. It is not a YAML parser: nested and multi-line values are skipped, which is
// enough for name and description.
func frontmatter(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return out
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "|" || v == ">" || v == "|-" || v == ">-" {
			continue
		}
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

// clip shortens s to maxDescription runes.
func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxDescription {
		return s
	}
	return string(r[:maxDescription-1]) + "…"
}
