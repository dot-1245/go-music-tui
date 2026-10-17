# Changelog

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
