//go:build darwin

package modules

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func runningVLCProcesses() ([]vlcProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/pgrep", "-u", strconv.Itoa(os.Getuid()), "-x", "[Vv][Ll][Cc]")
	cmd.WaitDelay = 100 * time.Millisecond
	raw, err := cmd.Output()
	if err != nil {
		var e *exec.ExitError
		if errors.As(err, &e) && e.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	var found []vlcProcess
	for _, value := range strings.Fields(string(raw)) {
		if ctx.Err() != nil || len(found) >= 32 {
			break
		}
		pid, err := strconv.Atoi(value)
		if err != nil {
			continue
		}
		raw, err := unix.SysctlRaw("kern.procargs2", pid)
		if err != nil {
			continue
		}
		p, err := parseDarwinProcess(raw)
		if err != nil || !filepath.IsAbs(p.exe) {
			continue
		}
		name := strings.ToLower(filepath.Base(p.exe))
		if name != "vlc" {
			continue
		}
		p.pid = pid
		// Only needed for an explicit relative --config; lsof's name field preserves spaces.
		if configArgumentRelative(p.args) {
			cwdCmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", value, "-d", "cwd", "-Fn")
			cwdCmd.WaitDelay = 100 * time.Millisecond
			if raw, err := cwdCmd.Output(); err == nil {
				for _, line := range strings.Split(string(raw), "\n") {
					if strings.HasPrefix(line, "n/") {
						p.cwd = strings.TrimPrefix(line, "n")
						break
					}
				}
			}
		}
		found = append(found, p)
	}
	return found, nil
}
