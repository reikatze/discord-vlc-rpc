package modules

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

type vlcProcess struct {
	pid      int
	exe, cwd string
	args     []string
	env      map[string]string
}

func (p paths) vlcConfigFile() string {
	if p.configFile != "" {
		return p.configFile
	}
	return filepath.Join(p.config, "vlcrc")
}

func processVLCFolder(exe, goos string) string {
	if goos == "darwin" {
		for folder := filepath.Dir(exe); folder != filepath.Dir(folder); folder = filepath.Dir(folder) {
			if strings.HasSuffix(strings.ToLower(folder), ".app") {
				return folder
			}
		}
	}
	return filepath.Dir(exe)
}

func processConfig(p vlcProcess, fallback, goos string) (string, error) {
	var config string
	for i := 1; i < len(p.args); i++ {
		arg := p.args[i]
		if arg == "--" {
			break
		}
		if arg == "--ignore-config" || arg == "--no-config" {
			return "", errors.New("Running VLC ignores saved configuration; restart VLC without --ignore-config")
		}
		if strings.HasPrefix(arg, "--config=") {
			config = strings.TrimPrefix(arg, "--config=")
		}
		if arg == "--config" {
			if i+1 >= len(p.args) {
				return "", errors.New("Cannot determine VLC's --config file")
			}
			i++
			config = p.args[i]
		}
	}
	if config != "" {
		if !filepath.IsAbs(config) {
			if p.cwd == "" {
				return "", errors.New("VLC uses a relative --config file; specify --config-file with its absolute path")
			}
			config = filepath.Join(p.cwd, config)
		}
		return filepath.Clean(config), nil
	}
	if goos == "windows" {
		portable := filepath.Join(filepath.Dir(p.exe), "portable")
		if isDirectory(portable) {
			return filepath.Join(portable, "vlcrc"), nil
		}
	} else if goos == "linux" {
		base := p.env["XDG_CONFIG_HOME"]
		if !filepath.IsAbs(base) {
			base = ""
		}
		if base == "" && filepath.IsAbs(p.env["HOME"]) {
			base = filepath.Join(p.env["HOME"], ".config")
		}
		if base != "" {
			return filepath.Join(base, "vlc", "vlcrc"), nil
		}
	} else if goos == "darwin" && filepath.IsAbs(p.env["HOME"]) {
		return filepath.Join(p.env["HOME"], "Library/Preferences/org.videolan.vlc", "vlcrc"), nil
	}
	return filepath.Join(fallback, "vlcrc"), nil
}

// Select the VLC profile once before settings and the instance lock are initialized.
// Retain it so HTTP polling and setup use the same VLC configuration.
func resolveRunningPaths(p paths, processes []vlcProcess, goos string) (paths, error) {
	var selected *vlcProcess
	var selectedConfig string
	for i := range processes {
		proc := &processes[i]
		folder := processVLCFolder(proc.exe, goos)
		if p.vlcExplicit && !samePath(folder, p.vlc, goos) {
			continue
		}
		file, err := processConfig(*proc, p.config, goos)
		if err != nil {
			if !p.configExplicit && !p.configFileExplicit {
				return p, err
			}
			file = p.vlcConfigFile()
		}
		if p.configExplicit && !samePath(filepath.Dir(file), p.config, goos) {
			continue
		}
		if p.configFileExplicit && !samePath(file, p.configFile, goos) {
			continue
		}
		if selected != nil && (!samePath(file, selectedConfig, goos) || !samePath(folder, processVLCFolder(selected.exe, goos), goos)) {
			return p, errors.New("Multiple VLC installations or profiles are running; select one with --vlc-dir and --config-dir (or --config-file)")
		}
		selected, selectedConfig = proc, file
	}
	if selected == nil {
		return p, nil
	}
	if !p.vlcExplicit {
		p.vlc = processVLCFolder(selected.exe, goos)
	}
	if !p.configFileExplicit {
		p.configFile = selectedConfig
		if !p.configExplicit {
			p.config = filepath.Dir(selectedConfig)
		}
	}
	return p, nil
}
func samePath(a, b, goos string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if goos == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func discoverPaths(p paths) (paths, error) {
	processes, err := runningVLCProcesses()
	if err != nil {
		return p, nil
	} // Permissions or unavailable process APIs keep explicit/default paths.
	return resolveRunningPaths(p, processes, runtime.GOOS)
}

func configArgumentRelative(args []string) bool {
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		value, ok := strings.CutPrefix(args[i], "--config=")
		if args[i] == "--config" && i+1 < len(args) {
			value = args[i+1]
			ok = true
		}
		if ok && value != "" && !filepath.IsAbs(value) {
			return true
		}
	}
	return false
}

func parseDarwinProcess(raw []byte) (vlcProcess, error) {
	if len(raw) < 5 || len(raw) > 2*1024*1024 {
		return vlcProcess{}, errors.New("Invalid process arguments")
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	if argc < 1 || argc > 65536 {
		return vlcProcess{}, errors.New("Invalid process argument count")
	}
	raw = raw[4:]
	end := bytes.IndexByte(raw, 0)
	if end < 0 {
		return vlcProcess{}, errors.New("Missing process executable")
	}
	p := vlcProcess{exe: string(raw[:end]), env: map[string]string{}}
	raw = bytes.TrimLeft(raw[end+1:], "\x00")
	for i := 0; i < argc; i++ {
		end = bytes.IndexByte(raw, 0)
		if end < 0 {
			return vlcProcess{}, errors.New("Truncated process arguments")
		}
		p.args = append(p.args, string(raw[:end]))
		raw = raw[end+1:]
	}
	for _, entry := range bytes.Split(raw, []byte{0}) {
		key, value, ok := strings.Cut(string(entry), "=")
		if ok && key == "HOME" {
			p.env[key] = value
		}
	}
	return p, nil
}
