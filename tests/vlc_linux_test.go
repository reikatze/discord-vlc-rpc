//go:build linux

package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxProcessFilesystemDiscovery(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "Apps", "vlc")
	os.MkdirAll(filepath.Dir(exe), 0700)
	os.WriteFile(exe, []byte("executable"), 0600)
	procRoot := filepath.Join(root, "proc")
	base := filepath.Join(procRoot, "123")
	os.MkdirAll(base, 0700)
	os.Symlink(exe, filepath.Join(base, "exe"))
	os.Symlink(root, filepath.Join(base, "cwd"))
	os.WriteFile(filepath.Join(base, "cmdline"), []byte("vlc\x00--config\x00settings/custom.conf\x00"), 0600)
	os.WriteFile(filepath.Join(base, "environ"), []byte("HOME="+root+"\x00XDG_CONFIG_HOME="+filepath.Join(root, "profile")+"\x00SECRET=discarded\x00"), 0600)
	found, err := linuxVLCProcesses(procRoot, uint32(os.Getuid()))
	if err != nil || len(found) != 1 || found[0].exe != exe || found[0].cwd != root || len(found[0].env) != 2 {
		t.Fatal(found, err)
	}
	p, err := resolveRunningPaths(paths{config: filepath.Join(root, "fallback")}, found, "linux")
	if err != nil || p.vlcConfigFile() != filepath.Join(root, "settings/custom.conf") {
		t.Fatal(p, err)
	}
	if other, err := linuxVLCProcesses(procRoot, uint32(os.Getuid()+1)); err != nil || len(other) != 0 {
		t.Fatal("other user process selected", other, err)
	}
}
