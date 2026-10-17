package player

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// This test uses an isolated bus and is opt-in because some sandboxed runners
// prohibit the local Unix socket created by dbus-daemon.
func TestWatchMPRISPrivateBus(t *testing.T) {
	if os.Getenv("MUSIC_TUI_DBUS_INTEGRATION") != "1" {
		t.Skip("set MUSIC_TUI_DBUS_INTEGRATION=1 to run private DBus integration")
	}
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is unavailable")
	}
	command := exec.Command(daemon, "--session", "--nofork", "--print-address=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	addressCh := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); addressCh <- strings.TrimSpace(line) }()
	var address string
	select {
	case address = <-addressCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for private bus address")
	}
	if address == "" {
		t.Skip("dbus-daemon could not create its private socket in this environment")
	}
	oldAddress, hadAddress := os.LookupEnv("DBUS_SESSION_BUS_ADDRESS")
	if err := os.Setenv("DBUS_SESSION_BUS_ADDRESS", address); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadAddress {
			_ = os.Setenv("DBUS_SESSION_BUS_ADDRESS", oldAddress)
		} else {
			_ = os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	player := &mprisTestService{properties: testMPRISProperties("Playing", int64(2_000_000))}
	if err := server.Export(player, mprisPath, propertiesInterface); err != nil {
		t.Fatal(err)
	}
	if err := server.Export(player, mprisPath, mprisInterface); err != nil {
		t.Fatal(err)
	}
	if reply, err := server.RequestName(mprisPrefix+"codex_test", dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName = %v, %v", reply, err)
	}

	events := New(nil).WatchMPRIS(ctx, "codex_test")
	waitSnapshot := func(check func(Snapshot) bool) Snapshot {
		t.Helper()
		timer := time.NewTimer(4 * time.Second)
		defer timer.Stop()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					t.Fatal("watcher closed unexpectedly")
				}
				if event.Snapshot != nil && check(*event.Snapshot) {
					return *event.Snapshot
				}
			case <-timer.C:
				t.Fatal("timed out waiting for matching MPRIS snapshot")
			}
		}
	}
	waitStopped := func() {
		t.Helper()
		timer := time.NewTimer(4 * time.Second)
		defer timer.Stop()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					t.Fatal("watcher closed unexpectedly")
				}
				if event.Stopped {
					return
				}
			case <-timer.C:
				t.Fatal("timed out waiting for owner-loss notification")
			}
		}
	}
	waitSnapshot(func(s Snapshot) bool { return s.Info.Status == "Playing" && s.Info.Title == "Integration track" })
	if player.getCalls() == 0 {
		t.Fatal("watcher did not call Get for Position omitted from GetAll")
	}

	// No property signal is sent here: the one-second snapshot poll must notice
	// the changed position through the explicit Properties.Get fallback.
	player.update("Position", variant(int64(6_000_000)))
	waitSnapshot(func(s Snapshot) bool { return s.Info.PositionSeconds >= 6 })

	player.update("PlaybackStatus", variant("Paused"))
	if err := server.Emit(mprisPath, propertiesInterface+".PropertiesChanged", mprisInterface, map[string]dbus.Variant{"PlaybackStatus": variant("Paused")}, []string{}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(func(s Snapshot) bool { return s.Info.Status == "Paused" })

	if err := server.Emit(mprisPath, mprisInterface+".Seeked", int64(9_000_000)); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(func(s Snapshot) bool { return s.Info.PositionSeconds >= 9 })

	// Hold one asynchronous GetAll reply after it captures Paused. A subsequent
	// Seeked signal cancels that query and advances its generation; once the old
	// reply is released, the fresh Playing state must be the accepted snapshot.
	delayedStarted := make(chan struct{}, 1)
	delayedRelease := make(chan struct{})
	player.delayNextGetAll(delayedStarted, delayedRelease)
	player.update("PlaybackStatus", variant("Paused"))
	if err := server.Emit(mprisPath, propertiesInterface+".PropertiesChanged", mprisInterface, map[string]dbus.Variant{"PlaybackStatus": variant("Paused")}, []string{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delayedStarted:
	case <-time.After(4 * time.Second):
		t.Fatal("delayed GetAll did not start")
	}
	player.update("PlaybackStatus", variant("Playing"))
	player.update("Position", variant(int64(12_000_000)))
	if err := server.Emit(mprisPath, mprisInterface+".Seeked", int64(12_000_000)); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(func(s Snapshot) bool { return s.Info.Status == "Paused" && s.Info.PositionSeconds >= 12 })
	close(delayedRelease)
	waitSnapshot(func(s Snapshot) bool { return s.Info.Status == "Playing" && s.Info.PositionSeconds >= 12 })
	select {
	case event := <-events:
		if event.Snapshot != nil && event.Snapshot.Info.Status == "Paused" {
			t.Fatalf("stale delayed GetAll response was emitted after fresh Playing snapshot: %+v", event.Snapshot.Info)
		}
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := server.ReleaseName(mprisPrefix + "codex_test"); err != nil {
		t.Fatal(err)
	}
	waitStopped()
	player.update("PlaybackStatus", variant("Playing"))
	if reply, err := server.RequestName(mprisPrefix+"codex_test", dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("re-request name = %v, %v", reply, err)
	}
	// Owner change invalidates state and causes a fresh property query.
	waitSnapshot(func(s Snapshot) bool { return s.Info.Status == "Playing" })

	cancel()
	closeTimer := time.NewTimer(3 * time.Second)
	defer closeTimer.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-closeTimer.C:
			t.Fatal("watcher event channel did not close after cancellation")
		}
	}
}

type mprisTestService struct {
	mu         sync.Mutex
	properties map[string]dbus.Variant
	getCount   int
	delayNext  bool
	delayStart chan<- struct{}
	delayGate  <-chan struct{}
}

func testMPRISProperties(status string, position int64) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"Metadata": variant(map[string]dbus.Variant{
			"mpris:trackid": variant(dbus.ObjectPath("/track/integration")),
			"xesam:title":   variant("Integration track"), "xesam:artist": variant([]string{"Test artist"}),
			"xesam:album": variant("Test album"), "mpris:length": variant(int64(60_000_000)),
		}),
		"PlaybackStatus": variant(status), "Position": variant(position), "Rate": variant(float64(1)),
	}
}

func (s *mprisTestService) update(key string, value dbus.Variant) {
	s.mu.Lock()
	s.properties[key] = value
	s.mu.Unlock()
}

func (s *mprisTestService) delayNextGetAll(start chan<- struct{}, gate <-chan struct{}) {
	s.mu.Lock()
	s.delayNext = true
	s.delayStart = start
	s.delayGate = gate
	s.mu.Unlock()
}

func (s *mprisTestService) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != mprisInterface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
	}
	s.mu.Lock()
	copy := make(map[string]dbus.Variant, len(s.properties))
	for key, value := range s.properties {
		copy[key] = value
	}
	delay, start, gate := s.delayNext, s.delayStart, s.delayGate
	s.delayNext = false
	s.delayStart = nil
	s.delayGate = nil
	s.mu.Unlock()
	delete(copy, "Position")
	if delay {
		start <- struct{}{}
		<-gate
	}
	return copy, nil
}

func (s *mprisTestService) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != mprisInterface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCount++
	value, ok := s.properties[property]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
	}
	return value, nil
}

func (s *mprisTestService) getCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCount
}
