package smartlight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	mu                                                                           sync.Mutex
	server                                                                       *httptest.Server
	queue                                                                        []Object
	writes                                                                       []string
	bodies                                                                       []Object
	paths                                                                        []string
	auths                                                                        map[string][]string
	count, navPolls                                                              int
	noLocation, badJSON, postError, groupOff, redirect, crossRef, changedRecheck bool
	getDrops                                                                     int
	deleteError                                                                  bool
	stage, unit, technology                                                      string
	distance                                                                     any
	direction                                                                    int
	active                                                                       bool
	id                                                                           string
	stopAt, changeAt                                                             int
	navDelay                                                                     time.Duration
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{queue: []Object{{"id": "existing", "name": "phone", "uri": "/smartlight/queuedpurposes/existing"}}, stage: "execute", unit: "m", distance: json.Number("50"), direction: 64, active: true, id: "current", technology: "appleCarPlay", auths: map[string][]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
	f.auths[r.URL.Path] = append(f.auths[r.URL.Path], r.Header.Get("Authorization"))
	if f.getDrops > 0 && r.Method == "GET" {
		f.getDrops--
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
		return
	}
	write := func(code int, data any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if data != nil {
			_ = json.NewEncoder(w).Encode(Object{"status": "success", "data": data})
		}
	}
	if f.deleteError && r.Method == http.MethodDelete {
		f.writes = append(f.writes, "DELETE_FAILED "+r.URL.Path)
		write(500, nil)
		return
	}
	if r.URL.Path == "/registry/" {
		write(200, []Object{{"name": "simplenavigation", "uri": f.server.URL + "/simplenavigation/"}})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/simplenavigation/") {
		if f.navDelay > 0 && f.navPolls >= 2 {
			f.mu.Unlock()
			time.Sleep(f.navDelay)
			f.mu.Lock()
		}
		if r.Method != "GET" {
			write(405, nil)
			return
		}
		if strings.Contains(r.URL.Path, "/drivingContexts/") {
			if strings.HasSuffix(r.URL.Path, "/drivingContexts/") {
				f.navPolls++
			}
			active := f.active
			if f.stopAt > 0 && f.navPolls >= f.stopAt {
				active = false
			}
			if f.changeAt > 0 && f.navPolls >= f.changeAt {
				f.direction = 192
				f.id = "changed"
			}
			ref := "/simplenavigation/maneuvers/" + f.id
			if f.crossRef {
				ref = "http://127.0.0.1:1/foreign"
			}
			write(200, []Object{{"id": "cp", "uri": "/simplenavigation/drivingContexts/cp", "technology": f.technology, "isRouteGuidanceActive": active, "nextManeuver": Object{"uri": ref}}})
			return
		}
		if strings.Contains(r.URL.Path, "/maneuvers/") {
			write(200, []Object{{"id": f.id, "uri": "/simplenavigation/maneuvers/" + f.id, "mainType": 13, "directionType": f.direction, "stage": f.stage, "distance": f.distance, "distanceUnit": f.unit}})
			return
		}
		write(404, nil)
		return
	}
	if f.redirect && r.Method == "GET" {
		w.Header().Set("Location", f.server.URL+"/foreign")
		write(307, nil)
		return
	}
	switch {
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/purposes/"):
		left := Object{"id": "left", "name": "15", "uri": "/smartlight/purposes/left", "functionCategory": "navigation", "functionName": "direction", "functionValue": []string{"left"}, "settingGroup": Object{"id": "turn"}}
		right := Object{"id": "right", "name": "14", "uri": "/smartlight/purposes/right", "functionCategory": "navigation", "functionName": "direction", "functionValue": []string{"right"}, "settingGroup": Object{"id": "turn"}}
		write(200, []Object{left, right})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/configurations/"):
		write(200, []Object{{"id": "cfg", "uri": "/smartlight/configurations/cfg", "smartlightState": "ready"}})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/settinggroups/"):
		write(200, []Object{{"id": "turn", "uri": "/smartlight/settinggroups/turn", "name": "nav_turn", "isActivated": !f.groupOff}})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/queuedpurposes/"):
		write(200, f.queue)
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/players/"):
		write(200, []Object{{"id": "player", "name": "default"}})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/versions/"):
		write(200, []Object{{"id": "v", "name": "playerServiceSw", "version": "fixture"}})
	case r.Method == "POST" && r.URL.Path == "/smartlight/queuedpurposes/":
		var body Object
		_ = json.NewDecoder(r.Body).Decode(&body)
		if (body["name"] != "15" || body["purpose"] != "/smartlight/purposes/left") && (body["name"] != "14" || body["purpose"] != "/smartlight/purposes/right") {
			write(400, nil)
			return
		}
		f.count++
		uri := fmt.Sprintf("/smartlight/queuedpurposes/created-%d", f.count)
		f.queue = append(f.queue, Object{"id": fmt.Sprint(f.count), "uri": uri, "name": body["name"], "purpose": Object{"uri": body["purpose"]}})
		f.writes = append(f.writes, "POST "+uri)
		f.bodies = append(f.bodies, body)
		if !f.noLocation {
			w.Header().Set("Location", uri)
		}
		if f.postError {
			write(500, nil)
			return
		}
		if f.badJSON {
			w.WriteHeader(201)
			_, _ = io.WriteString(w, "not json")
			return
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	case r.Method == "DELETE":
		f.writes = append(f.writes, "DELETE "+r.URL.Path)
		var kept []Object
		for _, row := range f.queue {
			if str(row["uri"]) != r.URL.Path {
				kept = append(kept, row)
			}
		}
		if len(kept) == len(f.queue) {
			write(404, nil)
		} else {
			f.queue = kept
			write(204, nil)
		}
	default:
		write(404, nil)
	}
}

func (f *fixture) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(f.server.URL+"/smartlight/", "", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.RetryDelay = time.Millisecond
	return c
}
func (f *fixture) assertBaseline(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) != 1 || f.queue[0]["id"] != "existing" {
		t.Fatalf("other requests changed: %#v", f.queue)
	}
}

func TestTriggerDryRunListProbeAndSelectors(t *testing.T) {
	for _, action := range []string{"list", "probe", "trigger"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			args := []string{action, "--base", f.server.URL + "/smartlight/"}
			if action == "trigger" {
				args = append(args, "--direction", "left")
			}
			var out, errout bytes.Buffer
			if code := Run(context.Background(), args, &out, &errout); code != 0 {
				t.Fatalf("code=%d %s %s", code, &out, &errout)
			}
			if len(f.writes) != 0 {
				t.Fatal("dry-run mutated queue")
			}
			f.assertBaseline(t)
		})
	}
	for _, sel := range []Selector{{Direction: "left"}, {Purpose: "15"}, {Purpose: "left"}, {PurposeURI: "/smartlight/purposes/left"}} {
		f := newFixture(t)
		p, err := SelectPurpose(context.Background(), f.client(t), sel)
		if err != nil || p["name"] != "15" {
			t.Fatalf("selector: %v %#v", err, p)
		}
	}
}

func TestTriggerContractStateAndCleanup(t *testing.T) {
	f := newFixture(t)
	c := f.client(t)
	path := filepath.Join(t.TempDir(), "run.json")
	m := &RequestManager{Client: c, Apply: true, Hold: 100 * time.Millisecond}
	ok, err := m.Start(context.Background(), Selector{Direction: "left"}, path)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.bodies, []Object{{"name": "15", "purpose": "/smartlight/purposes/left"}}) {
		t.Fatal(f.bodies)
	}
	s, err := LoadState(path)
	if err != nil || s.QueueURI != f.server.URL+"/smartlight/queuedpurposes/created-1" || s.Phase != "posted" {
		t.Fatalf("%#v %v", s, err)
	}
	if err = m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertBaseline(t)
	s, _ = LoadState(path)
	if s.Phase != "cleaned" {
		t.Fatal(s)
	}
	if err = Cleanup(context.Background(), c, s, path, false); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 2 {
		t.Fatal("cleanup not idempotent")
	}
}

func TestPostErrorAndMalformedBodyKeepLocationForCleanup(t *testing.T) {
	for _, mode := range []string{"http500", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.postError = mode == "http500"
			f.badJSON = mode == "malformed"
			m := &RequestManager{Client: f.client(t), Apply: true}
			path := filepath.Join(t.TempDir(), "run.json")
			_, err := m.Start(context.Background(), Selector{Direction: "left"}, path)
			if err == nil {
				t.Fatal("expected failure")
			}
			s, _ := LoadState(path)
			if s.QueueURI == "" {
				t.Fatal("Location lost before JSON")
			}
			if err = m.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if f.count != 1 {
				t.Fatal("POST retried")
			}
			f.assertBaseline(t)
		})
	}
}

func TestMissingLocationNeverDeletesByName(t *testing.T) {
	f := newFixture(t)
	f.noLocation = true
	m := &RequestManager{Client: f.client(t), Apply: true}
	path := filepath.Join(t.TempDir(), "run.json")
	_, err := m.Start(context.Background(), Selector{Direction: "left"}, path)
	if err == nil {
		t.Fatal("missing Location accepted")
	}
	if err = m.Stop(context.Background()); err == nil {
		t.Fatal("unknown identity falsely cleaned")
	}
	if f.count != 1 || len(f.writes) != 1 {
		t.Fatal(f.writes)
	}
	s, _ := LoadState(path)
	if s.Phase == "cleaned" {
		t.Fatal(s)
	}
	if err = Cleanup(context.Background(), m.Client, s, path, true); err == nil {
		t.Fatal("confirm absent bypassed v2 identity")
	}
}

func TestBaselineSameNameAndDisabledGroupPreserved(t *testing.T) {
	for _, mode := range []string{"same-name", "group-off"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if mode == "same-name" {
				f.queue = append(f.queue, Object{"id": "other", "name": "15", "uri": "/smartlight/queuedpurposes/other"})
			} else {
				f.groupOff = true
			}
			m := &RequestManager{Client: f.client(t), Apply: true}
			_, err := m.Start(context.Background(), Selector{Direction: "left"}, filepath.Join(t.TempDir(), "run.json"))
			if mode == "group-off" && err == nil {
				t.Fatal("disabled group accepted")
			}
			if len(f.writes) != 0 {
				t.Fatal(f.writes)
			}
		})
	}
}

func TestCleanupRejectsOverlapAndMismatchedMetadata(t *testing.T) {
	f := newFixture(t)
	c := f.client(t)
	s := &State{Kind: "smartlight-short-request-v2", Phase: "posted", Base: c.Base.String(), Advertised: c.Base.String(), PurposeURI: c.Resource("purposes") + "left", RequestName: "15", QueueURI: c.Resource("queuedpurposes") + "existing", Baseline: []string{c.Resource("queuedpurposes") + "existing"}}
	if err := Cleanup(context.Background(), c, s, filepath.Join(t.TempDir(), "s.json"), false); err == nil {
		t.Fatal("baseline overlap accepted")
	}
	if len(f.writes) != 0 {
		t.Fatal("deleted baseline")
	}
}

func TestURIValidationIPv6MappingAndTraversal(t *testing.T) {
	c, err := NewClient("http://[fd53:7cb8:383:3::73]:49520/smartlight/", "http://[::1]:49520/smartlight/", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.Checked("http://[::1]:49520/smartlight/purposes/x", "purposes")
	if err != nil || u != "http://[fd53:7cb8:383:3::73]:49520/smartlight/purposes/x" {
		t.Fatal(u, err)
	}
	for _, raw := range []string{"http://evil.invalid/smartlight/purposes/x", "/smartlight/queuedpurposes/x", "/smartlight/purposes/../x", "/smartlight/purposes/%2e%2e/x", "/smartlight/purposes/%5cx", "/smartlight/purposes/x?q=1", "http://u:p@[::1]:49520/smartlight/purposes/x"} {
		if _, err = c.Checked(raw, "purposes"); err == nil {
			t.Error("accepted", raw)
		}
	}
	if _, err = NewClient(`http\://[::1]:49520/`, "", "", time.Second, nil); err == nil {
		t.Fatal("backslash accepted")
	}
}

func TestPaginationAndCycle(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(fmt.Sprint(cycle), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next := ""
				if r.URL.RawQuery == "" || cycle {
					next = "/smartlight/purposes/?page=2"
				}
				_ = json.NewEncoder(w).Encode(Object{"status": "ok", "data": []Object{{"id": "x", "uri": "/smartlight/purposes/x"}}, "paging": Object{"next": next}})
			}))
			defer server.Close()
			c, _ := NewClient(server.URL+"/smartlight/", "", "", time.Second, nil)
			rows, err := c.Collection(context.Background(), "purposes")
			if cycle && err == nil {
				t.Fatal("cycle accepted")
			}
			if !cycle && (err != nil || len(rows) != 2) {
				t.Fatal(rows, err)
			}
		})
	}
}

func TestGetRetryAndRedirect(t *testing.T) {
	f := newFixture(t)
	f.getDrops = 1
	c := f.client(t)
	if _, err := c.Collection(context.Background(), "purposes"); err != nil {
		t.Fatal(err)
	}
	if len(f.paths) != 2 {
		t.Fatalf("GET retry count %d", len(f.paths))
	}
	f.redirect = true
	if _, err := c.Collection(context.Background(), "purposes"); err == nil {
		t.Fatal("redirect accepted")
	}
	if strings.Contains(strings.Join(f.paths, "\n"), "/foreign") {
		t.Fatal("redirect followed")
	}
}

func TestRegistryCredentialsDoNotFlowToService(t *testing.T) {
	f := newFixture(t)
	u, err := ResolveService(context.Background(), f.server.URL+"/registry/", "simplenavigation", "Bearer registry", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewClient(u, "", "Bearer nav", time.Second, nil)
	if _, err = c.Collection(context.Background(), "drivingContexts"); err != nil {
		t.Fatal(err)
	}
	if got := f.auths["/registry/"]; len(got) != 1 || got[0] != "Bearer registry" {
		t.Fatal(got)
	}
	if got := f.auths["/simplenavigation/drivingContexts/"]; len(got) != 1 || got[0] != "Bearer nav" {
		t.Fatal(got)
	}
	if f.paths[0] != "GET /registry/?name=simplenavigation" {
		t.Fatal(f.paths)
	}
}

func TestUnitsAndMalformedDistance(t *testing.T) {
	for unit, scale := range map[string]float64{"m": 1, "km": 1000, "mi": 1609.344, "yd": .9144, "ft": .3048} {
		m, err := DistanceMeters(json.Number("1"), unit)
		if err != nil || m != scale {
			t.Fatal(unit, m, err)
		}
	}
	for _, bad := range []any{true, false, -1, math.NaN(), math.Inf(1), "50", nil} {
		if _, err := DistanceMeters(bad, "m"); err == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}
	if _, err := DistanceMeters(1, "unknown"); err == nil {
		t.Fatal("unknown unit")
	}
	d, err := DistanceMeters(json.Number("0.2"), "km")
	if err != nil || d != 200 {
		t.Fatal(d, err)
	}
	event := &NavigationEvent{Direction: "left", Stage: "follow", Distance: &d}
	if event.Eligible(150) {
		t.Fatal("0.2 km treated as 0.2 meters")
	}
}

type fakeSink struct {
	starts []string
	stops  int
	active bool
}

func (f *fakeSink) Start(_ context.Context, e *NavigationEvent) (bool, error) {
	f.starts = append(f.starts, e.Direction)
	f.active = true
	return true, nil
}
func (f *fakeSink) Stop(context.Context) error {
	if f.active {
		f.stops++
	}
	f.active = false
	return nil
}

func TestControllerRepeatWindowSurvivesIDChurn(t *testing.T) {
	now := time.Now()
	sink := &fakeSink{}
	c := &Controller{Sink: sink, Hold: 100 * time.Millisecond, MaxDuration: time.Second, FollowMeters: 150, Repeat: true, Clock: func() time.Time { return now }}
	e := &NavigationEvent{ContextURI: "context", ID: "1", Direction: "left", Stage: "execute"}
	if err := c.Step(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	now = now.Add(200 * time.Millisecond)
	e.ID = "2"
	c.Step(context.Background(), e)
	now = now.Add(time.Second)
	e.ID = "3"
	c.Step(context.Background(), e)
	if len(sink.starts) != 2 || sink.active {
		t.Fatal("id churn reset max-duration", sink)
	}
}

func TestControllerOneShotAndStopSessionReset(t *testing.T) {
	now := time.Now()
	sink := &fakeSink{}
	c := &Controller{Sink: sink, Hold: 100 * time.Millisecond, MaxDuration: time.Second, FollowMeters: 150, Clock: func() time.Time { return now }}
	e := &NavigationEvent{ContextURI: "c", Direction: "left", Stage: "execute"}
	c.Step(context.Background(), e)
	now = now.Add(200 * time.Millisecond)
	c.Step(context.Background(), e)
	if len(sink.starts) != 1 {
		t.Fatal(sink)
	}
	c.Step(context.Background(), nil)
	c.Step(context.Background(), e)
	if len(sink.starts) != 2 {
		t.Fatal(sink)
	}
}

func TestNavigationPollRevalidationAndSourceFilters(t *testing.T) {
	f := newFixture(t)
	c, _ := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	source := &NavigationSource{Client: c, FollowMeters: 150}
	e, err := source.Poll(context.Background())
	if err != nil || e.Direction != "left" {
		t.Fatal(e, err)
	}
	f.id = "refreshed"
	f.direction = 96
	ok, err := source.StillCurrent(context.Background(), e)
	if err != nil || !ok {
		t.Fatal("same semantic turn id churn", ok, err)
	}
	f.direction = 192
	ok, err = source.StillCurrent(context.Background(), e)
	if err != nil || ok {
		t.Fatal("opposite direction accepted", ok, err)
	}
	f.technology = "androidAuto"
	e, err = source.Poll(context.Background())
	if err != nil || e != nil {
		t.Fatal(e, err)
	}
}

func TestNavigationRejectsCrossOriginAndInvalidDistance(t *testing.T) {
	for _, mode := range []string{"cross", "negative", "bool", "unit"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			switch mode {
			case "cross":
				f.crossRef = true
			case "negative":
				f.distance = -1
			case "bool":
				f.distance = true
			case "unit":
				f.unit = "bad"
			}
			c, _ := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
			_, err := (&NavigationSource{Client: c, FollowMeters: 150}).Poll(context.Background())
			if err == nil {
				t.Fatal("accepted malformed snapshot")
			}
			if len(f.writes) != 0 {
				t.Fatal("mutated")
			}
		})
	}
}

func TestCarplayCLIEndToEndAndRollback(t *testing.T) {
	f := newFixture(t)
	f.stopAt = 3
	dir := filepath.Join(t.TempDir(), "state")
	var out, stderr bytes.Buffer
	code := Run(context.Background(), []string{"carplay", "--base", f.server.URL + "/smartlight/", "--nav-registry", f.server.URL + "/registry/", "--state-dir", dir, "--interval", "0.1", "--hold", "0.1", "--iterations", "3", "--apply", "--one-shot"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("code %d %s %s", code, &out, &stderr)
	}
	f.assertBaseline(t)
	if len(f.writes) != 2 {
		t.Fatal(f.writes)
	}
	if _, err := os.Stat(filepath.Join(dir, "carplay-monitor.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lock not released")
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "carplay-*.json"))
	if len(paths) != 1 {
		t.Fatal(paths)
	}
	state, _ := LoadState(paths[0])
	if state.Phase != "cleaned" {
		t.Fatal(state)
	}
}

func TestCancellationDefersPostThenCleans(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	c := f.client(t)
	// Cancel as soon as a POST response identity has been durably recorded.
	log := func(event string, _ Object) {
		if event == "http" && f.count == 1 {
			cancel()
		}
	}
	c.Log = log
	m := &RequestManager{Client: c, Apply: true}
	path := filepath.Join(t.TempDir(), "s.json")
	_, err := m.Start(ctx, Selector{Direction: "left"}, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertBaseline(t)
}

func TestStateDirectoryLockPendingAndExclusiveState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "states")
	d, err := OpenStateDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenStateDirectory(dir, true); err == nil {
		t.Fatal("duplicate lock")
	}
	d.Close()
	s := &State{Kind: "smartlight-short-request-v2", Phase: "creating", QueueURI: "http://fixture.invalid/smartlight/queuedpurposes/owned"}
	p := filepath.Join(dir, "carplay-pending.json")
	if err = WriteState(p, s, true); err != nil {
		t.Fatal(err)
	}
	if err = WriteState(p, s, true); err == nil {
		t.Fatal("state overwritten")
	}
	if _, err = OpenStateDirectory(dir, true); err == nil {
		t.Fatal("pending state accepted")
	}
}

func TestNavigationPostFailureContinuesAfterExactCleanup(t *testing.T) {
	for _, mode := range []string{"http500", "bad-json", "missing-location"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.postError = mode == "http500"
			f.badJSON = mode == "bad-json"
			f.noLocation = mode == "missing-location"
			nav, _ := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
			source := &NavigationSource{Client: nav, FollowMeters: 150}
			dir, err := OpenStateDirectory(filepath.Join(t.TempDir(), "states"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			sink := &NavigationSink{Manager: &RequestManager{Client: f.client(t), Apply: true}, Directory: dir, Source: source}
			event, err := source.Poll(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ok, err := sink.Start(context.Background(), event)
			if err != nil || ok || sink.Manager.State != nil {
				t.Fatalf("ignored failure: %v %v", ok, err)
			}
			if f.count != 1 {
				t.Fatalf("POST retried: %d", f.count)
			}
			if mode == "missing-location" {
				archives, _ := filepath.Glob(filepath.Join(dir.Path, "ignored-post-*.json"))
				if len(archives) != 1 {
					t.Fatal("uncertain evidence not preserved", archives)
				}
				s, err := LoadState(archives[0])
				if err != nil || s.Phase == "cleaned" || s.QueueURI != "" {
					t.Fatal("falsely marked clean", s, err)
				}
				if len(f.writes) != 1 {
					t.Fatal("guessed delete", f.writes)
				}
			} else {
				f.assertBaseline(t)
			}
		})
	}
}

func TestKnownFailedPostCleanupRemainsRecoveryRequired(t *testing.T) {
	f := newFixture(t)
	f.postError, f.deleteError = true, true
	var out, stderr bytes.Buffer
	dir := filepath.Join(t.TempDir(), "states")
	code := Run(context.Background(), []string{"carplay", "--base", f.server.URL + "/smartlight/", "--nav-base", f.server.URL + "/simplenavigation/", "--state-dir", dir, "--iterations", "1", "--apply"}, &out, &stderr)
	if code != 78 {
		t.Fatalf("want recovery code 78, got %d: %s", code, &out)
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "carplay-*.json"))
	if len(paths) != 1 {
		t.Fatal("known cleanup identity lost", paths)
	}
	s, _ := LoadState(paths[0])
	if s.QueueURI == "" || s.Phase == "cleaned" {
		t.Fatal("known pending identity lost", s)
	}
	if f.count != 1 {
		t.Fatal("failure caused new POST", f.count)
	}
}

func TestStartupArchivesUnknownReceiptWithoutDeletingEvidence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "states")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "carplay-old.json")
	s := &State{Kind: "smartlight-short-request-v2", Phase: "creating", RequestName: "15"}
	if err := WriteState(p, s, true); err != nil {
		t.Fatal(err)
	}
	d, err := OpenStateDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	archives, _ := filepath.Glob(filepath.Join(dir, "ignored-post-*.json"))
	if len(archives) != 1 {
		t.Fatal("missing archive", archives)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("active state not retired")
	}
	archived, err := LoadState(archives[0])
	if err != nil || archived.RequestName != "15" || archived.Phase != "creating" {
		t.Fatal("evidence changed", archived, err)
	}
}

func TestSkippedPulseRespectsHoldCadence(t *testing.T) {
	now := time.Now()
	sink := &skippedPulseSink{}
	c := &Controller{Sink: sink, Hold: time.Second, MaxDuration: 30 * time.Second, FollowMeters: 150, Repeat: true, Clock: func() time.Time { return now }}
	e := &NavigationEvent{ContextURI: "c", Direction: "left", Stage: "execute"}
	for _, advance := range []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond} {
		now = now.Add(advance)
		if err := c.Step(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if sink.count != 1 {
		t.Fatal("hammered skipped request", sink.count)
	}
	now = now.Add(time.Second)
	if err := c.Step(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if sink.count != 2 {
		t.Fatal("missing paced retry", sink.count)
	}
}

type skippedPulseSink struct{ count int }

func (s *skippedPulseSink) Start(context.Context, *NavigationEvent) (bool, error) {
	s.count++
	return false, nil
}
func (s *skippedPulseSink) Stop(context.Context) error { return nil }

func TestCLIArgumentValidation(t *testing.T) {
	for _, args := range [][]string{{"trigger", "--base", "http://fixture/", "--direction", "left", "--purpose", "x"}, {"carplay", "--base", "http://fixture/"}, {"carplay", "--base", "http://fixture/", "--nav-base", "http://nav/", "--nav-registry", "http://registry/"}, {"trigger", "--base", "http://fixture/", "--direction", "left", "--hold", "NaN"}, {"cleanup"}} {
		var out, err bytes.Buffer
		if Run(context.Background(), args, &out, &err) != 2 {
			t.Fatal(args)
		}
	}
}

func TestSlowSourceReadExpiresAndCleansPulse(t *testing.T) {
	f := newFixture(t)
	f.navDelay = 500 * time.Millisecond
	c := f.client(t)
	nav, _ := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	source := &NavigationSource{Client: nav, FollowMeters: 150}
	dir, err := OpenStateDirectory(filepath.Join(t.TempDir(), "states"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	sink := &NavigationSink{Manager: &RequestManager{Client: c, Apply: true}, Directory: dir, Source: source}
	controller := &Controller{Sink: sink, Hold: 50 * time.Millisecond, MaxDuration: time.Second, FollowMeters: 150, Repeat: false, Clock: time.Now}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = RunMonitor(ctx, source, controller, 2, 10*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	f.assertBaseline(t)
	if len(f.writes) != 2 {
		t.Fatal(f.writes)
	}
}

func TestTransportNeverReplaysPOSTAfterLostResponse(t *testing.T) {
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer server.Close()
	c, _ := NewClient(server.URL+"/smartlight/", "", "", time.Second, nil)
	if _, err := c.Request(context.Background(), http.MethodPost, c.Resource("queuedpurposes"), Object{"name": "x", "purpose": "/smartlight/purposes/x"}, false, nil); err == nil {
		t.Fatal("lost response expected")
	}
	if count != 1 {
		t.Fatalf("POST replayed %d times", count)
	}
}
