// Build all release companions, including menu-bar macOS app bundles.
package main

import (
	"archive/zip"
	"discord-vlc-rpc/modules"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	out := "../release-go"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	out, _ = filepath.Abs(out)
	must(os.MkdirAll(out, 0755))
	// Build in a temporary package so resource objects never alter the source.
	stage, err := os.MkdirTemp(".", ".discord-vlc-rpc-build-")
	must(err)
	defer os.RemoveAll(stage)
	entry, err := os.ReadFile("main.go")
	must(err)
	must(os.WriteFile(filepath.Join(stage, "main.go"), entry, 0644))
	var cmd *exec.Cmd
	for _, target := range []string{"windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
		parts := strings.Split(target, "/")
		name := parts[0]
		if name == "darwin" {
			name = "macos"
		}
		label := "discord-vlc-rpc-" + name + "-" + parts[1]
		dir, err := os.MkdirTemp(out, label+"-")
		must(err)
		defer os.RemoveAll(dir)
		exe := filepath.Join(dir, "discord-vlc-rpc")
		if parts[0] == "windows" {
			exe += ".exe"
			resource, err := modules.AppIconWindows(parts[1])
			must(err)
			must(os.WriteFile(filepath.Join(stage, "icon_windows_"+parts[1]+".syso"), resource, 0644))
		}
		if parts[0] == "darwin" {
			app := filepath.Join(dir, "discord-vlc-rpc.app/Contents")
			must(os.MkdirAll(filepath.Join(app, "MacOS"), 0755))
			exe = filepath.Join(app, "MacOS/discord-vlc-rpc")
			must(os.MkdirAll(filepath.Join(app, "Resources"), 0755))
			must(os.WriteFile(filepath.Join(app, "Resources/icon.icns"), modules.AppIconICNS(), 0644))
			must(os.WriteFile(filepath.Join(app, "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.discord-vlc-rpc.companion</string><key>CFBundleName</key><string>discord-vlc-rpc</string><key>CFBundleExecutable</key><string>discord-vlc-rpc</string><key>CFBundleIconFile</key><string>icon.icns</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string><key>LSUIElement</key><true/><key>NSHighResolutionCapable</key><true/></dict></plist>`), 0644))
		}
		flags := "-s -w"
		if parts[0] == "windows" {
			flags += " -H=windowsgui"
		}
		cmd = exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags", flags, "-o", exe, "./"+filepath.ToSlash(stage))
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[0], "GOARCH="+parts[1])
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		must(cmd.Run())
		f, e := os.Create(filepath.Join(out, label+".zip"))
		must(e)
		w := zip.NewWriter(f)
		must(filepath.Walk(dir, func(path string, info os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			if info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, path)
			h, e := zip.FileInfoHeader(info)
			if e != nil {
				return e
			}
			h.Name = filepath.ToSlash(rel)
			h.Method = zip.Deflate
			s, e := w.CreateHeader(h)
			if e != nil {
				return e
			}
			r, e := os.Open(path)
			if e != nil {
				return e
			}
			defer r.Close()
			_, e = io.Copy(s, r)
			return e
		}))
		must(w.Close())
		must(f.Close())
		fmt.Println("Built", label)
	}
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
