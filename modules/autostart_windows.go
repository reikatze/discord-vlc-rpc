//go:build windows

package modules

import (
	"golang.org/x/sys/windows/registry"
	"strings"
	"syscall"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	k, e := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if e != nil {
		return false
	}
	defer k.Close()
	v, _, e := k.GetStringValue("discord-vlc-rpc")
	return e == nil && v != ""
}
func setAutostart(enabled bool, args []string) error {
	k, _, e := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	if !enabled {
		e = k.DeleteValue("discord-vlc-rpc")
		if e == registry.ErrNotExist {
			return nil
		}
		return e
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = syscall.EscapeArg(a)
	}
	return k.SetStringValue("discord-vlc-rpc", strings.Join(q, " "))
}
