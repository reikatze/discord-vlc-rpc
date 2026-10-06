package main

import (
	_ "embed"

	"discord-vlc-rpc/modules"
)

//go:embed assets/icon.png
var trayIcon []byte

func main() {
	modules.Run(trayIcon)
}
