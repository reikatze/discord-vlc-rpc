# Application modules

This Go package contains the application logic for `discord-vlc-rpc`. The Source
root's `main.go` calls `modules.Run(modules.TrayIcon())` once. `icon.go` draws
the tray icon in memory with Go's standard image library.
`Run` retains the application's operating-system thread lock for the tray,
command-line options, startup discovery and shutdown behavior.

| Files | Responsibility |
|---|---|
| `app.go` | Application startup, tray menu and shutdown |
| `service.go` | Playback polling and background-job coordination |
| `playback.go`, `model.go` | VLC HTTP responses, chapter lookup and presence data |
| `filename.go` | Filename parsing and title matching |
| `metadata.go`, `index.go` | TMDb requests, metadata caching and local title indexes |
| `rpc.go`, `rpc_*.go` | Discord IPC and reconnection |
| `instance_lock.go`, `instance_lock_windows.go`, `instance_lock_unix.go` | OS-managed instance lock without a TCP listener |
| `settings.go` | Per-user JSON configuration, independent of VLC profiles |
| `paths.go`, `process_paths.go`, `vlc_*.go` | Installation and profile discovery |
| `http_setup.go`, `running_*.go` | VLC HTTP configuration and process waits |
| `autostart_*.go` | Per-user login startup |
| `files*.go`, `child_*.go` | File operations and platform process helpers |

Platform implementations retain their Go build constraints. Helpers are private
to this package. Unit tests live in `../tests/`; `cmd/test` overlays them into
this package during testing and vetting, preserving access to private helpers.

Run validation from the Source root:

```sh
go run ./cmd/test -race ./...
go run ./cmd/test vet ./...
```

See [the project README](../README.md) for installation, settings and release builds.

`icon.go` draws the shared tray/application design; `icon_formats.go` creates Windows COFF resources and macOS ICNS containers. `desktop_icon.go` installs the Linux application launcher and supplies its icon to autostart entries.
