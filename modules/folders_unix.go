//go:build !windows

package modules

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func openFolderPlatform(path string) error {
	program := "xdg-open"
	if runtime.GOOS == "darwin" {
		program = "open"
		if strings.HasSuffix(path, ".app") {
			path = filepath.Join(path, "Contents")
		}
	}
	command := exec.Command(program, path)
	configureChild(command)
	return command.Run()
}
