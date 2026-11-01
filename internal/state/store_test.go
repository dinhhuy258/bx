package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingReturnsEmptyProject(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "x.json"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != CurrentVersion || p.Name != "x" || p.NextNum != 1 || p.Sessions == nil || p.Stories == nil {
		t.Fatalf("unexpected empty project: %+v", p)
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "home-network.json")
	p := New("home-network", "/root/projects/home-network")
	p.Stories["guest-vlan-fix"] = &Story{Branch: "feat/guest-vlan-fix"}
	num := p.AddSession(&Session{Story: "guest-vlan-fix", UUID: "u1", Title: "Fix lease", Created: time.Date(2026, 10, 30, 14, 2, 0, 0, time.UTC),
		Model: "opus", Effort: "high", PermissionMode: "plan"})
	if num != 1 || p.NextNum != 2 {
		t.Fatalf("num=%d next=%d", num, p.NextNum)
	}
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, "home-network")
	if err != nil {
		t.Fatal(err)
	}
	s := got.Sessions[1]
	if s == nil || s.Title != "Fix lease" || s.Story != "guest-vlan-fix" || s.Model != "opus" || s.Effort != "high" || s.PermissionMode != "plan" {
		t.Fatalf("session = %+v", s)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestCorruptFileIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bunnix.json")
	os.WriteFile(path, []byte("{not json"), 0o644)
	_, err := Load(path, "bunnix")
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *CorruptError
	if !asCorrupt(err, &ce) {
		t.Fatalf("error type %T: %v", err, err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("corrupt file should have been moved")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "bunnix.corrupt-") {
		t.Errorf("quarantine file missing: %v", entries)
	}
}

func TestUnknownNewerVersionRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(path, []byte(`{"version": 99}`), 0o644)
	if _, err := Load(path, "p"); err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("err = %v", err)
	}
}

func TestChatsSessionHasNullStory(t *testing.T) {
	p := New("p", "/p")
	p.AddSession(&Session{UUID: "u", Title: "What does pmxcfs do?"})
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"story":null`) {
		t.Errorf("chat session must serialise story as null: %s", b)
	}
}

func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	l1, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Unlock()
	if _, err := Lock(path); err == nil {
		t.Fatal("second lock should fail")
	}
}

func asCorrupt(err error, target **CorruptError) bool {
	for err != nil {
		if ce, ok := err.(*CorruptError); ok {
			*target = ce
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
