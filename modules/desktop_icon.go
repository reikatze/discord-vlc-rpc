package modules

import (
	"bytes"
	"io"
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
	if err := writeDesktopFileIfChanged(icon, IconPNG(256)); err != nil {
		return err
	}
	return writeDesktopFileIfChanged(filepath.Join(base, "applications", "discord-vlc-rpc.desktop"), desktopEntry(args, icon, false))
}

// Limit the comparison to the expected size, then replace missing or different
// content atomically. Identical files keep their modification time and inode.
func writeDesktopFileIfChanged(path string, body []byte) error {
	file, err := os.Open(path)
	if err == nil {
		existing, readErr := io.ReadAll(io.LimitReader(file, int64(len(body))+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if bytes.Equal(existing, body) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWrite(path, body, 0644)
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
