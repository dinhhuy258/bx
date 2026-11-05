package runcfg

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Format writes f in bx's fixed layout: hide and show, then one [[run]] per run with its keys
// in the form's order and [run.env] last. A build is a ”' block. Comments are not kept
// (HasComments warns before a form save drops them).
func Format(f File) []byte {
	var b strings.Builder
	if len(f.Hide) > 0 {
		fmt.Fprintf(&b, "hide = %s\n", ints(f.Hide))
	}
	if len(f.Show) > 0 {
		fmt.Fprintf(&b, "show = %s\n", ints(f.Show))
	}
	for _, r := range f.Runs {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[[run]]\n")
		str := func(k, v string) {
			if v != "" {
				fmt.Fprintf(&b, "%s = %s\n", k, quote(v))
			}
		}
		str("name", r.Name)
		str("cmd", r.Cmd)
		str("dir", r.Dir)
		str("image", r.Image)
		if r.Build != "" {
			if strings.Contains(r.Build, "'''") {
				str("build", r.Build)
			} else {
				fmt.Fprintf(&b, "build = '''\n%s'''\n", ensureNL(r.Build))
			}
		}
		switch len(r.Port) {
		case 0:
		case 1:
			fmt.Fprintf(&b, "port = %d\n", r.Port[0])
		default:
			fmt.Fprintf(&b, "port = %s\n", ints(r.Port))
		}
		str("open", r.Open)
		switch {
		case r.Setup.Image != "":
			fmt.Fprintf(&b, "setup = { cmd = %s, image = %s }\n", quote(r.Setup.Cmd), quote(r.Setup.Image))
		default:
			str("setup", r.Setup.Cmd)
		}
		str("creates", r.Creates)
		str("stop", r.Stop)
		if len(r.Cache) > 0 {
			q := make([]string, len(r.Cache))
			for i, c := range r.Cache {
				q[i] = quote(c)
			}
			fmt.Fprintf(&b, "cache = [%s]\n", strings.Join(q, ", "))
		}
		if len(r.Env) > 0 {
			b.WriteString("[run.env]\n")
			for _, k := range slices.Sorted(maps.Keys(r.Env)) {
				fmt.Fprintf(&b, "%s = %s\n", k, quote(r.Env[k]))
			}
		}
	}
	return []byte(b.String())
}

func ints(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

func ensureNL(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// quote is a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// HasComments reports whether data holds a # comment outside strings, which Format would drop.
func HasComments(data []byte) bool {
	s := string(data)
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], `"""`):
			i = skipTo(s, i+3, `"""`, true)
		case strings.HasPrefix(s[i:], `'''`):
			i = skipTo(s, i+3, `'''`, false)
		case s[i] == '"':
			i = skipTo(s, i+1, `"`, true)
		case s[i] == '\'':
			i = skipTo(s, i+1, `'`, false)
		case s[i] == '#':
			return true
		}
	}
	return false
}

// skipTo is the index of the last byte of end at or after i, skipping \x escapes when esc.
func skipTo(s string, i int, end string, esc bool) int {
	for ; i < len(s); i++ {
		if esc && s[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(s[i:], end) {
			return i + len(end) - 1
		}
	}
	return len(s)
}
