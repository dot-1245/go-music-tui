<img width="48%" alt="go-music-tui showing karaoke lyrics" src=".github/images/screenshot1.png" />
<img width="48%" alt="go-music-tui showing album art" src=".github/images/screenshot2.png" />

# go-music-tui

A terminal music viewer with live lyrics, karaoke timing, and Kitty album art.
It reads playback state from MPRIS through `playerctl` and keeps the UI
responsive while network-backed features load in the background.

> [!NOTE]
> This project is developed with AI-assisted tooling. Please review changes
> and test them against a real MPRIS player before relying on them.

## Features

- Live lyrics from LRCLIB, SyncLRC, and AMLL
- Line-synchronized and word-synchronized karaoke lyrics
- Kitty graphics album art
- ANSI terminal-native colors that follow the active terminal palette
- CJK-aware lyric and metadata rendering
- Optional information, lyrics, and album-art-only modes
- Feature-specific diagnostic programs under [`debug/`](debug/)

## Requirements

- Linux/Unix environment with [`playerctl`](https://github.com/altdesktop/playerctl)
  and an MPRIS-compatible music player
- Go 1.26.1 or newer
- A terminal with Kitty graphics support for album art (Kitty is recommended)
- A Nerd Font for the status icons
- Network access to the lyric providers for live lyrics

macOS is not supported by the current playerctl and terminal-size
implementation.

## Installation

### Install with Go

```bash
go install github.com/dot-1245/go-music-tui@latest
```

To try the v0.4 release candidate explicitly:

```bash
go install github.com/dot-1245/go-music-tui@v0.4.0-rc.1
```

Prereleases are not selected by `@latest` when a stable release is available.

### Build from source

```bash
git clone https://github.com/dot-1245/go-music-tui.git
cd go-music-tui
go build
./go-music-tui
```

To try the current checkout without building:

```bash
go run .
```

## Usage

The main program accepts these flags:

| Flag | Description |
| --- | --- |
| `--noinfo` | Hide metadata and the progress/volume bars |
| `--nolyrics` | Hide lyrics and skip lyric requests |
| `--noart` | Hide album art and skip artwork requests |
| `--color=auto` | Enable colors only when stdout is a suitable terminal (default) |
| `--color=always` | Force ANSI colors |
| `--color=never` | Disable ANSI colors |
| `--player NAME` | Select the initial player instead of choosing one automatically |
| `--debug` | Append lyric/artwork/player diagnostics to `~/.cache/go-music-tui-debug.log` |

`--noinfo --nolyrics --noart` is rejected because it would leave nothing to
display. `NO_COLOR` disables colors in `auto` mode.

### Keyboard controls

| Key | Action |
| --- | --- |
| `Space` | Play/pause |
| `q` / `e` | Previous / next track |
| `w` / `s` | Increase / decrease volume by 5% |
| `a` / `d` | Seek backward / forward by 5 seconds |
| `z` | Toggle shuffle |
| `x` | Cycle loop mode: none → track → playlist → none |
| `F` / `f` | Cycle through the available MPRIS players |
| `R` / `r` | Reset lyric/artwork/player state and redraw the entire screen |
| `Esc` / `Ctrl-C` | Quit |

## Runtime design

Playback monitoring is event-driven:

```text
MPRIS player
    │ DBus events
    ▼
one long-lived playerctl --follow process
    │ framed metadata records
    ▼
player.Follow → local position interpolation → frame buffer
                                      ├─ lyricsState → lyric providers
                                      └─ artworkState → cache → Kitty
```

The renderer does not start `playerctl` on every frame. A single
`playerctl --follow` process waits for MPRIS changes, while the current
position is interpolated locally between notifications. Lyrics and artwork
requests are cancellable when the track changes. Text output replaces only
changed rows and clears unused continuation rows, avoiding a full text-panel
wipe before each update. Artwork encoding and terminal writes still run on
the rendering path and can delay updates.

The renderer checks playback every 50ms. Position recovery requests run in
the background at the estimated track end, at most once every two seconds.
Regular mid-track clock synchronization and word-boundary scheduling are not
implemented yet.

SyncLRC results wait for provider lookups to complete and require a compatible
LRCLIB result (or retained ordinary lyrics during refresh). Content matching
ignores punctuation and whitespace and tolerates different line breaks.

When no player is specified, the TUI chooses a currently playing player (or
the first available player) and pins the follow stream to that name. Press
`F`/`f` to switch to the next available MPRIS player. `--player NAME` selects
the initial player explicitly; it can still be changed interactively.

## Terminal colors

Colors use the terminal's normal ANSI 16-color slots rather than RGB values
from matugen or a generated file. This lets Kitty, foot, Alacritty, tmux, and
other terminals provide the actual palette. The semantic roles are primary,
accent, secondary text, and gray text. Body text and sung lyrics use the
terminal's default foreground (SGR 39); the current karaoke word is shown in
bold, while upcoming words and surrounding lines use gray.

Explicit line end times hide lyrics during gaps. In the normal view, gaps of
up to 250ms retain the preceding lyric without bold emphasis; longer gaps
hide it in both normal and lyrics-only views. Ordinary
LRC without explicit ends uses the next line's start as the current line end.
Playback corrections exceeding 50ms follow the player clock.

Use `--color=never` when redirecting output or when escape sequences are not
desired. `go run ./debug/tty-color` prints the mode, palette decision, a karaoke
sample, and comparisons of the default foreground and ANSI 16 colors.

## Debug tools

Feature-specific manual checks are available under [`debug/`](debug/). The
full command reference is in [`debug/README.md`](debug/README.md).

## Development

Run the shared tests and static checks from the repository root:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Keep reusable parsing, fetching, ranking, rendering, and player-boundary code
under `internal/`. Debug programs should call those packages instead of
copying production logic, so manual checks and the main TUI stay in sync.

## Known limitations

- Album art uses the Kitty graphics protocol; text rendering still works in
  terminals without Kitty graphics when `--noart` is used.
- Lyrics depend on third-party services and may be unavailable, rate-limited,
  or incomplete for a track.
- Providers can return a different collaboration or version with similar
  lyrics, even when their response metadata matches the requested track.
  Introductory-credit validation and a lyrics blacklist are not implemented.
- Karaoke timing can drift or jump when player notifications are delayed.
  The v0.4 release candidate includes experimental timing and rendering changes;
  it does not yet implement periodic mid-track position correction. Lyric
  timestamps themselves may also differ from the playing recording.
- Local lyric files and custom lyric API selection remain future work; the
  parsers are already exercised by `debug/lyrics-render`.

## Todo

- [ ] Customizable key configuration
- [ ] Clickable UI
- [ ] Local-lyrics mode
- [ ] Custom lyrics API
- [ ] Move into a named TUI platform
- [ ] AUR support

## License

[GPL v3.0](LICENSE)
