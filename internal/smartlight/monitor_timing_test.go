package smartlight

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func missingManeuverSource(t *testing.T, reads *atomic.Int32, first chan struct{}) *NavigationSource {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var nextManeuver any
		if r.URL.Path == "/simplenavigation/drivingContexts/cp" {
			nextManeuver = "/simplenavigation/maneuvers/became-current"
		} else if reads.Add(1) == 1 && first != nil {
			close(first)
		}
		_ = json.NewEncoder(w).Encode(Object{"status": "success", "data": []Object{{
			"uri": "/simplenavigation/drivingContexts/cp", "technology": "appleCarPlay",
			"isRouteGuidanceActive": true, "nextManeuver": nextManeuver,
		}}})
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &NavigationSource{Client: client, FollowMeters: 150}
}

func TestMonitorSnapshotChangesRespectIntervalAndIterations(t *testing.T) {
	var reads atomic.Int32
	source := missingManeuverSource(t, &reads, nil)
	controller := &Controller{Sink: &fakeSink{}, Hold: time.Second, MaxDuration: time.Second, Clock: time.Now}
	started := time.Now()
	if err := RunMonitor(context.Background(), source, controller, 3, 50*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if reads.Load() != 3 || elapsed < 100*time.Millisecond {
		t.Fatalf("snapshot retries were not paced: reads=%d elapsed=%s", reads.Load(), elapsed)
	}
}

func TestMonitorSnapshotRetryWaitIsCancelable(t *testing.T) {
	var reads atomic.Int32
	first := make(chan struct{})
	source := missingManeuverSource(t, &reads, first)
	controller := &Controller{Sink: &fakeSink{}, Hold: time.Second, MaxDuration: time.Second, Clock: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunMonitor(ctx, source, controller, 0, 2*time.Second, nil) }()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first navigation read did not arrive")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("interval wait ignored cancellation")
	}
	if reads.Load() != 1 {
		t.Fatalf("snapshot retry issued before interval: %d reads", reads.Load())
	}
}

type timingPulseSink struct {
	started, stopped time.Time
	starts           int
	active           bool
}

func (s *timingPulseSink) Start(context.Context, *NavigationEvent) (bool, error) {
	s.started = time.Now()
	s.starts++
	s.active = true
	return true, nil
}
func (s *timingPulseSink) Queued(context.Context) (bool, error) { return s.active, nil }

func (s *timingPulseSink) Stop(context.Context) error {
	if s.active {
		s.stopped = time.Now()
	}
	s.active = false
	return nil
}

func TestMonitorKeepsPulsePastStageWindow(t *testing.T) {
	f := newFixture(t)
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink := &timingPulseSink{}
	controller := &Controller{Sink: sink, Hold: time.Second, MaxDuration: 50 * time.Millisecond, FollowMeters: 150, Repeat: true, Clock: time.Now}
	if err = RunMonitor(context.Background(), &NavigationSource{Client: nav, FollowMeters: 150}, controller, 2, 130*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	duration := sink.stopped.Sub(sink.started)
	if sink.starts != 1 || sink.active || duration < 130*time.Millisecond {
		t.Fatalf("stage expiry interrupted active pulse: starts=%d active=%v duration=%s", sink.starts, sink.active, duration)
	}
}

func TestMonitorCanReadNavigationPastStageWindow(t *testing.T) {
	f := newFixture(t)
	f.configure(func(f *fixture) { f.navDelay = 80 * time.Millisecond })
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", 2*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink := &timingPulseSink{}
	controller := &Controller{Sink: sink, Hold: time.Second, MaxDuration: 100 * time.Millisecond, FollowMeters: 150, Repeat: true, Clock: time.Now}
	snapshots := 0
	log := func(event string, _ Object) {
		if event == "carplay_snapshot" {
			snapshots++
			if !sink.active {
				t.Fatal("stage expiry revoked the request before the next snapshot")
			}
		}
	}
	if err = RunMonitor(context.Background(), &NavigationSource{Client: nav, FollowMeters: 150}, controller, 2, 10*time.Millisecond, log); err != nil {
		t.Fatal(err)
	}
	duration := sink.stopped.Sub(sink.started)
	if snapshots != 2 || duration <= controller.MaxDuration || sink.active {
		t.Fatalf("stage expiry interrupted a slow navigation read: snapshots=%d duration=%s active=%v", snapshots, duration, sink.active)
	}
}

func TestMonitorExhaustedWindowKeepsNormalPollingCadence(t *testing.T) {
	f := newFixture(t)
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink := &timingPulseSink{}
	controller := &Controller{Sink: sink, Hold: time.Second, MaxDuration: 100 * time.Millisecond, FollowMeters: 150, Repeat: true, Clock: time.Now}
	var snapshots []time.Time
	log := func(event string, _ Object) {
		if event == "carplay_snapshot" {
			snapshots = append(snapshots, time.Now())
		}
	}
	if err = RunMonitor(context.Background(), &NavigationSource{Client: nav, FollowMeters: 150}, controller, 4, 80*time.Millisecond, log); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 4 || sink.starts != 1 || sink.active {
		t.Fatalf("exhausted window restarted or skipped polls: snapshots=%d starts=%d active=%v", len(snapshots), sink.starts, sink.active)
	}
	// An expired request window leaves navigation on its normal cadence.
	if elapsed := snapshots[3].Sub(snapshots[2]); elapsed < 80*time.Millisecond {
		t.Fatalf("expired window caused a fast retry loop: last interval=%s", elapsed)
	}
}

func TestControllerRepeatRequestKeepsOriginalWindow(t *testing.T) {
	now := time.Now()
	sink := &fakeSink{}
	controller := &Controller{Sink: sink, Hold: 3 * time.Second, MaxDuration: 5 * time.Second, Repeat: true, FollowMeters: 150, Clock: func() time.Time { return now }}
	event := &NavigationEvent{ContextURI: "c", Direction: "left", Stage: "execute"}
	if err := controller.Step(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	windowEnd := now.Add(5 * time.Second)
	now = now.Add(3 * time.Second)
	if err := controller.Step(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !controller.HoldUntil.IsZero() || len(sink.starts) != 1 || sink.stops != 0 {
		t.Fatalf("repeat interrupted request or scheduled a hold deadline: until=%s starts=%d stops=%d", controller.HoldUntil, len(sink.starts), sink.stops)
	}
	now = windowEnd
	if err := controller.Step(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !sink.active || len(sink.starts) != 1 || sink.stops != 0 {
		t.Fatal("exhausted window stopped or restarted an active request")
	}
}

func TestControllerSlowPreflightExpiresWithoutPostOrRestart(t *testing.T) {
	for _, partialBody := range []bool{false, true} {
		name := "headers"
		if partialBody {
			name = "body"
		}
		t.Run(name, func(t *testing.T) { testSlowPreflightWindow(t, partialBody) })
	}
}

func testSlowPreflightWindow(t *testing.T, partialBody bool) {
	f := newFixture(t)
	preflightCanceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/smartlight/configurations/" {
			if partialBody {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"success","data":[`))
				w.(http.Flusher).Flush()
			}
			<-r.Context().Done()
			preflightCanceled <- struct{}{}
			return
		}
		f.serve(w, r)
	}))
	defer server.Close()
	light, err := NewClient(server.URL+"/smartlight/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := OpenStateDirectory(filepath.Join(t.TempDir(), "states"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	source := &NavigationSource{Client: nav, FollowMeters: 150}
	event, err := source.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manager := &RequestManager{Client: light, Apply: true}
	controller := &Controller{Sink: &NavigationSink{Manager: manager, Directory: directory, Source: source}, Hold: time.Second, MaxDuration: 100 * time.Millisecond, FollowMeters: 150, Repeat: true, Clock: time.Now}
	if err = controller.Step(context.Background(), event); err != nil {
		t.Fatalf("expired preflight should end window, not restart monitor: %v", err)
	}
	select {
	case <-preflightCanceled:
	case <-time.After(time.Second):
		t.Fatal("preflight did not receive max-window cancellation")
	}
	if err = controller.Step(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if f.snapshot().count != 0 || manager.State != nil || !controller.HoldUntil.IsZero() {
		t.Fatalf("expired window attempted a new POST: posts=%d state=%#v until=%s", f.snapshot().count, manager.State, controller.HoldUntil)
	}
}

func TestControllerInFlightPostSurvivesStageExpiry(t *testing.T) {
	f := newFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			time.Sleep(200 * time.Millisecond)
		}
		f.serve(w, r)
	}))
	defer server.Close()
	light, err := NewClient(server.URL+"/smartlight/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	nav, err := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := OpenStateDirectory(filepath.Join(t.TempDir(), "states"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	adaptive := testAdaptive(speedFunc(func(context.Context) (float64, error) { return 60, nil }), time.Now)
	source := &NavigationSource{Client: nav, FollowMeters: 150, Adaptive: adaptive}
	event, err := source.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manager := &RequestManager{Client: light, Apply: true}
	controller := &Controller{Sink: &NavigationSink{Manager: manager, Directory: directory, Source: source}, Hold: 3 * time.Second, MaxDuration: 100 * time.Millisecond, FollowMeters: 150, Repeat: true, Clock: time.Now, Adaptive: adaptive}
	defer func() {
		if err := controller.expireIndependent(); err != nil {
			t.Error(err)
		}
	}()
	if err = controller.Step(context.Background(), event); err != nil {
		t.Fatalf("in-flight request failed at stage expiry: %v", err)
	}
	if manager.State == nil || len(f.snapshot().writes) != 1 {
		t.Fatalf("stage expiry did not retain the accepted POST: state=%#v writes=%v", manager.State, f.snapshot().writes)
	}
	if adaptive.Fields()["speed_sampling_paused"] != true {
		t.Fatal("accepted request released speed sampling before the navigation boundary")
	}
	path := manager.Path
	if err = controller.Step(context.Background(), event); err != nil || len(f.snapshot().writes) != 1 {
		t.Fatalf("expired stage changed the request: %v writes=%v", err, f.snapshot().writes)
	}
	if err = controller.Step(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadState(path)
	if err != nil || saved.QueueURI == "" || saved.Phase != "cleaned" {
		t.Fatalf("receipt was not preserved through cleanup: %#v %v", saved, err)
	}
	f.assertBaseline(t)
}
