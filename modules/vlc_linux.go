//go:build linux

package modules

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
	if len(raw) > 2*1024*1024 {
		return nil, errors.New("Process data exceeds limit")
	}
	return raw, err
}
func profileEnvironment(raw []byte) map[string]string {
	env := map[string]string{}
	for _, entry := range bytes.Split(raw, []byte{0}) {
		key, value, ok := strings.Cut(string(entry), "=")
		if ok && (key == "HOME" || key == "XDG_CONFIG_HOME") {
			env[key] = value
		}
	}
	return env
}
func runningVLCProcesses() ([]vlcProcess, error) {
	return linuxVLCProcesses("/proc", uint32(os.Getuid()))
}
func linuxVLCProcesses(procRoot string, uid uint32) ([]vlcProcess, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var found []vlcProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		base := filepath.Join(procRoot, entry.Name())
		info, err := os.Stat(base)
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uid {
			continue
		}
		exe, err := os.Readlink(filepath.Join(base, "exe"))
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		name := strings.ToLower(filepath.Base(exe))
		if name != "vlc" && name != "vlc.bin" && name != "cvlc" {
			continue
		}
		cmd, err := processBytes(filepath.Join(base, "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(cmd), "\x00"), "\x00")
		env, _ := processBytes(filepath.Join(base, "environ"))
		cwd, _ := os.Readlink(filepath.Join(base, "cwd"))
		if !exists(exe) {
			mounted := filepath.Join(base, "root", strings.TrimPrefix(exe, "/"))
			if exists(mounted) {
				exe = mounted
			}
		}
		found = append(found, vlcProcess{pid: pid, exe: exe, cwd: cwd, args: args, env: profileEnvironment(env)})
	}
	return found, nil
}
