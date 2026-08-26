package player

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// FollowEvent is either a fresh player snapshot or a recoverable monitor
// error. Follow keeps retrying after the playerctl process exits.
type FollowEvent struct {
	Snapshot *Snapshot
	Err      error
	Started  bool
	Stopped  bool
}

// Follow starts one long-lived playerctl --follow process. The special %any
// selector follows the most recently updated MPRIS player and the emitted
// playerName keeps the selected player explicit for controls.
func (c *Client) Follow(ctx context.Context, playerName string) <-chan FollowEvent {
	events := make(chan FollowEvent, 8)
	go c.follow(ctx, playerName, events)
	return events
}

func (c *Client) follow(ctx context.Context, playerName string, events chan<- FollowEvent) {
	defer close(events)
	command := c.executable()
	if command == "" {
		sendFollowEvent(ctx, events, FollowEvent{Err: fmt.Errorf("playerctl follow requires an ExecRunner")})
		return
	}
	selector := strings.TrimSpace(playerName)
	if selector == "" {
		selector = "%any"
	}

	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		args := []string{
			"-p", selector,
			"--follow",
			"--format", metadataStreamFormat,
			"metadata",
		}
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.Stderr = io.Discard
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			sendFollowEvent(ctx, events, FollowEvent{Err: err})
			if !waitFollow(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		if err := cmd.Start(); err != nil {
			sendFollowEvent(ctx, events, FollowEvent{Err: err})
			if !waitFollow(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		sendFollowEvent(ctx, events, FollowEvent{Started: true})

		reader := bufio.NewReaderSize(stdout, 64*1024)
		for {
			record, readErr := reader.ReadString(metadataRecordSeparator[0])
			if len(record) > 0 {
				record = strings.TrimSuffix(record, metadataRecordSeparator)
				record = strings.TrimSuffix(record, "\n")
				if strings.TrimSpace(record) != "" {
					info, parseErr := ParseMetadata(record, selector)
					if parseErr != nil {
						sendFollowEvent(ctx, events, FollowEvent{Err: parseErr})
					} else {
						snapshot := Snapshot{Info: info, ReceivedAt: time.Now()}
						sendFollowEvent(ctx, events, FollowEvent{Snapshot: &snapshot})
					}
				}
			}
			if readErr != nil {
				break
			}
		}
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		if waitErr != nil {
			sendFollowEvent(ctx, events, FollowEvent{Err: waitErr, Stopped: true})
		} else {
			sendFollowEvent(ctx, events, FollowEvent{Err: fmt.Errorf("playerctl follow exited"), Stopped: true})
		}
		if !waitFollow(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func sendFollowEvent(ctx context.Context, events chan<- FollowEvent, event FollowEvent) {
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

func waitFollow(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func nextBackoff(current time.Duration) time.Duration {
	if current >= 2*time.Second {
		return 2 * time.Second
	}
	return current * 2
}
