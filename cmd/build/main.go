// Build all release companions, including menu-bar macOS app bundles.
package main

import (
	"archive/zip"
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
	var cmd *exec.Cmd
	for _, target := range []string{"windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
		parts := strings.Split(target, "/")
		name := parts[0]
		if name == "darwin" {
			name = "macos"
		}
		label := "discord-vlc-rpc-" + name + "-" + parts[1]
		dir := filepath.Join(out, label)
		must(os.MkdirAll(dir, 0755))
		exe := filepath.Join(dir, "discord-vlc-rpc")
		if parts[0] == "windows" {
			exe += ".exe"
		}
		if parts[0] == "darwin" {
			app := filepath.Join(dir, "discord-vlc-rpc.app/Contents")
			must(os.MkdirAll(filepath.Join(app, "MacOS"), 0755))
			exe = filepath.Join(app, "MacOS/discord-vlc-rpc")
			must(os.WriteFile(filepath.Join(app, "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.discord-vlc-rpc.companion</string><key>CFBundleName</key><string>discord-vlc-rpc</string><key>CFBundleExecutable</key><string>discord-vlc-rpc</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string><key>LSUIElement</key><true/><key>NSHighResolutionCapable</key><true/></dict></plist>`), 0644))
		}
		flags := "-s -w"
		if parts[0] == "windows" {
			flags += " -H=windowsgui"
		}
		cmd = exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags", flags, "-o", exe, ".")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[0], "GOARCH="+parts[1])
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		must(cmd.Run())
		for _, file := range []string{"LICENSE", "THIRD_PARTY_NOTICES.txt", "README.md"} {
			b, e := os.ReadFile(file)
			must(e)
			must(os.WriteFile(filepath.Join(dir, filepath.Base(file)), b, 0644))
		}
		must(os.MkdirAll(filepath.Join(dir, "assets"), 0755))
		for _, asset := range []string{"discord-logo.svg", "tmdb-logo.svg"} {
			b, e := os.ReadFile(filepath.Join("assets", asset))
			must(e)
			must(os.WriteFile(filepath.Join(dir, "assets", asset), b, 0644))
		}
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
