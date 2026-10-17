# Changelog

## v0.4.0

Stable release of the v0.4 changes listed below, with periodic MPRIS clock
synchronization replacing the event-only playback monitor.

- Keep one persistent DBus connection and read playback properties every second,
  including while paused; no playerctl subprocess is needed for each update.
- Interpolate position with the MPRIS playback Rate and a monotonic clock.
- Handle Seeked, status, Rate, track and owner-change signals immediately;
  invalidate stale in-flight replies after transport changes.
- Recover missing mid-track seek/repeat notifications through periodic queries.
- Fall back to an explicit Position read when GetAll omits it, and reconnect
  after a player or session bus disconnects.
- Add reproducible clock competitions, recording/replay probes, timer comparisons
  and frame benchmarks under `debug/sync-clock` and the shared internal packages.
- Add private-DBus integration tests for polling, seek, pause, stale responses,
  owner recovery and cancellation.

The fixed one-second acquisition policy survived all configured non-regression
gates in both simulated competition splits. This is not a guarantee of exact
audio alignment: cached player positions, terminal output and lyric timestamps
remain sources of error. The renderer retains its 50ms cadence; word-boundary
scheduling, introductory-credit validation and lyric blacklists remain future work.

## v0.4.0-rc.1

This prerelease collects changes since v0.3.0. Karaoke synchronization remains
experimental; delayed player notifications can still cause drift or jumps.

- Select another MPRIS player with `F`/`f`.
- Reset lyrics, artwork, player monitoring and screen state with `R`/`r`.
- Retry lyric lookups and retain existing lyrics during unsuccessful refreshes.
- Validate SyncLRC lyrics against an LRCLIB backbone and reject introductory
  single-line placeholders. Similar alternate versions can still pass validation.
- Match lyric content across differences in punctuation and line breaks.
- Honor explicit lyric end times, retaining short gaps without bold emphasis.
- Use the terminal's default foreground for body text and sung lyrics, with
  bold for the active word and gray for upcoming words.
- Compare ANSI palette slots and foreground styles with `debug/tty-color`.
- Update changed terminal rows and remove stale wrapped lines without first
  clearing the entire text panel.
- Improve same-track repeat recovery and stale-event handling; reduce the
  backward-position smoothing threshold to 50ms.
- Remove the keyboard footer; controls remain documented in the README.

Periodic mid-track synchronization, word-boundary scheduling, introductory
credit validation and user-managed lyric blacklists remain unimplemented.
