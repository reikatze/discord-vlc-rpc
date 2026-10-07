# Standalone companion release validation

Built with Go 1.27.1 and CGO disabled for Windows, macOS and Linux in amd64 and
arm64. Windows executables use the GUI subsystem; macOS ZIPs contain menu-bar app
bundles. Project and third-party license notices are included. Packages are unsigned.

Passed here:

- Go tests with the race detector and Go vet.
- End-to-end mock VLC HTTP to Discord IPC: playback, paused timestamps, ignored
  directory clearing and prevention of private activity returning.
- Filename/episode/year parsing and ignored-path boundaries.
- Authenticated HTTP probing, redirect rejection, deadlines, settings preservation,
  backup accuracy, generated passwords, duplicate keys and idempotence.
- TMDb episode names/counts/stills, disk caching, misses, rate limits, cancellation
  and credential-free errors, using mock endpoints.
- Streaming gzip indexing, external merge sorting, ambiguity handling, binary
  lookup, manifest validation and failure/cancellation publication safety.
- Settings defaults and persistence, protected browser settings, CSRF rejection
  and hidden saved credentials.
- Configuration caching: unchanged files, atomic replacements with matching size
  and timestamp, invalid edits, deletion/recreation and forced reloads.
- Cancelled metadata results rejected when a newer lookup targets the same file;
  current lookup completion and cancellation state remain intact.
- Discord fragmented frames, ping/pong, frame caps, errors, deduplication, clearing,
  reconnection and bounded idle reads.
- Stable timestamps through quantized playback samples, with re-anchoring for
  seeks, pauses/resumes, speed/duration changes and different media.
- Filename parse reuse and release-group stripping.
- HTTP polling backoff, bounded retry intervals, immediate Refresh and active recovery.
- Chapter cache reuse, same-size/timestamp replacements, expiration, failed retries,
  cancellation, concurrent probe sharing and the 32-file bound.
- Running-process path selection, explicit overrides, Windows portable profiles,
  custom/relative configuration files, Linux HOME/XDG environment, macOS bundle
  paths and native argument parsing, and ambiguity handling.
- Linux process filesystem discovery with synthetic proc entries and user filtering.
- Discord socket discovery overall deadline, cancellation, cached-socket priority,
  fallback and directory-scope invalidation, using in-memory transports.
- Index reader generation pinning, reuse across title/media queries, prompt
  handle closure before network requests and corrupt-index online fallback.
- All six cross-compilation targets and ZIP integrity.
- Packaged Linux x64 headless startup, standalone configuration creation and
  bounded shutdown and preservation of VLC preferences on startup.

Tests use synthetic exports and local mock services. No actual TMDb database was
downloaded or copied. This host cannot listen on Unix sockets, so protocol tests
use TCP or in-memory mock transports. Live VLC/Discord IPC, Windows named pipes,
platform tray/login behavior and optional ffprobe labels need target
hardware validation. Signing and macOS notarization have not been performed.

Reproduce from the Source root: `go run ./cmd/test -race ./...`, `go run ./cmd/test vet ./...`, and
`go run ./cmd/build ../release-go`.

## Local performance checks

Synthetic 30,000-title index build, ten iterations on this Linux host:
61.1 ms with buffered output versus 95.6 ms with direct row/offset writes
(about 36% less build time). Buffers use 128 KiB total and flush before file sync
and manifest publication.

Unchanged VLC configuration polling, 200 iterations with a synthetic 132 KiB
configuration: about 0.57 microseconds and 272 allocated bytes with the file cache,
versus 220 microseconds and 548 kB with repeated reads/parsing. These are local
microbenchmarks, not estimates of overall application CPU usage or target-device
performance. Polling still checks file identity, size and modification time.

Reproduce with `go run ./cmd/test ./modules -run '^$' -bench 'Benchmark(IndexBuild|VLCConfigurationPolling)' -benchmem`.

Filename parsing microbenchmark (1,000 iterations on the same host): about
11.9 microseconds and 53 allocations for parsing a representative episode path,
versus zero allocations when reusing its cached result. Release-group regexes
and the title separator replacer are now constructed once. Reproduce with
`go run ./cmd/test ./modules -run '^$' -bench BenchmarkFilenameParsing -benchmem`.

Periodic tray status labels and checkbox values update only when changed.
Live tray behavior remains part of target-desktop validation.

Running-process discovery is performed before companion settings, the profile lock
and the browser server are initialized. The session retains its selected profile.
Windows executable/command-line queries and macOS sysctl/lsof calls still require
native target validation. This host denies reading child-process `/proc/exe` and
`/proc/cwd`, so live Linux discovery is not validated here; the filesystem parser
and resolver are covered with synthetic process fixtures. Process environments
retain only profile-related HOME/XDG fields and are never logged.

Index candidate collection now opens one generation and reuses its file handles
and fixed read buffers for all title variants and media kinds in that lookup.
Handles close before network requests; a missing or incomplete generation leaves
online search available. TV-only lookups open only the TV pair.

Synthetic eight-query candidate collection (four titles across movie and TV,
three runs of 1,000 batches on this host): median 74.9 microseconds and about
12.3 kB allocated per batch, versus 266 microseconds and about 90.6 kB with the
previous implementation. This fixture has two tiny synthetic media indexes;
timings varied substantially across runs. The measurements describe file/manifest
reuse, not full-database performance or
overall application CPU usage. Reproduce the current batch comparison with
`go run ./cmd/test ./modules -run '^$' -bench BenchmarkIndexCandidateBatch -benchmem`.

Discord socket discovery on macOS/Linux shares a one-second overall deadline
across all candidates, with a 50 ms per-attempt limit. The most recently connected
socket is tried first while it remains in the current directory scope. Failed
cached sockets fall back to discovery, and failed searches invalidate stale
cached paths. Native IPC behavior still requires target-desktop validation.

The Go project now builds from the Source root. A small root `main.go` generates the tray icon with
`modules.TrayIcon()` and calls `modules.Run`; application logic is in `modules`
and unit test sources are in `tests`, while `cmd/build` creates release packages containing only
the executable or macOS app bundle. Package relocation preserves platform build constraints and the
tray's operating-system thread lock. Race tests, vet, six cross-builds and
packaged Linux headless startup/shutdown were rechecked after the move.

Startup notifications default to enabled and can be disabled in Settings. Tests
cover saving both toggle values, loading configurations without the new option,
omitting the built-in Discord ID from settings and JSON, retaining custom IDs,
and using the effective ID for playback. Windows folder actions use ShellExecuteW
with thread-scoped COM initialization instead of waiting for Explorer's exit.
Race tests, vet, and six cross-builds pass; actual notification display and folder
opening still require a Windows desktop retest.

Remembered VLC folder tests cover Windows, macOS and Linux process fixtures,
process exit and application restart, explicit overrides, configuration-profile
isolation, deleted folders, and malformed state. The installation folder is
stored independently from profile settings and refreshed in a background loop.

Application icons use the same procedural drawing as the tray. Container tests decode all macOS ICNS representations and inspect Windows COFF resources for both architectures. Linux launcher tests verify the generated icon, preserved arguments, and autostart icon. All six release packages were rebuilt and inspected for linked Windows resource entries and macOS bundle icon metadata. Live Explorer/Finder/desktop icon display still needs validation on those systems.
