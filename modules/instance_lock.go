package modules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errInstanceRunning = errors.New("Another discord-vlc-rpc instance is already running")

// Keep the file in place: ownership is the OS lock, not the file's existence.
// Removing it could let another process lock a different file at the same path.
func acquireInstanceLock(folder string) (*os.File, error) {
	if folder == "" {
		return nil, errors.New("Cannot determine the application configuration directory")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		return nil, fmt.Errorf("Create instance lock folder: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(folder, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("Open instance lock: %w", err)
	}
	if err := lockInstanceFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("Acquire instance lock: %w", err)
	}
	// Closing the handle, including at process termination, releases ownership.
	return file, nil
}
