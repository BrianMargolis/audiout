package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ExpandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func RequireBinary(name string) error {
	_, err := exec.LookPath(name)
	return err
}
