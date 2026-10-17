# Debug programs

The programs in this directory are small manual probes for the same packages
used by the main TUI. Run them from the repository root:

```bash
go run ./debug/<feature> [flags]
```

They are intentionally separate executables so a provider, parser, image
source, or player-control path can be checked without starting the full UI.
Keep deterministic behavior in the shared `internal/` packages and add tests
there when a debug probe exposes a regression.

## Terminal colors

```bash
go run ./debug/tty-color
go run ./debug/tty-color --mode=always
go run ./debug/tty-color --mode=never
```

The command reports whether stdout is a TTY, the `TERM`/`NO_COLOR` inputs, and
the resulting semantic ANSI palette. `always` overrides `NO_COLOR`; `auto`
disables colors for non-TTY, `dumb`, or `NO_COLOR` environments.
The output includes a karaoke sample, normal/bold comparisons for SGR 39
(default foreground), 37 and 97, and all 16 ANSI palette slots when colors
are enabled. Lyrics use the default foreground, with the current word in
bold; upcoming words use gray.

## Lyrics

### Compare playback clocks and update timers

```bash
go run ./debug/sync-clock
go run ./debug/sync-clock --timers
go run ./debug/sync-clock --live --samples 120 --interval 250ms --record /tmp/music-clock.json
go run ./debug/sync-clock --replay /tmp/music-clock.json --sync-every 4
go run ./debug/sync-clock --competition
go run ./debug/sync-clock --competition --json
go run ./debug/sync-clock --competition --live --samples 80 --interval 250ms --record /tmp/music-position.json
go run ./debug/sync-clock --competition --replay /tmp/music-position.json --sync-every 4
```

The deterministic probe compares five clock strategies across eleven scenarios.
`--timers` compares host wake-up lateness for the current 50ms cadence and
lyric-boundary deadlines. It does not measure terminal paint or audio latency.

`--live` only reads the selected local MPRIS player and its actual follow stream;
it never changes playback or requests lyrics/artwork. Recordings replace track
identity with a hash and omit title, artist, album and artwork URLs. The output
file is created exclusively with owner-only permissions. `--player NAME` pins
the target. `--json` provides machine-readable metrics.

Replay scores predictions **before** feeding the next observation to the clock.
`--sync-every 4` on a 250ms recording feeds clocks one query per second but
validates against every query, without leaking intermediate reference samples.
Paused and track/state-transition samples are excluded from live running-clock
scores. Check `samples`; zero means no usable comparison, not zero error.

Live errors measure consistency against query midpoints, not vocal alignment.
`outside95` is the 95th-percentile distance outside the query's possible sample
interval, in milliseconds. Even that interval assumes an uncached position and
normal playback speed. No method can infer exact audio timing from cached or
quantized player positions alone.

The TUI uses the winning periodic acquisition policy through direct MPRIS DBus;
the alternative estimators and boundary timers remain diagnostic-only. See
[`sync-clock/RESULTS.md`](sync-clock/RESULTS.md) for measured results and limits.

`--competition` adds a baseline-relative elimination tournament: six candidates,
twelve simulated MPRIS scenarios, five discovery seeds and five separate
validation seeds. Poll timing is randomized per seed, and all candidates receive
the same transport signals. Accuracy, recovery, backward steps, coverage and
query budgets are checked per scenario. Constants and elimination gates are
fixed in the source; both splits must pass to survive. This models MPRIS data;
it does not launch a fake DBus service.

Competition live mode uses the explicit `playerctl position` command rather
than the interpolated `{{position}}` format property. Two additional metadata
queries guard track/state identity around each position query; the simulated
query budget counts position requests, not these diagnostic guard commands.
Live replay scores receipt-time prediction against the next reported position,
assumes rate 1, and cannot establish absolute audio alignment. Adaptive policies
are tested only in simulation because they choose their own query times.
Earlier recordings lacking `position_source` are supported but are not equivalent
to the new explicit-position recordings. See
[`sync-clock/COMPETITION.md`](sync-clock/COMPETITION.md) for the tournament result.

### Fetch and inspect providers

```bash
go run ./debug/lyrics-fetch --title "Song" --artist "Artist"
go run ./debug/lyrics-fetch --title "Song" --artist "Artist" -v
go run ./debug/lyrics-fetch --title "Song" --artist "Artist" --provider lrclib --raw
go run ./debug/lyrics-fetch --title "Song" --artist "Artist" --json
```

`--provider` accepts `all`, `lrclib`, `synclrc`, or `amll`. The default output
is compact: each provider gets a result summary and a 256-byte response
preview. `-v` lists every request and expands previews to 4096 bytes.

`--raw` requires one selected provider and writes the complete response body
that best corresponds to its lyrics result. It disables the response-size cap,
so use it only for deliberate diagnostics. Do not paste responses containing
private or identifying metadata into public bug reports without reviewing
them first.

Artist values may use common player separators such as commas, semicolons, or
`feat.`. Use `--raw-artist` when the provider should receive a specific
unparsed artist string. SyncLRC is not queried when no artist is supplied, so a
TUI-equivalent probe should include all available metadata, for example:

```bash
go run ./debug/lyrics-fetch --title "Shelter" \
  --artist "Porter Robinson, Madeon" \
  --raw-artist "Porter Robinson, Madeon" \
  --album "Shelter" --duration 219 --provider all -v
```

`--timeout` bounds the whole command.

Provider results containing only one line that starts within the first second
are treated as missing lyrics; this filters common placeholder responses.

### Render lyric formats

```bash
go run ./debug/lyrics-render --color=never
go run ./debug/lyrics-render --file ./lyrics.lrc --format lrc --position 42
go run ./debug/lyrics-render --file ./lyrics.ttml --format ttml --position 42
go run ./debug/lyrics-render --file ./lyrics.yaml --format lyricsfile --position 42
```

Supported formats are ordinary/enhanced LRC, TTML, and LRCLIB's YAML
Lyricsfile. The command prints the active and next line and highlights the
active word when word timing is available. Use `--max-lines` to change the
display window.

### Check ranking

```bash
go run ./debug/lyrics-rank
go run ./debug/lyrics-rank --title "Song" --artist "Artist" --duration 215
go run ./debug/lyrics-rank --file ./candidates.json
```

Without `--file`, a small built-in candidate set demonstrates the ranking
rules. A supplied file must contain a JSON array of LRCLIB-style candidate
objects, for example:

```json
[
  {
    "trackName": "Song",
    "artistName": "Artist",
    "albumName": "Album",
    "duration": 215,
    "syncedLyrics": "[00:00.00]Lyrics"
  }
]
```

The output shows title/artist/duration matching, search-candidate selection,
and provider-priority selection, including word-synchronized results.

## Artwork

```bash
go run ./debug/artwork-fetch --source ./cover.png
go run ./debug/artwork-fetch --source ./cover.png --out /tmp/cover-copy.png
go run ./debug/artwork-render --source ./cover.png
go run ./debug/artwork-render --source ./cover.png --fullscreen --render
```

Artwork sources may be HTTP(S), `file://`, local paths, or data URLs. The
fetcher enforces byte and pixel limits. `artwork-fetch` only reports metadata
unless `--out` is supplied. `artwork-render` reports placement by default and
writes Kitty graphics escape sequences to stdout only with `--render`; use it
in a Kitty-compatible terminal.

## Player metadata and controls

```bash
go run ./debug/metadata
go run ./debug/metadata --player spotify --json
go run ./debug/controls --action play-pause
go run ./debug/controls --action loop --loop-state Track
go run ./debug/controls --player mpv --action next --execute
```

`metadata` selects a playing player when no name is supplied, then prints the
same fields consumed by the TUI. `--json` is useful for scripts.

`controls` is a dry-run by default: it prints the exact `playerctl` command
without changing playback. `--execute` performs the action and therefore
requires a running player. Supported actions are `play-pause`, `previous`,
`next`, `volume-up`, `volume-down`, `seek-back`, `seek-forward`, `shuffle`, and
`loop`. For a dry-run loop action, `--loop-state` supplies the current state;
for execution, the tool queries the selected player first.

## Troubleshooting

- If the TUI says `No player found`, verify that `playerctl -l` lists an MPRIS
  player and try `go run ./debug/metadata`.
- If only lyrics are needed, use `go run . --noinfo --noart`.
- If colors look wrong, compare `go run ./debug/tty-color --mode=always` and
  `--mode=never`, then inspect the terminal's ANSI color slots.
- If lyric selection is unexpected, run `lyrics-fetch -v` and
  `lyrics-rank` with the same title, artist, album, and duration.
- Use `--debug` on the main TUI and inspect
  `~/.cache/go-music-tui-debug.log` for asynchronous fetch and player events.
