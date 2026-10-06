//go:build !windows

package modules

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

func vlcRunning() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pgrep", "-x", "[Vv][Ll][Cc]")
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, errors.New("Cannot check whether VLC is running; pgrep must be available")
}
