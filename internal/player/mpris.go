package player

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	mprisPrefix          = "org.mpris.MediaPlayer2."
	mprisInterface       = "org.mpris.MediaPlayer2.Player"
	mprisPath            = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	propertiesInterface  = "org.freedesktop.DBus.Properties"
	positionPollInterval = time.Second
)

func mprisText(values map[string]dbus.Variant, key string) string {
	switch v := values[key].Value().(type) {
	case string:
		return v
	case dbus.ObjectPath:
		return string(v)
	}
	return ""
}

func applyMPRISProperties(info Info, properties map[string]dbus.Variant) Info {
	if value, ok := properties["Metadata"]; ok {
		metadata, _ := value.Value().(map[string]dbus.Variant)
		info.TrackID = mprisText(metadata, "mpris:trackid")
		info.Title = mprisText(metadata, "xesam:title")
		info.Album = mprisText(metadata, "xesam:album")
		info.ArtUrl = mprisText(metadata, "mpris:artUrl")
		info.Artists = nil
		switch artists := metadata["xesam:artist"].Value().(type) {
		case []string:
			info.Artists = FlattenArtists(artists)
		case string:
			info.Artists = SplitArtistsFallback(artists)
		}
		info.Artist = strings.Join(info.Artists, ", ")
		info.LengthSeconds = 0
		info.Length = 0
		if length, ok := metadata["mpris:length"].Value().(int64); ok && length > 0 {
			info.LengthSeconds = float64(length) / 1e6
			info.Length = int(info.LengthSeconds)
		}
	}
	if value, ok := properties["PlaybackStatus"]; ok {
		if status, ok := value.Value().(string); ok {
			info.Status = status
		}
	}
	if value, ok := properties["Rate"]; ok {
		if rate, ok := value.Value().(float64); ok && rate != 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0) {
			info.Rate = rate
		}
	}
	if info.Rate == 0 {
		info.Rate = 1
	}
	if value, ok := properties["Volume"]; ok {
		if volume, ok := value.Value().(float64); ok && !math.IsNaN(volume) && !math.IsInf(volume, 0) {
			info.Volume = int(math.Max(0, math.Min(100, volume*100)))
		}
	}
	if value, ok := properties["Shuffle"]; ok {
		if shuffle, ok := value.Value().(bool); ok {
			info.Shuffle = fmt.Sprint(shuffle)
		}
	}
	if _, ok := properties["LoopStatus"]; ok {
		info.Loop = mprisText(properties, "LoopStatus")
	}
	return info
}

func readMPRISSnapshot(ctx context.Context, conn *dbus.Conn, name string) (Snapshot, error) {
	var properties map[string]dbus.Variant
	object := conn.Object(mprisPrefix+name, mprisPath)
	if err := object.CallWithContext(ctx, propertiesInterface+".GetAll", 0, mprisInterface).Store(&properties); err != nil {
		return Snapshot{}, err
	}
	// Some players omit Position from GetAll even though an explicit Get works.
	if _, ok := properties["Position"]; !ok {
		var position dbus.Variant
		if err := object.CallWithContext(ctx, propertiesInterface+".Get", 0, mprisInterface, "Position").Store(&position); err != nil {
			return Snapshot{}, err
		}
		properties["Position"] = position
	}
	position, ok := properties["Position"].Value().(int64)
	if !ok || position < 0 {
		return Snapshot{}, fmt.Errorf("MPRIS returned an invalid Position")
	}
	info := applyMPRISProperties(Info{Name: name, Rate: 1}, properties)
	if info.Status != "Playing" && info.Status != "Paused" && info.Status != "Stopped" {
		return Snapshot{}, fmt.Errorf("MPRIS returned an invalid PlaybackStatus")
	}
	info.PositionSeconds = float64(position) / 1e6
	info.Position = int(info.PositionSeconds)
	return Snapshot{Info: info, ReceivedAt: time.Now()}, nil
}

type mprisState struct {
	snapshot   Snapshot
	ready      bool
	generation uint64
}

func (s *mprisState) properties(changed map[string]dbus.Variant, invalidated []string, now time.Time) (changedState, query bool) {
	for _, key := range invalidated {
		if key == "Position" || key == "Metadata" || key == "PlaybackStatus" || key == "Rate" {
			s.generation++
			query = true
		}
	}
	if !s.ready {
		s.generation++
		return false, true
	}
	previous := s.snapshot
	info := applyMPRISProperties(previous.Info, changed)
	newTrack := info.TrackID != previous.Info.TrackID || !sameTrack(info, previous.Info)
	clockChanged := newTrack || info.Status != previous.Info.Status || info.Rate != previous.Info.Rate
	if clockChanged {
		info.PositionSeconds = previous.PositionAt(now)
		if newTrack {
			info.PositionSeconds = 0
		}
		info.Position = int(info.PositionSeconds)
		s.snapshot.ReceivedAt = now
		s.generation++
		query = true
	}
	if value, ok := changed["Position"]; ok {
		if position, ok := value.Value().(int64); ok && position >= 0 {
			info.PositionSeconds = float64(position) / 1e6
			info.Position = int(info.PositionSeconds)
			s.snapshot.ReceivedAt = now
			s.generation++
			query = true
		}
	}
	s.snapshot.Info = info
	return len(changed) > 0, query
}

func (s *mprisState) seek(position int64, now time.Time) bool {
	s.generation++
	if !s.ready || position < 0 {
		return false
	}
	s.snapshot.Info.PositionSeconds = float64(position) / 1e6
	s.snapshot.Info.Position = int(s.snapshot.Info.PositionSeconds)
	s.snapshot.ReceivedAt = now
	return true
}

type mprisReply struct {
	snapshot   Snapshot
	generation uint64
	err        error
}

func (s *mprisState) accept(reply mprisReply) bool {
	if reply.err != nil || reply.generation != s.generation {
		return false
	}
	s.snapshot = reply.snapshot
	s.ready = true
	return true
}

func selectMPRIS(ctx context.Context, conn *dbus.Conn, selector string) (string, error) {
	selector = strings.TrimPrefix(strings.TrimSpace(selector), mprisPrefix)
	if selector != "" && selector != "%any" {
		return selector, nil
	}
	var names []string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return "", err
	}
	sort.Strings(names)
	first := ""
	for _, service := range names {
		if strings.HasPrefix(service, mprisPrefix) {
			name := strings.TrimPrefix(service, mprisPrefix)
			if first == "" {
				first = name
			}
			snapshot, err := readMPRISSnapshot(ctx, conn, name)
			if err == nil && snapshot.Info.Status == "Playing" {
				return name, nil
			}
		}
	}
	if first == "" {
		return "", fmt.Errorf("no MPRIS player found")
	}
	return first, nil
}

// WatchMPRIS owns one persistent DBus connection. Queries run off the event loop;
// replies are stamped at receipt and superseded by newer transport generations.
// Follow remains available for the playerctl diagnostic and fallback comparisons.
func (c *Client) WatchMPRIS(ctx context.Context, selector string) <-chan FollowEvent {
	events := make(chan FollowEvent, 8)
	go func() {
		defer close(events)
		for ctx.Err() == nil {
			err := c.watchMPRIS(ctx, selector, events)
			if ctx.Err() != nil {
				return
			}
			sendFollowEvent(ctx, events, FollowEvent{Err: err, Stopped: true})
			if !waitFollow(ctx, 500*time.Millisecond) {
				return
			}
		}
	}()
	return events
}

func (c *Client) watchMPRIS(ctx context.Context, selector string, events chan<- FollowEvent) error {
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	selectionCtx, cancelSelection := c.commandContext(ctx)
	name, err := selectMPRIS(selectionCtx, conn, selector)
	cancelSelection()
	if err != nil {
		return err
	}
	signals := make(chan *dbus.Signal, 64)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	for _, options := range [][]dbus.MatchOption{
		{dbus.WithMatchSender(mprisPrefix + name), dbus.WithMatchObjectPath(mprisPath), dbus.WithMatchInterface(mprisInterface), dbus.WithMatchMember("Seeked")},
		{dbus.WithMatchSender(mprisPrefix + name), dbus.WithMatchObjectPath(mprisPath), dbus.WithMatchInterface(propertiesInterface), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, mprisInterface)},
		{dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, mprisPrefix+name)},
	} {
		matchCtx, cancel := c.commandContext(ctx)
		err := conn.AddMatchSignalContext(matchCtx, options...)
		cancel()
		if err != nil {
			return err
		}
	}
	sendFollowEvent(ctx, events, FollowEvent{Started: true})
	var state mprisState
	replies := make(chan mprisReply, 1)
	queryRunning := false
	queryWanted := true
	queryCancel := func() {}
	defer func() { queryCancel() }()
	ticker := time.NewTicker(positionPollInterval)
	defer ticker.Stop()
	applySignal := func(signal *dbus.Signal) {
		if signal == nil {
			return
		}
		now := time.Now()
		emit, want := false, false
		switch signal.Name {
		case mprisInterface + ".Seeked":
			if len(signal.Body) == 1 {
				if position, ok := signal.Body[0].(int64); ok {
					emit = state.seek(position, now)
					want = true
				}
			}
		case propertiesInterface + ".PropertiesChanged":
			if len(signal.Body) == 3 {
				changed, ok := signal.Body[1].(map[string]dbus.Variant)
				invalidated, _ := signal.Body[2].([]string)
				if ok {
					emit, want = state.properties(changed, invalidated, now)
				}
			}
		case "org.freedesktop.DBus.NameOwnerChanged":
			state.generation++
			state.ready = false
			want = true
			sendFollowEvent(ctx, events, FollowEvent{Stopped: true})
		}
		if want {
			queryWanted = true
			queryCancel()
		}
		if emit {
			snapshot := state.snapshot
			sendFollowEvent(ctx, events, FollowEvent{Snapshot: &snapshot})
		}
	}
	for {
		// Drain already queued signals before accepting or starting a query.
		for draining := true; draining; {
			select {
			case signal := <-signals:
				applySignal(signal)
			default:
				draining = false
			}
		}
		if queryWanted && !queryRunning {
			queryRunning = true
			queryWanted = false
			generation := state.generation
			queryCtx, cancel := c.commandContext(ctx)
			queryCancel = cancel
			go func() {
				snapshot, err := readMPRISSnapshot(queryCtx, conn, name)
				cancel()
				reply := mprisReply{snapshot: snapshot, generation: generation, err: err}
				select {
				case replies <- reply:
				case <-ctx.Done():
				}
			}()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-conn.Context().Done():
			return fmt.Errorf("MPRIS bus disconnected")
		case signal := <-signals:
			applySignal(signal)
		case <-ticker.C:
			queryWanted = true
		case reply := <-replies:
			queryRunning = false
			for draining := true; draining; {
				select {
				case signal := <-signals:
					applySignal(signal)
				default:
					draining = false
				}
			}
			if reply.generation != state.generation {
				continue
			}
			if reply.err != nil {
				sendFollowEvent(ctx, events, FollowEvent{Err: reply.err})
				if !conn.Connected() {
					return reply.err
				}
				continue
			}
			if state.accept(reply) {
				snapshot := state.snapshot
				sendFollowEvent(ctx, events, FollowEvent{Snapshot: &snapshot})
			}
		}
	}
}
