# Application modules

This Go package contains the application logic for `discord-vlc-rpc`. The Source
root's `main.go` embeds the tray icon and calls `modules.Run(trayIcon)` once.
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
| `settings.go`, `settings_ui.go` | Saved configuration and browser settings |
| `paths.go`, `process_paths.go`, `vlc_*.go` | Installation and profile discovery |
| `http_setup.go`, `running_*.go` | VLC HTTP configuration and process waits |
| `autostart_*.go` | Per-user login startup |
| `files*.go`, `child_*.go` | File operations and platform process helpers |

Platform implementations retain their Go build constraints. Helpers are private
to this package; tests stay alongside the modules to exercise those helpers.

Run validation from the Source root:

```sh
go test -race ./...
go vet ./...
```

See [the project README](../README.md) for installation, settings and release builds.
