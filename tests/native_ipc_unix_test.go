//go:build !windows

package modules

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func nativeDiscordListener(t *testing.T) net.Listener {
	t.Helper()
	// Short paths also fit macOS's Unix-domain socket path limit.
	dir, err := os.MkdirTemp("/tmp", "dv-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for _, key := range []string{"XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "discord-ipc-0"))
	if err != nil {
		t.Fatal("create native Discord IPC mock:", err)
	}
	return listener
}
