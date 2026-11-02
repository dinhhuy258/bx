// Package naming holds the rules that turn what the user types into story names and branches,
// shared by bx serve (which decides) and the TUI (which previews).
package naming

import "strings"

// Types are the branch types offered when a story is created, in picker order. "none" makes a
// branch without a type prefix.
var Types = []string{"feat", "fix", "chore", "hotfix", "refactor", "docs", "none"}

// Slug lowercases s, turns every rune outside [a-z0-9._-] into "-", collapses runs of "-" and
// trims "-" and "." at both ends: "MON-123 Sign in form" is "mon-123-sign-in-form".
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !ok || r == '-' {
			if !dash {
				b.WriteByte('-')
			}
			dash = true
			continue
		}
		dash = false
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "-.")
}

// CleanBranch slugs every "/"-separated part of a typed branch and drops the empty ones:
// "Fix/Login Timeout" is "fix/login-timeout".
func CleanBranch(s string) string {
	var parts []string
	for _, p := range strings.Split(s, "/") {
		if p = Slug(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// StoryName is the story a branch makes: the slug of its part after the last "/", so
// "fix/login-timeout" is story "login-timeout".
func StoryName(branch string) string {
	return Slug(branch[strings.LastIndex(branch, "/")+1:])
}

// Branch is the branch of a story of type typ: "<typ>/<slug>", or the bare slug for "none".
func Branch(typ, slug string) string {
	if typ == "none" {
		return slug
	}
	return typ + "/" + slug
}

// ValidType reports whether typ is one of Types.
func ValidType(typ string) bool {
	for _, t := range Types {
		if t == typ {
			return true
		}
	}
	return false
}

// DefaultType is the type to highlight: last when it is one of Types, else "feat".
func DefaultType(last string) string {
	if ValidType(last) {
		return last
	}
	return "feat"
}
