# Playback synchronization experiments — 2026-10-17

These are historical prototype results collected before the clock tournament.
The v0.4.0 TUI now uses periodic direct-MPRIS queries; see
[`COMPETITION.md`](COMPETITION.md) for the selection and production validation.

## Candidates

All polling candidates use the same observations, without future samples:

| Strategy | Position anchor and correction |
| --- | --- |
| receipt | Query completion, with the existing 50ms backward-step suppression |
| midpoint | Query midpoint, immediate correction |
| slew | Midpoint; errors up to 250ms settle over 500ms |
| bounded-slew | Slew, rejecting queries taking more than 250ms |
| interval | Reject slow queries; correct only outside the possible query sample interval, slewing small corrections |

The live comparison also evaluates the former production `playerctl --follow`
stream. The receipt candidate polls, unlike that earlier event-only TUI.

## Deterministic comparison

Run `go run ./debug/sync-clock`. Eleven 70-second scenarios use one query per
second and a 10ms evaluation grid. The truth includes asymmetric sampling,
650ms outliers, constant high RTT, changing asymmetry, coarse positions, clock
rates of 0.995/1.005, seeks, pauses, track changes and same-track repeats.
Query starts and transport transitions do not align with integer seconds.

Selected results (milliseconds):

| Scenario | receipt P95 / max | midpoint P95 / max | interval P95 / max |
| --- | --- | --- | --- |
| Sample near request start | 8 / 8 | 24 / 25 | 4 / 4 |
| Slow outliers | 36 / 585 | 24 / 260 | 4 / 4 |
| Constant 120ms RTT, asymmetric sampling | 108 / 108 | 49 / 49 | 48 / 48 |
| Clock rate 1.005 | 18.70 / 19.90 | 5.70 / 6.15 | 18.85 / 19.90 |
| Integer-second position reports | 233 / 233 | 203 / 203 | 177 / 177 |

There is no universal winner. Interval correction resists jitter/outliers but
tracks genuine rate drift more slowly than midpoint correction. Rejecting every
slow query leaves the bounded strategies uninitialized in the all-slow case;
the probe reports no data, not perfect accuracy. A production implementation
would need a fallback and an uncertainty indicator.

Polling alone also cannot detect transport changes immediately. A repeat can
leave the display tens of seconds away from the new position until the next
query. Mean/max error includes that detection delay. Synthetic backsteps count
only outside a fixed two-second window after transport changes, so delayed
legitimate resets are not classified as steady-play jitter.

## Feishin observations

Captured 120 queries over approximately 30 seconds of playback, then 240 queries
over approximately 60 seconds while also collecting 45 follow snapshots.
Playback was paused during the latter recording. The comparisons below use
172 consecutive playing/same-track samples, excluding the pause and transition
samples. Actual repeat behavior was not captured; repeat tests are synthetic.
Private raw metadata was not retained or committed.

The same reference samples and actual follow stream are used for both replay
budgets. Scores are predicted-position disagreement with query midpoints,
**not error relative to audible vocals**.

| Strategy | P95 at 250ms sync | P95 at 1s sync | Outside sample interval P95 at 1s |
| --- | --- | --- | --- |
| Actual follow | 57.31 | 57.31 | 52.61 |
| receipt polling | 16.85 | 15.18 | 8.65 |
| midpoint | 17.17 | 17.51 | 12.21 |
| slew | 14.14 | 17.31 | 12.21 |
| bounded-slew | 14.14 | 17.31 | 12.21 |
| interval | 13.50 | 15.01 | 10.82 |

One-second replay feeds every fourth query to clocks while scoring against all
queries. Interval and receipt polling are effectively tied at that budget; the
large improvement is periodic observation rather than the precise filter.
The separate 30-second trace also showed improvement over receipt polling at
250ms, but did not establish a decisive filter winner at one second.

Midpoint is not audio ground truth and can favor midpoint-based estimators.
The interval-distance score removes that exact-midpoint assumption, but still
assumes the player sampled an uncached position during the call at rate 1.
Feishin position reports can themselves contain quantization and jitter.

## Update scheduling and rendering

Three concurrent-worker host timer runs, 60 boundaries spaced 37ms apart:

| Update policy | Mean wake-up lateness | P95 lateness |
| --- | --- | --- |
| 50ms ticker | 25.18–25.24ms | 46.11–46.46ms |
| Boundary deadline timer | 0.57–0.60ms | 1.00–1.08ms |

Run `go run ./debug/sync-clock --timers`. These figures measure timer wake-up,
not frame construction, terminal writes, image encoding or paint. Host load
and worker contention can change the results. The new shared boundary helper
builds sorted lyric/word/end events and calculates the next deadline.

Independent no-artwork frame benchmarks on this host measured approximately
47 microseconds for 100 lines of ten words and 67 microseconds for a 100-word
rap line, including incremental row generation. Command:

```bash
go test -run '^$' -bench 'Benchmark.*Frame' -benchmem -benchtime=200ms .
```

This does not explain large delays by CPU text-frame generation alone. Terminal
writes and artwork encoding still require separate measurement.

## Implementation recommendation

1. Use deadline-driven lyric updates instead of a fixed 50ms render cadence.
2. Keep event-driven track/status/seek handling and add periodic position
   observations around once per second in a separate worker.
3. Start conservatively with bounded corrections; retain interval filtering as
   a candidate rather than treating a small live-score advantage as conclusive.
4. Guard observations with a playback generation so old queries cannot override
   a track change, seek, player switch or repeat. Reject overlapping old queries.
5. Integrate clock corrections with deadline invalidation and account for
   correction speed. A timer based only on rate 1 would be wrong while slewing.
6. Add slow-query fallback, explicit playback-rate handling, real repeat tests,
   output-latency measurement and per-recording lyric offsets before claiming
   end-to-end synchronization.

Exact zero error cannot be guaranteed without a shared audio clock and reliable
lyric timestamps. These experiments identify avoidable errors without claiming
that polling or midpoint estimation provides such a clock.
