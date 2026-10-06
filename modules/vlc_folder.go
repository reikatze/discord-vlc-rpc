package modules

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type rememberedVLC struct {
	Folder string `json:"vlc_folder"`
}

func vlcFolderStateFile() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "discord-vlc-rpc", "last-vlc.json")
}

func rememberVLCFolder(file, folder string) {
	if file == "" || !filepath.IsAbs(folder) || !isDirectory(folder) {
		return
	}
	var previous rememberedVLC
	if readJSON(file, 65536, &previous) == nil && previous.Folder == folder {
		return
	}
	body, err := json.Marshal(rememberedVLC{Folder: folder})
	if err == nil {
		_ = atomicWrite(file, append(body, '\n'), 0600)
	}
}

// Pin the session's configuration profile while refreshing only its install folder.
func (p paths) vlcFolderFrom(processes []vlcProcess, goos, stateFile string) string {
	if p.vlcExplicit {
		return p.vlc
	}
	pinned := p
	pinned.vlc = "" // Distinguish an actual running match from a discovery fallback.
	pinned.configExplicit, pinned.configFileExplicit = true, true
	pinned.configFile = p.vlcConfigFile()
	if found, err := resolveRunningPaths(pinned, processes, goos); err == nil && filepath.IsAbs(found.vlc) && isDirectory(found.vlc) {
		rememberVLCFolder(stateFile, found.vlc)
		return found.vlc
	}
	var previous rememberedVLC
	if stateFile != "" && readJSON(stateFile, 65536, &previous) == nil && filepath.IsAbs(previous.Folder) && isDirectory(previous.Folder) {
		return previous.Folder
	}
	if filepath.IsAbs(p.vlc) && isDirectory(p.vlc) {
		return p.vlc
	}
	return ""
}

func (p paths) vlcFolder() string {
	if p.vlcExplicit {
		return p.vlc
	}
	processes, _ := runningVLCProcesses()
	if folder := p.vlcFolderFrom(processes, runtime.GOOS, vlcFolderStateFile()); folder != "" {
		return folder
	}
	if folder := defaultPaths().vlc; filepath.IsAbs(folder) && isDirectory(folder) {
		return folder
	}
	return ""
}

func (p paths) trackVLCFolder(ctx context.Context) {
	if p.vlcExplicit {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		p.vlcFolder()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
