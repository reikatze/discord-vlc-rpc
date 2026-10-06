//go:build windows

package modules

import (
	"context"
	"errors"
	"fmt"
	"github.com/Microsoft/go-winio"
	"net"
	"time"
)

func connectDiscord() (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 10; i++ {
		c, err := winio.DialPipeContext(ctx, fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i))
		if err == nil {
			return c, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.New("Discord desktop unavailable")
}
