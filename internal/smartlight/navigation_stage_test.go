package smartlight

import (
	"errors"
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
	}{
		{name: "execute-at-turn", distance: 0.0, unit: "m", wantStage: "execute", wantMeters: 0},
		{name: "execute-boundary", distance: 50.0, unit: "m", wantStage: "execute", wantMeters: 50},
		{name: "follow-past-boundary", distance: 50.5, unit: "m", wantStage: "follow", wantMeters: 50.5},
		{name: "follow-kilometres", distance: 1.2, unit: "km", wantStage: "follow", wantMeters: 1200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, err := source.event(stageRow(tc.distance, tc.unit), contextURI, maneuverURI)
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

// Without a distance there is nothing to derive from. A transient error keeps
// the monitor alive and retries on the next interval instead of exiting.
func TestMissingStageWithoutDistanceIsTransient(t *testing.T) {
	source := newStageSource(t)
	_, err := source.event(stageRow(nil, ""),
		"http://[fd53:7cb8:383:3::73]:49520/simplenavigation/drivingContexts/c",
		"http://[fd53:7cb8:383:3::73]:49520/simplenavigation/maneuvers/m")
	if !errors.Is(err, ErrSnapshotChanged) {
		t.Fatalf("missing distance should be transient; got %v", err)
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
