package workspace

import (
	"errors"
	"os"
	"path/filepath"
)

// DetectRoot walks upward from start to find go.work or go.mod.
// If none found, it returns the absolute start path.
func DetectRoot(start string) (string, error) {
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	path := abs
	for {
		if exists(filepath.Join(path, "go.work")) || exists(filepath.Join(path, "go.mod")) {
			return path, nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return abs, nil
		}
		path = parent
	}
}

func exists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// ValidateRoot ensures root exists and is a directory.
func ValidateRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("workspace root is not a directory")
	}
	return nil
}
