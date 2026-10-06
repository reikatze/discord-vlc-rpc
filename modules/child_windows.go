package modules

import (
	"os/exec"
	"syscall"
)

func configureChild(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
