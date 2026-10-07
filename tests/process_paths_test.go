package modules

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRunningVLCProfileResolutionAndOverrides(t *testing.T) {
	root := t.TempDir()
	fallback := filepath.Join(root, "user", "vlc")
	exe := filepath.Join(root, "Apps", "VLC", "vlc.exe")
	portable := filepath.Join(filepath.Dir(exe), "portable")
	os.MkdirAll(portable, 0700)
	proc := vlcProcess{exe: exe, args: []string{exe}, env: map[string]string{}}
	p := paths{config: fallback}
	got, err := resolveRunningPaths(p, []vlcProcess{proc}, "windows")
	if err != nil || got.vlc != filepath.Dir(exe) || got.config != portable || got.vlcConfigFile() != filepath.Join(portable, "vlcrc") {
		t.Fatal(got, err)
	}
	custom := filepath.Join(root, "Data", "settings", "custom.conf")
	proc.args = []string{exe, "--config=" + custom}
	got, err = resolveRunningPaths(p, []vlcProcess{proc}, "windows")
	if err != nil || got.config != filepath.Dir(custom) || got.vlcConfigFile() != custom {
		t.Fatal("custom config ignored", got, err)
	}
	// A directory override keeps the selected directory but can discover a custom filename inside it.
	p.config, p.configExplicit = filepath.Dir(custom), true
	got, err = resolveRunningPaths(p, []vlcProcess{proc}, "windows")
	if err != nil || got.config != p.config || got.vlcConfigFile() != custom {
		t.Fatal("directory override", got, err)
	}
	p = paths{config: fallback, configExplicit: true, vlc: filepath.Join(root, "explicit"), vlcExplicit: true}
	got, err = resolveRunningPaths(p, []vlcProcess{proc}, "windows")
	if err != nil || got != p {
		t.Fatal("explicit paths overwritten", got, err)
	}
	got, err = resolveRunningPaths(p, nil, "windows")
	if err != nil || got != p {
		t.Fatal("defaults changed without process")
	}
}

func TestLinuxAndMacRunningProfileResolution(t *testing.T) {
	root := t.TempDir()
	fallback := filepath.Join(root, "fallback")
	for _, test := range []struct {
		goos, exe              string
		env                    map[string]string
		wantFolder, wantConfig string
	}{
		{"linux", filepath.Join(root, "usr/bin/vlc"), map[string]string{"XDG_CONFIG_HOME": filepath.Join(root, "xdg"), "HOME": root}, filepath.Join(root, "usr/bin"), filepath.Join(root, "xdg/vlc")},
		{"linux", filepath.Join(root, "AppImage/usr/bin/vlc"), map[string]string{"HOME": filepath.Join(root, "portable-home")}, filepath.Join(root, "AppImage/usr/bin"), filepath.Join(root, "portable-home/.config/vlc")},
		{"darwin", filepath.Join(root, "Volumes/Apps/My VLC.app/Contents/MacOS/VLC"), map[string]string{"HOME": root}, filepath.Join(root, "Volumes/Apps/My VLC.app"), filepath.Join(root, "Library/Preferences/org.videolan.vlc")},
	} {
		got, err := resolveRunningPaths(paths{config: fallback}, []vlcProcess{{exe: test.exe, args: []string{test.exe}, env: test.env}}, test.goos)
		if err != nil || got.vlc != test.wantFolder || got.config != test.wantConfig {
			t.Fatal(test.goos, got, err)
		}
	}
}

func TestRelativeConfigAmbiguityAndIgnoredConfiguration(t *testing.T) {
	root := t.TempDir()
	p := paths{config: filepath.Join(root, "default")}
	proc := vlcProcess{exe: filepath.Join(root, "bin/vlc"), cwd: root, args: []string{"vlc", "--config", "settings/custom.conf"}}
	got, err := resolveRunningPaths(p, []vlcProcess{proc}, "linux")
	if err != nil || got.vlcConfigFile() != filepath.Join(root, "settings/custom.conf") {
		t.Fatal(got, err)
	}
	proc.cwd = ""
	if _, err := resolveRunningPaths(p, []vlcProcess{proc}, "linux"); err == nil {
		t.Fatal("relative config guessed without cwd")
	}
	proc.args = []string{"vlc", "--ignore-config"}
	if _, err := resolveRunningPaths(p, []vlcProcess{proc}, "linux"); err == nil {
		t.Fatal("ignored settings accepted")
	}
	a := vlcProcess{exe: filepath.Join(root, "a/vlc"), args: []string{"vlc", "--config=" + filepath.Join(root, "a/vlcrc")}}
	b := vlcProcess{exe: filepath.Join(root, "b/vlc"), args: []string{"vlc", "--config=" + filepath.Join(root, "b/vlcrc")}}
	if _, err := resolveRunningPaths(p, []vlcProcess{a, b}, "linux"); err == nil {
		t.Fatal("ambiguous profiles selected")
	}
	p.vlc, p.vlcExplicit = filepath.Dir(b.exe), true
	got, err = resolveRunningPaths(p, []vlcProcess{a, b}, "linux")
	if err != nil || got.config != filepath.Join(root, "b") {
		t.Fatal("explicit folder did not select process", got, err)
	}
	// A media filename after -- is not a process option.
	file, err := processConfig(vlcProcess{exe: a.exe, args: []string{"vlc", "--", "--config=/wrong"}}, p.config, "linux")
	if err != nil || file != filepath.Join(p.config, "vlcrc") {
		t.Fatal("media interpreted as config option")
	}
}

func TestDarwinNativeArgumentsPreserveSpacesAndEnvironment(t *testing.T) {
	args := []string{"/Applications/My VLC.app/Contents/MacOS/VLC", "--config", "/Users/user/My Profile/custom.conf", ""}
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, uint32(len(args)))
	raw = append(raw, []byte(args[0]+"\x00\x00\x00")...)
	for _, arg := range args {
		raw = append(raw, []byte(arg+"\x00")...)
	}
	raw = append(raw, []byte("HOME=/Users/user\x00SECRET=not-retained\x00")...)
	proc, err := parseDarwinProcess(raw)
	if err != nil || proc.exe != args[0] || !reflect.DeepEqual(proc.args, args) || proc.env["HOME"] != "/Users/user" || len(proc.env) != 1 {
		t.Fatal("native argument parsing", err)
	}
	if _, err := parseDarwinProcess(raw[:6]); err == nil {
		t.Fatal("truncated native arguments accepted")
	}
}
