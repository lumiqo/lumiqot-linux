package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStatusSSHWindowsVerification(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ssh", ".lumiqot-windows-verified")
	for _, tc := range []struct {
		name, record, want string
	}{
		{"missing", "", ""},
		{"saved success", "2026-11-26T12:34:56Z\n", "2026-11-26T12:34:56Z"},
		{"malformed", "invalid\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.record != "" {
				if err := os.WriteFile(path, []byte(tc.record), 0600); err != nil {
					t.Fatal(err)
				}
			}
			value, err := statusSSHClient(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := value.(map[string]any)["windowsVerifiedAt"].(string)
			if got != tc.want {
				t.Fatalf("windowsVerifiedAt = %q, want %q", got, tc.want)
			}
		})
	}
}
