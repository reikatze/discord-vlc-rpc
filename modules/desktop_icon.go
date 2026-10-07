package modules

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func desktopIconPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "discord-vlc-rpc", "icon.png"), nil
}

// Linux executables have no file icon resource. Install a per-user launcher
// when starting the tray, with the same generated icon and original arguments.
func installDesktopIcon(args []string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	icon, err := desktopIconPath()
	if err != nil {
		return err
	}
	base := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, ".local", "share")
	}
	if err := atomicWrite(icon, IconPNG(256), 0644); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(base, "applications", "discord-vlc-rpc.desktop"), desktopEntry(args, icon, false), 0644)
}

func desktopArg(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", "\\$").Replace(s)
	return strings.ReplaceAll(`"`+s+`"`, `\`, `\\`)
}

func desktopEntry(args []string, icon string, autostart bool) []byte {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = desktopArg(arg)
	}
	body := "[Desktop Entry]\nType=Application\nName=discord-vlc-rpc\nComment=VLC Discord presence companion\nExec=" + strings.Join(quoted, " ") + "\nTerminal=false\n"
	if icon != "" {
		body += "Icon=" + strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(icon) + "\n"
	}
	if autostart {
		body += "X-GNOME-Autostart-enabled=true\n"
	}
	return []byte(body)
}
