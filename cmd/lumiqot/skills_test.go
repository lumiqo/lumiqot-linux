package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestBundledSkillsActivationAndRollback(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	const name = "lumiqot-gateway-publish"
	userSkills := filepath.Join(home, ".agents", "skills")
	userEntry := filepath.Join(userSkills, "personal")
	managed := filepath.Join(userSkills, name)
	current := filepath.Join(root, "current")
	old := filepath.Join(root, "versions", "old")
	candidate := filepath.Join(root, "versions", "candidate")
	bundled := filepath.Join(candidate, "share", "lumiqot", "skills")

	for _, dir := range []string{userEntry, old, filepath.Join(bundled, name), filepath.Join(bundled, "personal")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, skill := range []string{name, "personal"} {
		if err := os.WriteFile(filepath.Join(bundled, skill, "SKILL.md"), []byte(skill), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(candidate, current); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := syncBundledSkills(home, root, true); err != nil {
			t.Fatal(err)
		}
	}
	target, err := os.Readlink(managed)
	if want := filepath.Join(current, "share", "lumiqot", "skills", name); err != nil || target != want {
		t.Fatalf("managed link = %q, %v, want %q", target, err, want)
	}
	if got, err := os.ReadFile(filepath.Join(managed, "SKILL.md")); err != nil || string(got) != name {
		t.Fatalf("skill content = %q, %v", got, err)
	}

	// The outgoing package removes its links before an older CLI takes over.
	if err := syncBundledSkills(home, root, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(managed); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("outgoing skill link still exists: %v", err)
	}
	if info, err := os.Lstat(userEntry); err != nil || !info.IsDir() {
		t.Fatalf("custom skill directory was modified: %v, %v", info, err)
	}
}
