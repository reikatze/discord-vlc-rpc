//go:build !windows

package modules

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func autostartPath() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library/LaunchAgents/com.discord-vlc-rpc.companion.plist")
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "autostart/discord-vlc-rpc.desktop")
}
func autostartEnabled() bool { return exists(autostartPath()) }
func desktopArg(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", "\\$").Replace(s)
	return strings.ReplaceAll(`"`+s+`"`, `\`, `\\`)
}
func autostartBody(args []string) []byte {
	if runtime.GOOS == "darwin" {
		var body strings.Builder
		body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>com.discord-vlc-rpc.companion</string><key>ProgramArguments</key><array>`)
		for _, arg := range args {
			body.WriteString("<string>")
			xml.EscapeText(&body, []byte(arg))
			body.WriteString("</string>")
		}
		body.WriteString("</array><key>RunAtLoad</key><true/></dict></plist>\n")
		return []byte(body.String())
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = desktopArg(a)
	}
	return []byte("[Desktop Entry]\nType=Application\nName=discord-vlc-rpc\nComment=VLC Discord presence companion\nExec=" + strings.Join(quoted, " ") + "\nTerminal=false\nX-GNOME-Autostart-enabled=true\n")
}
func setAutostart(enabled bool, args []string) error {
	path := autostartPath()
	if !enabled {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return atomicWrite(path, autostartBody(args), 0600)
}
