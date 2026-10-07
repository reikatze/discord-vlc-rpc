package modules

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGeneratedTrayIcon(t *testing.T) {
	encoded := TrayIcon()
	icon, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if icon.Bounds().Dx() != 64 || icon.Bounds().Dy() != 64 {
		t.Fatal("wrong dimensions", icon.Bounds())
	}
	if pixel := color.NRGBAModel.Convert(icon.At(0, 0)).(color.NRGBA); pixel.A != 0 {
		t.Fatal("background is not transparent", pixel)
	}
	if pixel := color.NRGBAModel.Convert(icon.At(32, 10)).(color.NRGBA); pixel != (color.NRGBA{244, 126, 33, 255}) {
		t.Fatal("orange circle missing", pixel)
	}
	if pixel := color.NRGBAModel.Convert(icon.At(30, 32)).(color.NRGBA); pixel != (color.NRGBA{255, 255, 255, 255}) {
		t.Fatal("play symbol missing", pixel)
	}
	if !bytes.Equal(encoded, TrayIcon()) {
		t.Fatal("icon generation is not deterministic")
	}
}

func TestApplicationIconContainers(t *testing.T) {
	icns := AppIconICNS()
	if string(icns[:4]) != "icns" || int(binary.BigEndian.Uint32(icns[4:])) != len(icns) {
		t.Fatal("invalid ICNS header")
	}
	entries := 0
	for offset := 8; offset < len(icns); entries++ {
		length := int(binary.BigEndian.Uint32(icns[offset+4:]))
		if length < 8 || offset+length > len(icns) {
			t.Fatal("invalid ICNS entry")
		}
		icon, err := png.Decode(bytes.NewReader(icns[offset+8 : offset+length]))
		if err != nil {
			t.Fatal(err)
		}
		expected := map[string]int{"icp4": 16, "icp5": 32, "icp6": 64, "ic07": 128, "ic08": 256, "ic09": 512, "ic10": 1024}[string(icns[offset:offset+4])]
		if expected == 0 || icon.Bounds().Dx() != expected {
			t.Fatal("wrong ICNS representation")
		}
		if !bytes.Equal(icns[offset+8:offset+length], IconPNG(expected)) {
			t.Fatal("ICNS drawing differs")
		}
		offset += length
	}
	if entries != 7 {
		t.Fatal("missing ICNS sizes")
	}
	for arch, machine := range map[string]uint16{"amd64": 0x8664, "arm64": 0xaa64} {
		object, err := AppIconWindows(arch)
		if err != nil {
			t.Fatal(err)
		}
		file, err := pe.NewFile(bytes.NewReader(object))
		if err != nil {
			t.Fatal(err)
		}
		if file.Machine != machine || len(file.Sections) != 1 {
			t.Fatal("invalid COFF target")
		}
		section := file.Sections[0]
		if section.Name != ".rsrc" || len(section.Relocs) != 7 {
			t.Fatal("missing icon resource relocations")
		}
		data, err := section.Data()
		if err != nil {
			t.Fatal(err)
		}
		for i, relocation := range section.Relocs {
			entry := data[relocation.VirtualAddress:]
			start, length := binary.LittleEndian.Uint32(entry), binary.LittleEndian.Uint32(entry[4:])
			payload := data[start : start+length]
			if i < 6 {
				size := []int{16, 32, 48, 64, 128, 256}[i]
				if !bytes.Equal(payload, IconPNG(size)) {
					t.Fatal("Windows drawing differs")
				}
			} else if binary.LittleEndian.Uint16(payload[4:]) != 6 {
				t.Fatal("missing Windows icon group")
			}
		}
		file.Close()
	}
	if _, err := AppIconWindows("unsupported"); err == nil {
		t.Fatal("unsupported COFF architecture accepted")
	}
}

func TestLinuxApplicationLauncher(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux desktop integration")
	}
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	args := []string{"/a folder/discord-vlc-rpc", "--config-dir", "/a profile/100%"}
	if err := installDesktopIcon(args); err != nil {
		t.Fatal(err)
	}
	icon, err := desktopIconPath()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(icon)
	if err != nil || !bytes.Equal(body, IconPNG(256)) {
		t.Fatal("launcher icon missing or differs", err)
	}
	launcher, err := os.ReadFile(filepath.Join(root, "data", "applications", "discord-vlc-rpc.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(launcher, []byte("Icon="+icon+"\n")) || !bytes.Contains(launcher, []byte(`Exec="/a folder/discord-vlc-rpc" "--config-dir" "/a profile/100%%"`)) {
		t.Fatal("launcher does not preserve icon and arguments", string(launcher))
	}
	if bytes.Contains(launcher, []byte("X-GNOME-Autostart-enabled")) {
		t.Fatal("launcher enables autostart")
	}
	if !bytes.Contains(desktopEntry(args, icon, true), []byte("Icon="+icon+"\n")) {
		t.Fatal("autostart icon differs")
	}
}

func TestLinuxLauncherSkipsUnchangedFilesAndRepairsChanges(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux desktop integration")
	}
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	args := []string{"/first/discord-vlc-rpc", "--config-dir", "/profile"}
	if err := installDesktopIcon(args); err != nil {
		t.Fatal(err)
	}
	icon, err := desktopIconPath()
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "data", "applications", "discord-vlc-rpc.desktop")
	stamp := time.Unix(1234567890, 0)
	before := make(map[string]os.FileInfo)
	for _, path := range []string{icon, launcher} {
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = info
	}
	if err := installDesktopIcon(args); err != nil {
		t.Fatal(err)
	}
	for path, info := range before {
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(info, after) || !after.ModTime().Equal(info.ModTime()) {
			t.Fatal("identical file rewritten", path)
		}
	}
	args[0] = "/moved/discord-vlc-rpc"
	args[2] = "/new-profile"
	if err := installDesktopIcon(args); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(icon)
	if err != nil || !os.SameFile(before[icon], after) {
		t.Fatal("launcher update rewrote icon", err)
	}
	body, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`/moved/discord-vlc-rpc`)) || !bytes.Contains(body, []byte(`/new-profile`)) {
		t.Fatal("launcher not updated")
	}
	if err := os.WriteFile(icon, []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(launcher); err != nil {
		t.Fatal(err)
	}
	if err := installDesktopIcon(args); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(icon)
	if err != nil || !bytes.Equal(body, IconPNG(256)) {
		t.Fatal("damaged icon not repaired", err)
	}
	if _, err := os.Stat(launcher); err != nil {
		t.Fatal("missing launcher not restored", err)
	}
}
