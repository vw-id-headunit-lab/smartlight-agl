package smartlight

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func repeatController(t *testing.T, f *fixture, clock func() time.Time) (*Controller, *NavigationSource, *RequestManager) {
	t.Helper()
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := OpenStateDirectory(filepath.Join(t.TempDir(), "states"), true)
	if err != nil {
		t.Fatal(err)
	}
	source := &NavigationSource{Client: nav, FollowMeters: 150}
	manager := &RequestManager{Client: f.client(t), Apply: true, Hold: 3 * time.Second}
	c := &Controller{
		Sink: &NavigationSink{Manager: manager, Directory: directory, Source: source},
		Hold: 3 * time.Second, MaxDuration: defaultStageDuration * time.Second, FollowMeters: 150,
		Repeat: true, Clock: clock,
	}
	t.Cleanup(func() {
		if err := c.expireIndependent(); err != nil {
			t.Error(err)
		}
		if err := directory.Close(); err != nil {
			t.Error(err)
		}
		f.assertBaseline(t)
	})
	return c, source, manager
}

func pollStep(t *testing.T, source *NavigationSource, c *Controller) {
	t.Helper()
	event, err := source.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Step(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatKeepsOwnedRequestAcrossFollowExecuteAndIDRefresh(t *testing.T) {
	f := newFixture(t)
	started := time.Now()
	now := started
	c, source, manager := repeatController(t, f, func() time.Time { return now })
	for _, step := range []struct {
		elapsed, stageStarted time.Duration
		stage, id             string
	}{
		{0, 0, "follow", "follow"},
		{15 * time.Second, 0, "follow", "traffic"},
		{90 * time.Second, 90 * time.Second, "execute", "execute"},
		{93 * time.Second, 90 * time.Second, "execute", "refreshed"},
		{105 * time.Second, 90 * time.Second, "execute", "last"},
		{106 * time.Second, 90 * time.Second, "follow", "stage-regression"},
		{107 * time.Second, 90 * time.Second, "execute", "resumed"},
	} {
		now = started.Add(step.elapsed)
		f.configure(func(f *fixture) { f.stage, f.id = step.stage, step.id })
		pollStep(t, source, c)
		snapshot := f.snapshot()
		if snapshot.count != 1 || len(snapshot.writes) != 1 || manager.State == nil {
			t.Fatalf("%s at %s interrupted the owned request: %v", step.stage, step.elapsed, snapshot.writes)
		}
		if want := started.Add(step.stageStarted); c.Started != want {
			t.Fatalf("stage start at %s: got %s, want %s", step.elapsed, c.Started, want)
		}
	}
	f.configure(func(f *fixture) { f.active = false })
	pollStep(t, source, c)
	if manager.State != nil || len(f.snapshot().writes) != 2 {
		t.Fatalf("turn end did not clean the original request: %v", f.snapshot().writes)
	}
}

func TestExecuteCanRestartAfterTrafficAndFollowExpiry(t *testing.T) {
	f := newFixture(t)
	started := time.Now()
	now := started
	c, source, _ := repeatController(t, f, func() time.Time { return now })
	f.configure(func(f *fixture) { f.stage = "follow" })
	pollStep(t, source, c)
	f.configure(func(f *fixture) { f.queue = f.queue[:1] })
	now = started.Add(20 * time.Second)
	pollStep(t, source, c)
	if f.snapshot().count != 1 {
		t.Fatal("follow reposted after its window ended")
	}

	now = started.Add(90 * time.Second)
	f.configure(func(f *fixture) { f.stage = "execute" })
	pollStep(t, source, c)
	if f.snapshot().count != 2 {
		t.Fatal("execute did not start a request after the follow request finished")
	}

	f.configure(func(f *fixture) { f.queue = f.queue[:1] })
	now = started.Add(104 * time.Second)
	pollStep(t, source, c)
	if f.snapshot().count != 3 {
		t.Fatal("execute did not replace the request within its own window")
	}
	f.configure(func(f *fixture) { f.queue = f.queue[:1] })
	now = started.Add(107 * time.Second)
	pollStep(t, source, c)
	f.configure(func(f *fixture) { f.stage = "follow" })
	pollStep(t, source, c)
	f.configure(func(f *fixture) { f.stage = "execute" })
	pollStep(t, source, c)
	if f.snapshot().count != 3 {
		t.Fatal("execute expiry or stage regression allowed a new request")
	}
}

func TestExecuteCanStartImmediatelyAfterSkippedFollow(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	c, source, _ := repeatController(t, f, func() time.Time { return now })
	f.configure(func(f *fixture) { f.stage = "follow" })
	follow, err := source.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.configure(func(f *fixture) { f.stage = "execute" })
	if err = c.Step(context.Background(), follow); err != nil {
		t.Fatal(err)
	}
	if f.snapshot().count != 0 {
		t.Fatal("follow preflight posted after navigation entered execute")
	}
	pollStep(t, source, c)
	if c.Started != now || f.snapshot().count != 1 {
		t.Fatal("skipped follow attempt delayed the new execute window")
	}
}

func TestRepeatRepostsOnlyAfterOwnedQueueItemDisappears(t *testing.T) {
	f := newFixture(t)
	started := time.Now()
	now := started
	c, source, manager := repeatController(t, f, func() time.Time { return now })
	pollStep(t, source, c)
	firstPath, firstURI := manager.Path, manager.State.QueueURI
	// Simulate service-side completion, leaving the pre-existing phone intact.
	f.configure(func(f *fixture) {
		var kept []Object
		for _, row := range f.queue {
			if f.server.URL+str(row["uri"]) != firstURI {
				kept = append(kept, row)
			}
		}
		f.queue = kept
	})
	now = started.Add(2 * time.Second)
	pollStep(t, source, c)
	if len(f.snapshot().writes) != 1 {
		t.Fatalf("completion bypassed the hold cadence: %v", f.snapshot().writes)
	}
	now = started.Add(3 * time.Second)
	pollStep(t, source, c)
	oldState, err := LoadState(firstPath)
	if err != nil || oldState.Phase != "cleaned" {
		t.Fatalf("completed receipt not settled: state=%#v err=%v", oldState, err)
	}
	snapshot := f.snapshot()
	if snapshot.count != 2 || len(snapshot.writes) != 3 || snapshot.writes[1] != "DELETE /smartlight/queuedpurposes/created-1" || snapshot.writes[2] != "POST /smartlight/queuedpurposes/created-2" {
		t.Fatalf("completion did not settle the exact old item before reposting: %v", snapshot.writes)
	}
	if manager.State == nil || manager.State.QueueURI == firstURI || c.Started != started {
		t.Fatalf("completion lost ownership or reset the window: state=%#v started=%s", manager.State, c.Started)
	}
}

func TestRepeatStillCleansOnConfirmedNavigationBoundary(t *testing.T) {
	for _, boundary := range []string{"stopped", "instruction", "far-follow", "opposite-direction", "new-context"} {
		t.Run(boundary, func(t *testing.T) {
			f := newFixture(t)
			now := time.Now()
			c, source, _ := repeatController(t, f, func() time.Time { return now })
			pollStep(t, source, c)
			now = now.Add(time.Second)
			event, err := source.Poll(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "stopped":
				event = nil
			case "instruction":
				event.Stage = "instruction"
			case "far-follow":
				event.Stage = "follow"
				distance := 1000.0
				event.Distance = &distance
			case "opposite-direction":
				event.Direction = "right"
				f.configure(func(f *fixture) { f.direction = 192 })
			case "new-context":
				event.ContextURI += "-replacement"
			}
			if err = c.Step(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			writes := f.snapshot().writes
			if len(writes) < 2 || writes[1] != "DELETE /smartlight/queuedpurposes/created-1" {
				t.Fatalf("boundary failed to clean the old request: %v", writes)
			}
		})
	}
}

func TestMonitorRepeatNavigationReadCanOutlastHold(t *testing.T) {
	f := newFixture(t)
	f.configure(func(f *fixture) { f.navDelay = 80 * time.Millisecond })
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink := &timingPulseSink{}
	c := &Controller{Sink: sink, Hold: 50 * time.Millisecond, MaxDuration: time.Second, FollowMeters: 150, Repeat: true, Clock: time.Now}
	snapshots := 0
	log := func(event string, _ Object) {
		if event == "carplay_snapshot" {
			snapshots++
		}
	}
	if err = RunMonitor(context.Background(), &NavigationSource{Client: nav, FollowMeters: 150}, c, 2, 10*time.Millisecond, log); err != nil {
		t.Fatal(err)
	}
	if duration := sink.stopped.Sub(sink.started); snapshots != 2 || sink.starts != 1 || duration < 150*time.Millisecond {
		t.Fatalf("hold deadline interrupted a valid navigation read: snapshots=%d starts=%d duration=%s", snapshots, sink.starts, duration)
	}
}

type slowQueuedSink struct{ timingPulseSink }

func (s *slowQueuedSink) Queued(ctx context.Context) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

func TestRepeatSlowCompletionCheckStillHonorsWindow(t *testing.T) {
	f := newFixture(t)
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink := &slowQueuedSink{}
	c := &Controller{Sink: sink, Hold: 20 * time.Millisecond, MaxDuration: 100 * time.Millisecond, FollowMeters: 150, Repeat: true, Clock: time.Now}
	observations := 0
	log := func(event string, _ Object) {
		if event == "carplay_snapshot" {
			observations++
			if !sink.active {
				t.Fatal("completion check expiry stopped playback")
			}
		}
	}
	if err = RunMonitor(context.Background(), &NavigationSource{Client: nav, FollowMeters: 150}, c, 3, 30*time.Millisecond, log); err != nil {
		t.Fatalf("completion check expiry returned an error: %v", err)
	}
	if duration := sink.stopped.Sub(sink.started); observations != 3 || sink.starts != 1 || sink.active || duration < 120*time.Millisecond || duration > 500*time.Millisecond {
		t.Fatalf("completion read escaped the stage window: starts=%d active=%v duration=%s", sink.starts, sink.active, duration)
	}
}
