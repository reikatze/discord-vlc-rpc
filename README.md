# discord-vlc-rpc

<p align="center">
  <a href="https://discord.com/"><img src="assets/discord-logo.svg" alt="Discord" height="40"></a>&nbsp;&nbsp;&nbsp;&nbsp;
  <a href="https://www.themoviedb.org/"><img src="assets/tmdb-logo.svg" alt="TMDb" height="40"></a>
</p>

A small companion app that shows what you are watching in [VLC](https://www.videolan.org/vlc/) as Discord Rich Presence. It runs on Windows, macOS, and Linux and reads playback through VLC's built-in HTTP interface.

It works on its own with filenames and VLC metadata. Add a free [TMDb](https://www.themoviedb.org/) API key if you also want official titles, posters, episode names, episode stills, and links back to TMDb.

## What it does

- Shows **Watching `<title>`** in Discord
- Follows playing, paused, and idle states, plus buffering when VLC reports it
- Keeps Discord's progress bar in sync with playback speed and seeks
- Understands common movie, TV, scene, and anime filenames
- Removes common release tags before looking up a title
- Can show episode details such as `04 of 20: Chatty`
- Uses TMDb posters and verified episode stills when available
- Reconnects automatically if Discord restarts
- Caches metadata between app sessions
- Can use a local TMDb title index to help find exact matches
- Lets you hide private files and folders
- Includes a tray menu for settings, status, and optional autostart

You do not need a Discord bot, OAuth setup, or installation link.

## Before you start

You will need:

- [VLC](https://www.videolan.org/vlc/)
- The Discord desktop app
- **Display current activity as a status message** enabled under Discord's **Settings > Activity Privacy**
- VLC's local HTTP interface configured through the app's tray menu
- A free [TMDb API key](https://www.themoviedb.org/settings/api) or read access token for TMDb features

Basic Rich Presence works without a TMDb key. Optional chapter-name lookup uses `ffprobe` when it is available on your `PATH`.

## Install

Download the ZIP for your operating system and architecture, then extract it to a permanent folder.

| OS | Packages | Open |
|---|---|---|
| Windows | `windows-amd64` or `windows-arm64` | `discord-vlc-rpc.exe` |
| macOS | `macos-amd64` or `macos-arm64` | `discord-vlc-rpc.app` |
| Linux | `linux-amd64` or `linux-arm64` | `discord-vlc-rpc` |

`amd64` means x64; `arm64` is for ARM64 processors, including Apple silicon on macOS. On Linux, restore the executable permission with `chmod +x discord-vlc-rpc` if needed.

Linux trays need StatusNotifierItem support. GNOME may need an AppIndicator extension. Release packages are unsigned; Windows signing and macOS signing/notarization are separate release steps.

## Get it working

1. Start Discord and VLC.
2. Open `discord-vlc-rpc`. Starting VLC first lets the app discover its installation folder and configuration profile.
3. Open the tray menu and choose **Check / Configure VLC HTTP Interface**.
4. If setup is needed, close VLC when prompted. The app waits up to five minutes for it to exit, then applies the configuration.
5. Start or restart VLC, choose the check action again, and confirm **VLC HTTP: Ready**.
6. Open a local video in VLC and check your Discord profile for the activity.

For the full experience, open **Settings…** in the tray menu and save your TMDb key or read access token. The saved key stays hidden; leaving the key field empty keeps it, and a separate checkbox removes it.

HTTP setup preserves unrelated VLC preferences, an existing password, and other interfaces. It creates a password if needed, saves a timestamped backup of an existing VLC configuration file, and configures new HTTP access on `127.0.0.1`. A working HTTP configuration is preserved. VLC preferences change only when you select the setup action.

The app includes a Discord Application ID, so you can leave that setting at its default. To use your own application, create one in the [Discord Developer Portal](https://discord.com/developers/applications) and save its ID in **Settings…** or `discord_application_id` in the configuration file.

For a custom application, you can upload square Rich Presence assets and set their names in `large_image`, `small_image_playing`, `small_image_paused`, and `small_image_idle`. These asset names are empty by default.

## Tray menu

| Action | What it does |
|---|---|
| **Autostart** | Starts the app when your user account logs in |
| **Settings…** | Opens the local settings page in your browser |
| **Enable Discord presence** | Saves your on/off preference |
| **Pause presence for this session** | Temporarily hides activity |
| **Refresh playback / metadata** | Checks playback immediately and reloads settings and metadata |
| **Check / Configure VLC HTTP Interface** | Checks readiness and configures local HTTP access when needed |
| **Pause database updates for this session** | Temporarily pauses local-index maintenance |
| **Build / refresh local title database** | Requests an index rebuild when the index and TMDb key are enabled |
| **Clear metadata cache** | Removes saved TMDb matches |
| **Open VLC / database / configuration / metadata cache folder** | Opens the selected folder |
| **Quit** | Clears activity and closes the app |

Status rows show **VLC HTTP**, **Discord Rich Presence**, **Playback**, **TMDb**, and **Database** information.

Autostart is opt-in. It uses the Windows user Run registry key, macOS LaunchAgents, or Linux's per-user autostart directory and retains your selected VLC and configuration paths.

## Configuration

The defaults should be fine for most people. Use **Settings…** to change them, or edit `discord-vlc-rpc/config.json` beneath VLC's configuration directory. Changes reload automatically; **Refresh playback / metadata** also forces a reload.

| Option | Default | What it controls |
|---|---|---|
| `discord_application_id` | built in | Discord Application ID; keep the included value or supply your own |
| `tmdb_api_key` | empty | Enables TMDb titles, episode data, links, and artwork |
| `tmdb_language` | `en-US` | Language used for TMDb searches and metadata |
| `tmdb_episode_lookup` | `true` | Looks up the exact parsed season and episode |
| `tmdb_local_index` | `true` | Enables local title-index lookup and automatic maintenance |
| `tmdb_index_path` | empty | Optional absolute path for the title database |
| `tmdb_positive_cache_days` | `60` | Days before successful TMDb metadata is refreshed |
| `metadata_cache_path` | empty | Optional absolute path for the metadata cache |
| `large_image` | empty | Fallback large-image asset key |
| `large_text` | `VLC` | Fallback image hover text |
| `small_image_playing` | empty | Playing badge asset key |
| `small_image_paused` | empty | Paused and buffering badge asset key |
| `small_image_idle` | empty | Idle badge asset key |
| `poster_fit` | `contain` | `contain` fits artwork through wsrv.nl; `raw` uses the TMDb image directly |
| `enabled` | `true` | Enables Rich Presence |
| `ignored_paths` | `[]` | JSON array of local files or directories to hide |

Invalid edits leave the last valid runtime settings in use and appear in the status rows. The file stores your TMDb credential in plain text and is created with user-only permissions where supported. The browser settings page runs on localhost and uses a per-run session token and form checks.

### Where files are stored

| OS | Default VLC configuration directory |
|---|---|
| Windows | `%APPDATA%\vlc` |
| Portable Windows | VLC's `portable` directory when detected |
| macOS | `~/Library/Preferences/org.videolan.vlc` |
| Linux | `$XDG_CONFIG_HOME/vlc` or `~/.config/vlc` |

The app stores these paths beneath the selected VLC configuration directory:

| Path | Contents |
|---|---|
| `discord-vlc-rpc/config.json` | App settings |
| `discord-vlc-rpc/tmdb-index/` | Generated movie and TV title indexes |
| `discord-vlc-rpc/metadata-cache/` | Cached TMDb matches |

Startup discovery checks your user's running VLC processes on all three platforms. It follows VLC's `--config` file, Linux's `XDG_CONFIG_HOME` or `HOME`, and macOS's `HOME`. Windows also checks the native `portable` folder and supports absolute launcher-supplied `--config` files. On macOS, the VLC folder is its enclosing `.app` bundle.

For a custom installation or profile, use these command-line options:

| Option | What it selects |
|---|---|
| `--vlc-dir PATH` | VLC's installation folder |
| `--config-dir PATH` | VLC's configuration directory |
| `--config-file FILE` | An exact VLC configuration file, including a name other than `vlcrc` |
| `--headless` | Runs without a tray icon |

Explicit paths override automatic discovery. Multiple distinct running installations or profiles need explicit selection. If process information is inaccessible, normal default paths remain available; custom profiles need explicit flags.

The selected profile stays fixed during a session so settings and caches remain together. If you start VLC with a different profile later, restart the app with that VLC running. HTTP setup refuses to edit a different automatically detected profile.

## Keep private media private

Add files or directories you never want shown in Discord to **Ignored local files or folders** in Settings, or edit `ignored_paths` as a JSON array:

```json
{
  "ignored_paths": [
    "C:/Media/Private",
    "C:/Media/test.mkv"
  ]
}
```

Use absolute paths. On Windows, forward slashes avoid JSON backslash escaping. On Linux or macOS, use paths such as `/home/you/Videos/Private` or `/Users/you/Movies/Private`.

A file entry hides that file. A directory entry includes everything below it, with directory boundaries respected. Windows paths are compared without case sensitivity.

Opening ignored media clears previous Discord activity and skips media parsing and metadata lookup for that file. Network streams are also ignored. Turning off presence or pausing it for the session clears activity immediately.

## What appears in Discord

The app chooses a title in this order:

1. Official TMDb title
2. A usable embedded title reported by VLC
3. A cleaned version of the filename
4. The original filename

For a TV episode, the second line can include the episode number, season total, and title:

```text
Dragon Ball DAIMA
04 of 20: Chatty
```

If the season total is unavailable, this becomes `04: Chatty`. Without verified episode details, the app can show the parsed season and episode, a meaningful chapter title, or the playback state. Episode lookup uses the exact parsed season and episode rather than remapping it to another season.

While a video is playing, Discord receives timestamps based on its position, duration, and playback speed. Timestamps disappear while paused or buffering and are recalculated after playback changes or detectable seeks. Ordinary whole-second VLC sampling jitter does not keep shifting them.

Metadata lookup runs in the background, so the filename or embedded title appears first. Results for an older file cannot replace the current activity. Stopped playback shows an idle VLC activity; an unreachable VLC clears activity.

VLC's HTTP interface may not distinguish buffering from ordinary playback or expose chapter names. Meaningful labels are used when available. With `ffprobe` on `PATH`, optional chapter lookup has a three-second limit and caches results for up to 12 hours across 32 files. File changes invalidate those results; failed probes retry after ten minutes, and cancelled probes are not cached.

## How filename matching works

You do not need to rename a typical media library. These formats are recognized automatically:

```text
Movie.Name.2026.1080p.BluRay.mkv
Show.Name.S02E05.mkv
Show.Name.2x05.mkv
Show Name Season 2 Episode 5.mkv
[Judas] Dragon Ball Daima - S01E04v2.mkv
[SubsPlease] Show - 04 [1080p].mkv
```

The parser removes common source, resolution, codec, audio, bit-depth, and release-group tags while preserving meaningful title text. A parent directory such as `Show Name (2026)` can provide extra title and year context.

TMDb matching considers original, translated, and alternative titles as well as the year. Ambiguous matches keep the filename instead of assigning an uncertain title. Filename parsing is still heuristic; simplifying an unusual filename or parent-directory name can help with a wrong match.

## Optional local TMDb index

The local index helps find exact movie and TV titles alongside regular online TMDb search. It is built from [TMDb's daily ID exports](https://developer.themoviedb.org/docs/daily-id-exports).

This feature is optional. A TMDb key is still needed to retrieve metadata and artwork, and automatic maintenance will not start without one.

With `tmdb_local_index` enabled, the app builds the index in the background from the previous day's movie and TV exports and maintains it daily. A missing, incomplete, or outdated index triggers a rebuild. You can also choose **Build / refresh local title database** from the tray.

The last complete generation remains available while both replacement indexes are built. Playback, cached metadata, and online searches continue during maintenance. A failed build leaves the active index untouched. Titles with more than four matching IDs fall back to online search.

Downloads, decompression, and sorting are handled by the app with bounded buffers and cancellation. Lookups read small sections from disk instead of loading the whole database into memory. Each lookup reuses its index files and read buffers, then closes the files before making network requests.

To disable local lookups and automatic maintenance, turn off **Build and maintain the local title index** in Settings or set:

```json
{
  "tmdb_local_index": false
}
```

**Pause database updates for this session** temporarily pauses index maintenance and cancels an active build without rewriting your saved preference. Generated files are stored in `discord-vlc-rpc/tmdb-index/` unless you set `tmdb_index_path`.

## Cache, requests, and artwork

Metadata is stored in `discord-vlc-rpc/metadata-cache/` beneath the selected VLC configuration directory. Set `metadata_cache_path` to use a different absolute path.

Successful TMDb results are refreshed after 60 days by default, controlled by `tmdb_positive_cache_days`. Missing results retry after an hour; partial matches or artwork fallbacks retry after ten minutes. The disk cache is limited to 4,096 entries. Use **Clear metadata cache** when you want to test matching from scratch.

Requests have bounded deadlines and are cancelled when their media or settings change. Authentication and rate-limit errors appear in status without exposing credentials. A TMDb error leaves basic filename presence available.

With `poster_fit=contain`, the TMDb image URL is sent to [wsrv.nl](https://wsrv.nl/) so portrait artwork fits Discord's square image area. If fitting fails, the raw image is used temporarily. Set `poster_fit=raw` to use TMDb images directly; Discord may crop them. Verified episode stills take priority over the show poster.

The app checks reachable VLC every half-second. Failed checks back off through one, two, and four seconds to a five-second maximum; a successful check restores normal polling. **Refresh playback / metadata** checks immediately, and saving browser settings also wakes the app. Repeated identical Discord activity updates are skipped, with periodic refreshes to keep presence current.

Discord reconnects use increasing retry delays. On macOS and Linux, socket discovery has a one-second overall deadline and tries the last successful socket first when it remains in the current search scope.

## Troubleshooting

| Problem | Things to check |
|---|---|
| Nothing appears in Discord | Start the Discord desktop app, enable activity sharing, and check the presence toggles and status rows |
| VLC HTTP is not ready | Choose **Check / Configure VLC HTTP Interface**, close VLC if prompted, then restart it and check again |
| VLC is running but cannot be reached | Confirm the selected profile and HTTP password; use explicit paths for a custom profile |
| A custom Discord application does not work | Double-check `discord_application_id`; it must contain digits |
| No TMDb titles or artwork | Save a TMDb key or read access token and check the **TMDb** status row |
| The wrong movie or show is selected | Simplify an unusual filename or parent directory, then clear the metadata cache and refresh |
| No episode title | TMDb may not have that exact season and episode, or `tmdb_episode_lookup` may be disabled |
| No `of <total>` text | The season total is unavailable or smaller than the current episode number |
| No chapter title | VLC may not expose names; optional file probing needs `ffprobe` on `PATH` |
| A poster is cropped | Set `poster_fit=contain` |
| You do not want to use wsrv.nl | Set `poster_fit=raw` |
| A network stream or private file shows no activity | Network streams and matching ignored paths deliberately clear presence |
| Discord was restarted | Reconnection is automatic; check the status row if it remains disconnected |
| The local index does not build | Enable it, save a TMDb key, check directory permissions, and inspect the **Database** status |
| Another app instance is already running | Quit it first; only one instance runs for each configuration profile |
| Settings do not take effect | Check for invalid JSON or values, then choose **Refresh playback / metadata** |

## Build and test

Install Go 1.27.1 or newer, then run these commands from the `Source` root:

```sh
go test -race ./...
go vet ./...
go run ./cmd/build ../release-go
```

The source is organized as follows:

| Path | Purpose |
|---|---|
| `main.go` | Executable entry point and embedded tray icon |
| `modules/` | Application modules and their tests |
| `cmd/build/` | Release-package builder |
| `assets/` | Tray icon and documentation logos |
| `.github/workflows/` | GitHub Actions tests and builds |

The builder creates all six Windows, macOS, and Linux packages for x64 and ARM64. macOS packages contain a menu-bar app bundle. The build uses Go with CGO disabled.

The included GitHub workflow tests on all three operating systems and builds all six packages. It does not publish a GitHub release.

Tests use synthetic exports and mock VLC, TMDb, and Discord endpoints. No actual TMDb database is downloaded during development or validation. Live VLC/Discord IPC, tray, and login behavior still need target-desktop validation.

## Credits

Movie and TV metadata and artwork are provided by TMDb. Square artwork fitting uses wsrv.nl when enabled. Dependency notices are included in `THIRD_PARTY_NOTICES.txt` in every release package.

Discord is a trademark of Discord Inc. This project is not affiliated with Discord, VideoLAN, or TMDb. It uses the TMDb API but is not endorsed or certified by TMDb.

## License

[MIT](LICENSE)
