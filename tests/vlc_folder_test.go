package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVLCFolderSurvivesProcessExitAndAppRestart(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, "Portable VLC")
			exe := filepath.Join(folder, "vlc")
			if goos == "windows" {
				exe += ".exe"
			}
			if goos == "darwin" {
				folder += ".app"
				exe = filepath.Join(folder, "Contents", "MacOS", "VLC")
			}
			if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
				t.Fatal(err)
			}
			p := paths{config: filepath.Join(root, "profile")}
			state := filepath.Join(root, "app-state", "last-vlc.json")
			proc := vlcProcess{exe: exe, args: []string{exe, "--config=" + p.vlcConfigFile()}}
			if got := p.vlcFolderFrom([]vlcProcess{proc}, goos, state); got != folder {
				t.Fatal("running", got)
			}
			// A new paths value has no remembered installation in memory.
			fresh := paths{config: p.config}
			if got := fresh.vlcFolderFrom(nil, goos, state); got != folder {
				t.Fatal("closed/restarted", got)
			}
			if fresh.config != p.config || fresh.configFile != "" {
				t.Fatal("profile changed")
			}
			if err := os.RemoveAll(folder); err != nil {
				t.Fatal(err)
			}
			if got := fresh.vlcFolderFrom(nil, goos, state); got != "" {
				t.Fatal("deleted folder reused", got)
			}
		})
	}
}

func TestVLCFolderOverridesProfilesAndFallback(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, folder := range []string{a, b} {
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
	}
	p := paths{config: filepath.Join(root, "profile"), vlc: b}
	state := filepath.Join(root, "state.json")
	proc := vlcProcess{exe: filepath.Join(a, "vlc.exe"), args: []string{"vlc", "--config=" + p.vlcConfigFile()}}
	p.vlcFolderFrom([]vlcProcess{proc}, "windows", state)
	p.vlcExplicit = true
	if got := p.vlcFolderFrom([]vlcProcess{proc}, "windows", state); got != b {
		t.Fatal("explicit override", got)
	}
	p.vlcExplicit = false
	if got := p.vlcFolderFrom(nil, "windows", state); got != a {
		t.Fatal("override changed saved folder", got)
	}
	proc.args = []string{"vlc", "--config=" + filepath.Join(root, "other", "vlcrc")}
	if got := p.vlcFolderFrom([]vlcProcess{proc}, "windows", state); got != a {
		t.Fatal("other profile replaced cache", got)
	}
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	if got := p.vlcFolderFrom(nil, "windows", state); got != b {
		t.Fatal("installation fallback", got)
	}
	if err := os.WriteFile(state, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := p.vlcFolderFrom(nil, "windows", state); got != b {
		t.Fatal("corrupt state blocked fallback", got)
	}
}
