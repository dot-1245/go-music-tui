package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/dot-1245/go-music-tui/internal/player"
	"github.com/dot-1245/go-music-tui/internal/syncprobe"
)

type recording struct {
	PositionSource string               `json:"position_source,omitempty"`
	Observations   []player.Observation `json:"observations"`
	Follow         []player.Snapshot    `json:"follow"`
}

func anonymous(snapshot player.Snapshot) player.Snapshot {
	identity := sha256.Sum256([]byte(snapshot.TrackKey()))
	info := snapshot.Info
	snapshot.Info = player.Info{Name: "local-player", Title: fmt.Sprintf("track-%x", identity[:8]), Status: info.Status, PositionSeconds: info.PositionSeconds, LengthSeconds: info.LengthSeconds}
	return snapshot
}

func main() {
	live := flag.Bool("live", false, "read local MPRIS position without playback controls")
	competition := flag.Bool("competition", false, "run fixed-gate MPRIS-only competition with held-out seeds")
	timers := flag.Bool("timers", false, "compare 50ms ticker and boundary timers on this host")
	name := flag.String("player", "", "MPRIS player name")
	count := flag.Int("samples", 30, "number of live queries")
	stride := flag.Int("sync-every", 1, "feed every Nth query to clocks; score against all queries")
	interval := flag.Duration("interval", time.Second, "live query interval")
	record := flag.String("record", "", "write anonymized observations as JSON (exclusive creation)")
	replay := flag.String("replay", "", "read recorded observations instead of collecting live data")
	jsonOutput := flag.Bool("json", false, "print JSON metrics")
	flag.Parse()
	if *competition {
		if *timers {
			fatal(fmt.Errorf("competition cannot be combined with timers"))
		}
		if !*live && *replay == "" {
			if *record != "" {
				fatal(fmt.Errorf("record requires live"))
			}
			printCompetition(syncprobe.Tournament(), *jsonOutput)
			return
		}
	}
	if *timers {
		if *live || *replay != "" || *record != "" {
			fatal(fmt.Errorf("timers cannot be combined with live/replay/record"))
		}
		printMetrics(syncprobe.CompareTimers(), *jsonOutput)
		return
	}
	if *stride < 1 || *count < 2 || *count > 3600 || *interval < 20*time.Millisecond || (*live && *replay != "") || (*record != "" && !*live) {
		fatal(fmt.Errorf("require 2..3600 samples, interval >=20ms, live or replay, and record only with live"))
	}
	if *replay != "" {
		file, err := os.Open(*replay)
		if err != nil {
			fatal(err)
		}
		defer file.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(file).Decode(&raw); err != nil {
			fatal(err)
		}
		var trace recording
		if len(raw) > 0 && raw[0] == '[' {
			if err := json.Unmarshal(raw, &trace.Observations); err != nil {
				fatal(err)
			}
		} else if err := json.Unmarshal(raw, &trace); err != nil {
			fatal(err)
		}
		metrics := syncprobe.DisagreementEvery(trace.Observations, *stride)
		if *competition {
			metrics = syncprobe.CompetitionReplay(trace.Observations, *stride)
			fmt.Fprintf(os.Stderr, "source=%q; reference is next query at receipt, not audio truth; rate=1 assumed\n", trace.PositionSource)
		}
		if len(trace.Follow) > 0 && !*competition {
			metrics = append(metrics, syncprobe.FollowDisagreement(trace.Observations, trace.Follow))
		}
		printMetrics(metrics, *jsonOutput)
		return
	}
	if !*live {
		printMetrics(syncprobe.Simulate(), *jsonOutput)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*count)*(*interval+time.Second)+5*time.Second)
	defer cancel()
	client := player.New(nil)
	selected, _, err := client.Select(ctx, *name)
	if err != nil {
		fatal(err)
	}
	if selected == "" {
		fatal(fmt.Errorf("no MPRIS player"))
	}
	var observations []player.Observation
	followCtx, stopFollow := context.WithCancel(ctx)
	var followMu sync.Mutex
	var followSnapshots []player.Snapshot
	var followWG sync.WaitGroup
	followWG.Add(1)
	go func() {
		defer followWG.Done()
		for event := range client.Follow(followCtx, selected) {
			if event.Snapshot != nil {
				snapshot := anonymous(*event.Snapshot)
				followMu.Lock()
				followSnapshots = append(followSnapshots, snapshot)
				followMu.Unlock()
			}
		}
	}()
	defer stopFollow()
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for i := 0; i < *count; i++ {
		if i > 0 {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				fatal(ctx.Err())
			}
		}
		var o player.Observation
		if *competition {
			o, err = client.ObserveRawPosition(ctx, selected)
		} else {
			o, err = client.ObservePosition(ctx, selected)
		}
		if err != nil {
			if *competition {
				fmt.Fprintf(os.Stderr, "sample=%d skipped: %v\n", i+1, err)
				continue
			}
			fatal(err)
		}
		info := o.Snapshot.Info
		o.Snapshot = anonymous(o.Snapshot)
		observations = append(observations, o)
		fmt.Fprintf(os.Stderr, "sample=%d state=%s position=%.6f rtt=%.3fms\n", i+1, info.Status, info.PositionSeconds, float64(o.FinishedAt.Sub(o.StartedAt))/float64(time.Millisecond))
	}
	stopFollow()
	followWG.Wait()
	fmt.Fprintf(os.Stderr, "follow snapshots: %d\n", len(followSnapshots))
	if *record != "" {
		file, err := os.OpenFile(*record, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fatal(err)
		}
		source := "playerctl-format-position"
		if *competition {
			source = "playerctl-explicit-position"
		}
		err = json.NewEncoder(file).Encode(recording{PositionSource: source, Observations: observations, Follow: followSnapshots})
		closeErr := file.Close()
		if err != nil {
			fatal(err)
		}
		if closeErr != nil {
			fatal(closeErr)
		}
	}
	metrics := syncprobe.DisagreementEvery(observations, *stride)
	if *competition {
		metrics = syncprobe.CompetitionReplay(observations, *stride)
	} else {
		metrics = append(metrics, syncprobe.FollowDisagreement(observations, followSnapshots))
	}
	printMetrics(metrics, *jsonOutput)
	if *competition {
		fmt.Fprintln(os.Stderr, "Competition live scores are next-query disagreement at receipt, not audio truth. Rate 1 assumed; metadata guards add two commands per observation.")
	} else {
		fmt.Fprintln(os.Stderr, "Live metrics measure consistency against query midpoints, not alignment with audible vocals. Paused tracks do not validate running-clock accuracy.")
	}
}

func printMetrics(metrics []syncprobe.Metrics, jsonOutput bool) {
	if jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(metrics); err != nil {
			fatal(err)
		}
		return
	}
	fmt.Printf("%-19s %-13s %9s %9s %9s %9s %8s %8s %10s\n", "scenario", "strategy", "mean ms", "p95 ms", "max ms", "backsteps", "rejected", "samples", "outside95")
	for _, m := range metrics {
		if m.Samples == 0 {
			fmt.Printf("%-19s %-13s %9s %9s %9s %9s %8d %8d %10s\n", m.Scenario, m.Strategy, "n/a", "n/a", "n/a", "n/a", m.Rejected, 0, "n/a")
			continue
		}
		backsteps := fmt.Sprint(m.Backsteps)
		if m.Scenario == "live-consistency-not-truth" || m.Scenario == "host-wakeup" {
			backsteps = "n/a"
		}
		outside := "n/a"
		if m.Scenario == "live-consistency-not-truth" {
			outside = fmt.Sprintf("%.2f", m.OutsideP95MS)
		}
		fmt.Printf("%-19s %-13s %9.2f %9.2f %9.2f %9s %8d %8d %10s\n", m.Scenario, m.Strategy, m.MeanMS, m.P95MS, m.MaxMS, backsteps, m.Rejected, m.Samples, outside)
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func printCompetition(report syncprobe.CompetitionReport, jsonOutput bool) {
	if jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fatal(err)
		}
		return
	}
	fmt.Println("MPRIS-only competition: fixed 1s baseline; identical transport signals; 90s/scenario; 10ms scoring.")
	fmt.Println("Discovery seeds 1..5; held-out validation seeds 101..105; fixed thresholds, no per-case tuning.")
	fmt.Printf("%-12s %-19s %-16s %8s %8s %9s %8s %10s %8s\n", "split", "scenario", "method", "p95 ms", "p99 ms", "max ms", ">50ms %", "recover ms", "query/m")
	for _, r := range report.Rows {
		fmt.Printf("%-12s %-19s %-16s %8.2f %8.2f %9.2f %8.2f %10.0f %8.2f\n", r.Split, r.Scenario, r.Method, r.P95MS, r.P99MS, r.MaxMS, r.Over50Percent, r.RecoveryMS, r.QueriesPerMinute)
	}
	fmt.Println("\nElimination: <=baseline+5ms p95, +25ms p99, +100ms max/recovery, +5 backsteps, +0.5pp time over50ms, +1 query/min.")
	for _, v := range report.Verdicts {
		status := "eliminated"
		if v.Survives {
			status = "survives"
		}
		fmt.Printf("%s %-16s %s; improved=%d; queries saved=%.1f%%\n", v.Split, v.Method, status, v.ImprovedScenarios, v.SavedQueriesPercent)
		for _, reason := range v.Reasons {
			fmt.Println("  " + reason)
		}
	}
	fmt.Printf("Survivors across both splits: %v\n", report.Survivors)
}
