# MPRIS-only clock tournament — 2026-10-17

The incumbent **once-per-second position query plus Rate-aware interpolation**
survived. None of the five challengers passed every non-regression gate in both
the discovery and validation sets. This is a result for the implemented
algorithms and conditions, not a proof that no better estimator exists.

The v0.4.0 production TUI implements the incumbent using a persistent DBus
connection, one-second property queries and signal-driven generation guards.
Alternative estimators and boundary-driven scheduling remain experimental.

## Reproduce

```bash
go run ./debug/sync-clock --competition
go run ./debug/sync-clock --competition --json
```

This is a deterministic simulation of MPRIS information, not a fake DBus server
or a measurement of terminal/audio latency. It uses six candidates, twelve
scenarios, ten seeds and 90 seconds per run: 720 runs, representing 18 hours of
playback. Time advances virtually in 10ms increments. Seeds 1–5 are discovery;
101–105 are a separate validation set. No per-scenario parameter tuning is used.

## Candidates

| Candidate | Correction | Polling |
| --- | --- | --- |
| periodic | Immediately accept reported position at receipt | Every second |
| gentle | Settle errors <=250ms over 500ms; accept larger changes immediately | Every second |
| median-3 | Median of three observations projected using Rate, with small-error settling | Every second |
| pll | Phase gain 0.35, frequency gain 0.02, frequency correction limited to ±0.01 | Every second |
| adaptive-periodic | Same as periodic | 250ms–2s based on prior observed residuals |
| adaptive-gentle | Same as gentle | Same adaptive schedule |

Adaptive policies have a one-request-per-second token budget, with a maximum
burst of two. Requests, including failures, count toward the budget. There is
at most one query in flight. The fixed candidates share request timing within
each run; adaptive candidates query the same underlying deterministic source
at their own request times. Query startup phase varies across seeds to avoid
accidentally favoring integer-second or cached position samples.

All methods get identical available MPRIS signal payloads with 20ms delivery
delay. Seeked carries its new position. Track changes start at zero. Playback
status and Rate events do not get an oracle position: each candidate projects
its own clock before applying the new state or rate. Signals invalidate pending
queries by a client-assigned generation. Missing signals are not reconstructed
from ground truth.

## Conditions and gates

Scenarios cover fresh positions, ±30ms noise, occasional 650ms queries, isolated
800ms position outliers, cached positions updated every 250/800ms, integer-second
positions, seeks/pause/resume/track changes/repeats, missing signals, query
timeouts, frozen position reports and playback rates 0.5/1.5.

Scoring includes paused periods, transitions and query failures after a common
one-second startup window. Recovery means <=50ms error sustained for 100ms.
Backsteps are measured outside a fixed two-second transport window. Exact
ground truth is available only to the scorer and source simulator.

A challenger is eliminated if **any** scenario exceeds the incumbent by:

- 5ms at P95, 25ms at P99, or 100ms maximum error;
- 100ms worst recovery, or more failed recoveries;
- five backward steps;
- 0.5 percentage points of time spent more than 50ms wrong;
- one position query per minute, or lower coverage by more than 0.01pp.

It must also improve P95 by at least 5ms in a scenario or save at least 10% of
queries overall. Both discovery and validation must pass. These are explicit
engineering tolerances, not statistical confidence intervals.

## Results

Selected **validation** errors, in milliseconds:

| Condition / metric | periodic | gentle | median-3 | pll |
| --- | --- | --- | --- | --- |
| Fresh position P95 | 14.51 | 13.44 | 11.03 | 9.00 |
| Noisy position P95 | 34.11 | 32.79 | 28.76 | 21.89 |
| Slow-response P95 | 17.34 | 21.18 | 13.48 | 49.42 |
| Missing-signal worst recovery | 900 | 900 | 1900 | 900 |

- Gentle improved some distributions and removed small backsteps, but extended
  the time above 50ms error for cached positions past the allowed tolerance.
- Median strongly reduced isolated outliers, but delayed an unannounced seek
  or repeat by another poll: worst recovery increased from 900ms to 1900ms.
- PLL improved six validation scenarios, but worsened slow-response P95 and
  backward-step counts with cached positions.
- Adaptive-periodic saved about 23.46% of queries overall, but worst missing-
  signal recovery rose to 1590ms and slow-response P95 rose to about 170ms.
- Adaptive-gentle saved about 23.33%, but also failed recovery and stale/coarse
  position conditions.

All challengers were eliminated in both splits. The incumbent is not accurate
when the player returns stale, rounded or frozen positions: it merely avoided
the challengers' additional regressions under these gates. Large maximum errors
during seeks/repeats include time before the event or next query reveals the
new position, rather than silently excluding that interval.

## Live cross-check

An additional 20-second Feishin recording captured 80 explicit position queries
while playing and 20 follow snapshots. Identity and artwork URLs were removed;
the recording is local temporary data, not committed.

Replaying every fourth observation as estimator input approximates one query
per second. All 79 subsequent query observations remain scoring references and
are evaluated before they are fed to an estimator:

| Method | Mean disagreement | P95 disagreement |
| --- | --- | --- |
| periodic | 48.24ms | 197.65ms |
| gentle | 50.49ms | 197.63ms |
| median-3 | 59.15ms | 236.92ms |
| pll | 61.31ms | 168.37ms |

PLL has a lower P95 here but a worse mean. These are next-query consistency
scores, **not alignment with audible vocals**. Explicit position queries
returned repeated and unevenly advancing values; polling does not make those
values independent audio ground truth. Startup is included except for the
first uninitialized sample. The short recording did not include a repeat.

This live probe uses three playerctl commands per observation: metadata,
explicit position, metadata. Only the position call is timestamp-bracketed;
the guards add overhead. A production DBus implementation should use its cached
identity/state and a generation guard rather than copying this diagnostic cost.
Live replay assumes normal speed because old recording metadata lacks Rate.

Earlier recordings using `{{position}}` can still be replayed, but they use
playerctl's interpolation and must not be compared as if they were the same
measurement source. Nor is an RTT interval guaranteed to bound player-side
cached positions; the new tournament explicitly tests that failure.

## Decision

Keep fixed periodic acquisition as the incumbent. The evaluated smoothing,
median, PLL and adaptive policies are not selected for production. Boundary-
driven rendering remains a separate improvement compatible with the incumbent.
Further challengers must pass the same per-scenario gates, including stale
position and missing-signal recovery, rather than winning only a clean-playback
average.

The implemented monitor passed private-DBus integration tests for periodic
queries, pause, seek, delayed stale replies, owner recovery and cancellation,
including three consecutive race-enabled runs. The built TUI was also started
against real Feishin in a PTY; metadata, R/F handling and clean exit worked.
Feishin was paused during that final smoke check, so this is not evidence of
audible karaoke alignment or a broad real-player compatibility matrix.
