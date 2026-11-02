package naming

import "testing"

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"MON-123 Sign in form": "mon-123-sign-in-form",
		"login timeout":        "login-timeout",
		"  --Hello__World!!  ": "hello__world",
		"a   b---c":            "a-b-c",
		".hidden.":             "hidden",
		"v1.2":                 "v1.2",
		"Ünïcode":              "n-code",
		"!!!":                  "",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanBranch(t *testing.T) {
	for in, want := range map[string]string{
		"Fix/Login Timeout":    "fix/login-timeout",
		"feat//x":              "feat/x",
		"/release/1.4/":        "release/1.4",
		"user/MON-1 Big thing": "user/mon-1-big-thing",
		"///":                  "",
	} {
		if got := CleanBranch(in); got != want {
			t.Errorf("CleanBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoryName(t *testing.T) {
	for in, want := range map[string]string{
		"fix/login-timeout": "login-timeout",
		"login":             "login",
		"a/b/C D":           "c-d",
		"dependabot/npm/x":  "x",
	} {
		if got := StoryName(in); got != want {
			t.Errorf("StoryName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBranchAndDefaultType(t *testing.T) {
	if got := Branch("fix", "login-timeout"); got != "fix/login-timeout" {
		t.Fatal(got)
	}
	if got := Branch("none", "login-timeout"); got != "login-timeout" {
		t.Fatal(got)
	}
	for in, want := range map[string]string{"": "feat", "fix": "fix", "none": "none", "bogus": "feat"} {
		if got := DefaultType(in); got != want {
			t.Errorf("DefaultType(%q) = %q, want %q", in, got, want)
		}
	}
	if len(Types) != 7 || Types[0] != "feat" || Types[6] != "none" {
		t.Fatalf("types = %v", Types)
	}
}
