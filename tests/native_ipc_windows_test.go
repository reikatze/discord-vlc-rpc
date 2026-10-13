//go:build windows

package modules

import (
	"net"
	"testing"

	"github.com/Microsoft/go-winio"
)

func nativeDiscordListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := winio.ListenPipe(`\\.\pipe\discord-ipc-0`, &winio.PipeConfig{
		InputBufferSize: 65536, OutputBufferSize: 65536,
	})
	if err != nil {
		t.Fatal("create native Discord IPC mock; run in an isolated session without Discord:", err)
	}
	return listener
}
