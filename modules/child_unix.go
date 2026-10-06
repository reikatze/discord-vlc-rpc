//go:build !windows

package modules

import "os/exec"

func configureChild(command *exec.Cmd) {}
