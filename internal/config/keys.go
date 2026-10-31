package config

import (
	"fmt"
	"strings"
)

// bind decodes either "j" or ["j", "Down"]. An empty string or empty array means unbound.
type bind []string

func (b *bind) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		if t == "" {
			*b = nil
		} else {
			*b = bind{t}
		}
	case []any:
		out := make(bind, 0, len(t))
		for _, x := range t {
			s, ok := x.(string)
			if !ok {
				return fmt.Errorf("key bind must be a string, got %T", x)
			}
			if s != "" {
				out = append(out, s)
			}
		}
		*b = out
	default:
		return fmt.Errorf("key bind must be a string or array, got %T", v)
	}
	return nil
}

// ListAction returns the list action bound to key, or "" when none.
// key is a bx key name ("j", "Down", "Enter", "Z", "z M").
func (k Keys) ListAction(key string) string { return actionFor(k.List, key) }

// IsPrefixOfSequence reports whether some bound sequence starts with keys ("z" for "z M").
func (k Keys) IsPrefixOfSequence(keys string) bool {
	for _, binds := range k.List {
		for _, b := range binds {
			if strings.HasPrefix(b, keys+" ") {
				return true
			}
		}
	}
	return false
}

// First returns the first bound key of a list action, for hints in the key bar.
func (k Keys) First(section map[string][]string, action string) string {
	if b := section[action]; len(b) > 0 {
		return b[0]
	}
	return ""
}

func actionFor(section map[string][]string, key string) string {
	for action, binds := range section {
		for _, b := range binds {
			if b == key {
				return action
			}
		}
	}
	return ""
}

// Pretty renders a key name the way the prototype does: Enter→⏎, Space→space, Down→↓, C-h→^h.
func Pretty(key string) string {
	parts := strings.Fields(key)
	for i, p := range parts {
		switch p {
		case "Enter":
			parts[i] = "⏎"
		case "Esc":
			parts[i] = "esc"
		case "Space":
			parts[i] = "space"
		case "Down":
			parts[i] = "↓"
		case "Up":
			parts[i] = "↑"
		case "Left":
			parts[i] = "←"
		case "Right":
			parts[i] = "→"
		default:
			parts[i] = strings.Replace(p, "C-", "^", 1)
		}
	}
	return strings.Join(parts, "")
}
