package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func syncBundledSkills(home, root string, register bool) error {
	bundled := filepath.Join(root, "current", "share", "lumiqot", "skills")
	userSkills := filepath.Join(home, ".agents", "skills")
	wanted := make(map[string]bool)
	if register {
		entries, err := os.ReadDir(bundled)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			info, err := os.Lstat(filepath.Join(bundled, entry.Name(), "SKILL.md"))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				wanted[entry.Name()] = true
			}
		}
	}
	if len(wanted) > 0 {
		if err := os.MkdirAll(userSkills, 0755); err != nil {
			return err
		}
	}

	entries, err := os.ReadDir(userSkills)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if wanted[entry.Name()] || entry.Type()&fs.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(userSkills, entry.Name())
		target, err := os.Readlink(link)
		if err != nil {
			return err
		}
		// A managed link points to the same name under the active package.
		if target == filepath.Join(bundled, entry.Name()) {
			if err := os.Remove(link); err != nil {
				return err
			}
		}
	}
	for name := range wanted {
		err := os.Symlink(filepath.Join(bundled, name), filepath.Join(userSkills, name))
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}
