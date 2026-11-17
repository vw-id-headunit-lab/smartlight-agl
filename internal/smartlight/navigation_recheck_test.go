package smartlight

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type recheckScenario struct {
	initial func(Object)
	confirm func(Object)
}

func newRecheckSource(t *testing.T, scenario recheckScenario) (*NavigationSource, *NavigationEvent, *atomic.Int32) {
	t.Helper()
	var contextReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "read-only navigation service", http.StatusMethodNotAllowed)
			return
		}
		var row Object
		switch r.URL.Path {
		case "/simplenavigation/drivingContexts/cp":
			read := contextReads.Add(1)
			row = Object{
				"uri": "/simplenavigation/drivingContexts/cp", "technology": "appleCarPlay",
				"isRouteGuidanceActive": true, "nextManeuver": Object{"uri": "/simplenavigation/maneuvers/refreshed"},
			}
			if read == 1 && scenario.initial != nil {
				scenario.initial(row)
			}
			if read > 1 && scenario.confirm != nil {
				scenario.confirm(row)
			}
		case "/simplenavigation/maneuvers/refreshed":
			// This element remains readable even when the route changes or stops.
			row = Object{"uri": "/simplenavigation/maneuvers/refreshed", "id": "refreshed", "mainType": 13, "directionType": 64, "stage": "execute"}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Object{"status": "success", "data": []Object{row}})
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	previous := &NavigationEvent{
		ContextURI:  server.URL + "/simplenavigation/drivingContexts/cp",
		ManeuverURI: server.URL + "/simplenavigation/maneuvers/old", ID: "old", Direction: "left", Stage: "execute",
	}
	return &NavigationSource{Client: client, FollowMeters: 150}, previous, &contextReads
}

func TestStillCurrentConfirmsContextAfterManeuver(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario recheckScenario
		want     bool
		wantErr  bool
		reads    int32
	}{
		{name: "stable-refreshed-id", want: true, reads: 2},
		{name: "route-stopped", scenario: recheckScenario{confirm: func(row Object) { row["isRouteGuidanceActive"] = false }}, reads: 2},
		{name: "technology-changed", scenario: recheckScenario{confirm: func(row Object) { row["technology"] = "androidAuto" }}, reads: 2},
		{name: "context-changed", scenario: recheckScenario{confirm: func(row Object) { row["uri"] = "/simplenavigation/drivingContexts/replacement" }}, reads: 2},
		{name: "maneuver-changed", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = "/simplenavigation/maneuvers/new" }}, reads: 2},
		{name: "null-reference", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = nil }}, reads: 2},
		{name: "empty-reference", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = "" }}, reads: 2},
		{name: "empty-reference-object", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = Object{} }}, reads: 2},
		{name: "missing-reference", scenario: recheckScenario{confirm: func(row Object) { delete(row, "nextManeuver") }}, reads: 2},
		{name: "initial-null-reference", scenario: recheckScenario{initial: func(row Object) { row["nextManeuver"] = nil }}, reads: 1},
		{name: "initial-empty-reference", scenario: recheckScenario{initial: func(row Object) { row["nextManeuver"] = "" }}, reads: 1},
		{name: "initial-stopped", scenario: recheckScenario{initial: func(row Object) { row["isRouteGuidanceActive"] = false }}, reads: 1},
		{name: "malformed-reference", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = 23 }}, wantErr: true, reads: 2},
		{name: "malformed-reference-object", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = Object{"uri": false} }}, wantErr: true, reads: 2},
		{name: "cross-origin-reference", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = "http://foreign.invalid/simplenavigation/maneuvers/new" }}, wantErr: true, reads: 2},
		{name: "cross-origin-context", scenario: recheckScenario{confirm: func(row Object) { row["uri"] = "http://foreign.invalid/simplenavigation/drivingContexts/cp" }}, wantErr: true, reads: 2},
		{name: "traversal-reference", scenario: recheckScenario{confirm: func(row Object) { row["nextManeuver"] = "/simplenavigation/maneuvers/%2e%2e/new" }}, wantErr: true, reads: 2},
		{name: "initial-cross-origin-reference", scenario: recheckScenario{initial: func(row Object) { row["nextManeuver"] = "http://foreign.invalid/simplenavigation/maneuvers/new" }}, wantErr: true, reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, previous, reads := newRecheckSource(t, tc.scenario)
			ok, err := source.StillCurrent(context.Background(), previous)
			if ok != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("StillCurrent=%v, error=%v; want %v, error=%v", ok, err, tc.want, tc.wantErr)
			}
			if reads.Load() != tc.reads {
				t.Fatalf("context reads=%d; want %d", reads.Load(), tc.reads)
			}
		})
	}
}

func TestChangedNavigationSkipsPostAndCanResume(t *testing.T) {
	for _, change := range []string{"stopped", "empty", "replaced"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			source, previous, _ := newRecheckSource(t, recheckScenario{confirm: func(row Object) {
				switch change {
				case "stopped":
					row["isRouteGuidanceActive"] = false
				case "empty":
					row["nextManeuver"] = nil
				case "replaced":
					row["nextManeuver"] = "/simplenavigation/maneuvers/new"
				}
			}})
			manager := &RequestManager{Client: f.client(t), Apply: true, BeforePost: func(ctx context.Context) (bool, error) { return source.StillCurrent(ctx, previous) }}
			path := filepath.Join(t.TempDir(), "request.json")
			ok, err := manager.Start(context.Background(), Selector{Direction: "left"}, path)
			if ok || err != nil || manager.State != nil {
				t.Fatalf("changed route should skip without ending monitor: ok=%v error=%v state=%v", ok, err, manager.State)
			}
			if len(f.snapshot().writes) != 0 {
				t.Fatalf("changed route caused mutation: %v", f.snapshot().writes)
			}
			if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("skipped request should not create intent: %v", err)
			}
			// A later stable snapshot should still create one owned request and
			// clean it normally; the transient mismatch is not a terminal error.
			stable, current, _ := newRecheckSource(t, recheckScenario{})
			manager.BeforePost = func(ctx context.Context) (bool, error) { return stable.StillCurrent(ctx, current) }
			ok, err = manager.Start(context.Background(), Selector{Direction: "left"}, path)
			if !ok || err != nil {
				t.Fatalf("stable follow-up did not resume: ok=%v error=%v", ok, err)
			}
			if err = manager.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.assertBaseline(t)
			if f.snapshot().count != 1 {
				t.Fatalf("want exactly one resumed POST; got %d", f.snapshot().count)
			}
		})
	}
}
