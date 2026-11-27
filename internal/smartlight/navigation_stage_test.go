package smartlight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newStageSource(t *testing.T) *NavigationSource {
	t.Helper()
	client, err := NewClient("http://[fd53:7cb8:383:3::73]:49520/simplenavigation/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &NavigationSource{Client: client, FollowMeters: 150}
}

func stageRow(distance any, unit string) Object {
	row := Object{
		"uri": "/simplenavigation/maneuvers/m", "id": "m",
		"name": "maneuvers", "type": "direction",
		"mainType": 13, "directionType": 64, "stage": "",
	}
	if distance != nil {
		row["distance"] = distance
		row["distanceUnit"] = unit
	}
	return row
}

// Some head units never populate the simplenavigation "stage" field, including
// on the maneuver itself at zero distance. Treating that as a terminal error
// stops the monitor before it can issue any request, so a missing stage is
// derived from the distance instead of aborting the read.
func TestMissingStageDerivesFromDistance(t *testing.T) {
	source := newStageSource(t)
	contextURI := "http://[fd53:7cb8:383:3::73]:49520/simplenavigation/drivingContexts/c"
	maneuverURI := "http://[fd53:7cb8:383:3::73]:49520/simplenavigation/maneuvers/m"
	for _, tc := range []struct {
		name       string
		distance   any
		unit       string
		wantStage  string
		wantMeters float64
		update     func(Object)
	}{
		{name: "execute-at-turn", distance: 0.0, unit: "m", wantStage: "execute", wantMeters: 0},
		{name: "execute-boundary", distance: 50.0, unit: "m", wantStage: "execute", wantMeters: 50},
		{name: "follow-past-boundary", distance: 50.5, unit: "m", wantStage: "follow", wantMeters: 50.5},
		{name: "follow-kilometres", distance: 1.2, unit: "km", wantStage: "follow", wantMeters: 1200},
		{name: "omitted-stage", distance: 20, unit: "m", wantStage: "execute", wantMeters: 20, update: func(row Object) {
			delete(row, "stage")
		}},
		{name: "null-stage", distance: 20, unit: "m", wantStage: "execute", wantMeters: 20, update: func(row Object) {
			row["stage"] = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := stageRow(tc.distance, tc.unit)
			if tc.update != nil {
				tc.update(row)
			}
			event, err := source.event(row, contextURI, maneuverURI)
			if err != nil {
				t.Fatalf("missing stage should not fail: %v", err)
			}
			if event == nil || event.Stage != tc.wantStage {
				t.Fatalf("stage=%v; want %q", event, tc.wantStage)
			}
			if event.Direction != "left" {
				t.Fatalf("direction=%q; want left", event.Direction)
			}
			if event.Distance == nil || *event.Distance != tc.wantMeters {
				t.Fatalf("distance=%v; want %v", event.Distance, tc.wantMeters)
			}
		})
	}
}

func TestUnusableStageSnapshotIsTransient(t *testing.T) {
	source := newStageSource(t)
	for _, tc := range []struct {
		name     string
		distance any
		unit     string
		stage    any
	}{
		{"missing-distance", nil, "", ""},
		{"missing-unit", 20, "", ""},
		{"invalid-distance", -1, "m", ""},
		{"unknown-stage", 20, "m", "futureStage"},
		{"boolean-stage", 20, "m", false},
		{"numeric-stage", 20, "m", json.Number("0")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := stageRow(tc.distance, tc.unit)
			row["stage"] = tc.stage
			event, err := source.event(row,
				"http://[fd53:7cb8:383:3::73]:49520/simplenavigation/drivingContexts/c",
				"http://[fd53:7cb8:383:3::73]:49520/simplenavigation/maneuvers/m")
			if event != nil || !errors.Is(err, ErrSnapshotChanged) {
				t.Fatalf("expected a skipped snapshot, got event=%v err=%v", event, err)
			}
		})
	}
}

// A provided stage keeps its published meaning; the fallback never overrides it.
func TestProvidedStageIsNotDerived(t *testing.T) {
	source := newStageSource(t)
	row := stageRow(10.0, "m")
	row["stage"] = "prepare"
	event, err := source.event(row,
		"http://[fd53:7cb8:383:3::73]:49520/simplenavigation/drivingContexts/c",
		"http://[fd53:7cb8:383:3::73]:49520/simplenavigation/maneuvers/m")
	if err != nil {
		t.Fatal(err)
	}
	if event.Stage != "prepare" {
		t.Fatalf("stage=%q; want prepare", event.Stage)
	}
}

func TestManeuverSnapshotSkipAllowsLaterPost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		badRead  int32
		oneShot  bool
		distance any
		stage    string
		reason   string
	}{
		{"missing-distance-during-poll", 1, false, nil, "", "navigation_snapshot_changed"},
		{"missing-distance-before-post", 2, false, nil, "", "navigation_changed_during_preflight"},
		{"missing-distance-before-one-shot", 2, true, nil, "", "navigation_changed_during_preflight"},
		{"unknown-stage-before-one-shot", 2, true, 20, "futureStage", "navigation_changed_during_preflight"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.configure(func(f *fixture) { f.stage, f.distance = "", 20 })
			var reads atomic.Int32
			nav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/simplenavigation/maneuvers/current" || reads.Add(1) != tc.badRead {
					f.serve(w, r)
					return
				}
				row := stageRow(tc.distance, "m")
				row["id"], row["uri"], row["stage"] = "current", r.URL.Path, tc.stage
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(Object{"status": "success", "data": []Object{row}})
			}))
			defer nav.Close()

			args := []string{"carplay", "--base", f.server.URL + "/smartlight/",
				"--nav-base", nav.URL + "/simplenavigation/", "--state-dir", t.TempDir(),
				"--adaptive-follow=false", "--iterations", "3", "--interval", "0.1", "--hold", "0.1", "--apply"}
			if tc.oneShot {
				args = append(args, "--one-shot")
			}
			var out, stderr bytes.Buffer
			code := Run(context.Background(), args, &out, &stderr)
			if code != 0 || f.snapshot().count != 1 || !bytes.Contains(out.Bytes(), []byte(tc.reason)) {
				t.Fatalf("exit=%d POSTs=%d, stderr=%s, log=%s", code, f.snapshot().count, &stderr, &out)
			}
			f.assertBaseline(t)
		})
	}
}
