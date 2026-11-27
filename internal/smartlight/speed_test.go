package smartlight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type speedFunc func(context.Context) (float64, error)

func (f speedFunc) Read(ctx context.Context) (float64, error) { return f(ctx) }

func testAdaptive(reader SpeedReader, clock func() time.Time) *AdaptiveFollow {
	return &AdaptiveFollow{Source: reader, Fallback: 150, LeadSeconds: defaultFollowLead, Min: defaultFollowMin, Max: defaultFollowMax,
		Interval: 2 * time.Second, MaxAge: 5 * time.Second, Timeout: time.Second, Clock: clock}
}

func TestSpeedUnitsAndInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		value any
		unit  string
		want  float64
	}{{json.Number("120"), "km/h", 120}, {10, "m/s", 36}, {60.0, "mph", 96.56064}, {0, "km/h", 0},
		{json.Number("120"), "kmPerHour", 120}, {60.0, "milesPerHour", 96.56064}} {
		got, err := SpeedKPH(tc.value, tc.unit)
		if err != nil || math.Abs(got-tc.want) > 1e-6 {
			t.Fatalf("%v %s = %v, %v; want %v", tc.value, tc.unit, got, err, tc.want)
		}
	}
	for _, value := range []any{nil, true, "120", -1, 301, math.NaN(), math.Inf(1)} {
		if _, err := SpeedKPH(value, "km/h"); err == nil {
			t.Fatalf("accepted invalid speed %#v", value)
		}
	}
	if _, err := SpeedKPH(30, "auto"); err == nil {
		t.Fatal("missing real unit accepted")
	}
}

func TestSpeedSourceParsing(t *testing.T) {
	for _, tc := range []struct {
		name, body, unit string
		want             float64
		wantErr          bool
	}{
		{"element", `{"status":"success","data":{"type":"velocityPrimary","currentValue":120,"unit":"km/h"}}`, "auto", 120, false},
		{"numeric-status-string", `{"status":"200","data":{"type":"velocityPrimary","currentValue":120,"unit":"km/h"}}`, "auto", 120, false},
		{"collection", `{"data":[{"type":"velocitySecondary","currentValue":999},{"type":"velocityPrimary","currentValue":30}]}`, "km/h", 30, false},
		{"zero", `{"type":"velocityPrimary","currentValue":0,"unit":"km/h"}`, "auto", 0, false},
		{"viwi-kph-auto", `{"type":"velocityPrimary","currentValue":120,"unit":"kmPerHour"}`, "auto", 120, false},
		{"viwi-kph-configured", `{"type":"velocityPrimary","currentValue":120,"unit":"kmPerHour"}`, "km/h", 120, false},
		{"viwi-kph-configured-alias", `{"type":"velocityPrimary","currentValue":120,"unit":"km/h"}`, "kmPerHour", 120, false},
		{"viwi-mph-auto", `{"type":"velocityPrimary","currentValue":60,"unit":"milesPerHour"}`, "auto", 96.56064, false},
		{"viwi-mph-configured", `{"type":"velocityPrimary","currentValue":60,"unit":"milesPerHour"}`, "mph", 96.56064, false},
		{"viwi-unit-conflict", `{"type":"velocityPrimary","currentValue":30,"unit":"milesPerHour"}`, "km/h", 0, true},
		{"unknown-unit", `{"type":"velocityPrimary","currentValue":30,"unit":"knots"}`, "auto", 0, true},
		{"malformed-unit", `{"type":"velocityPrimary","currentValue":30,"unit":true}`, "km/h", 0, true},
		{"missing", `{"type":"velocityPrimary","unit":"km/h"}`, "auto", 0, true},
		{"missing-unit", `{"type":"velocityPrimary","currentValue":30}`, "auto", 0, true},
		{"unit-conflict", `{"type":"velocityPrimary","currentValue":30,"unit":"mph"}`, "km/h", 0, true},
		{"invalid-flag", `{"type":"velocityPrimary","currentValue":30,"isInvalid":true}`, "km/h", 0, true},
		{"valid-flag", `{"type":"velocityPrimary","currentValue":30,"isValid":false}`, "km/h", 0, true},
		{"malformed-flag", `{"type":"velocityPrimary","currentValue":30,"isInvalid":"false"}`, "km/h", 0, true},
		{"wrong-type", `{"type":"average","currentValue":30}`, "km/h", 0, true},
		{"duplicate", `[{"type":"velocityPrimary","currentValue":30},{"type":"velocityPrimary","currentValue":40}]`, "km/h", 0, true},
		{"pagination", `{"data":[{"type":"velocityPrimary","currentValue":30}],"paging":{"next":"/more"}}`, "km/h", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := decode([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&SpeedSource{Unit: tc.unit}).parse(v)
			if (err != nil) != tc.wantErr || (!tc.wantErr && got != tc.want) {
				t.Fatalf("got %v, %v; want %v error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestAdaptiveDistanceClampsAndFallback(t *testing.T) {
	now := time.Now()
	var value float64
	var readErr error
	var count int
	a := testAdaptive(speedFunc(func(context.Context) (float64, error) { count++; return value, readErr }), func() time.Time { return now })
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if meters, speed := a.Current(); meters != 150 || speed != nil {
		t.Fatal("startup must use fixed fallback")
	}
	for _, tc := range []struct{ speed, meters float64 }{
		{0, 50}, {9, 50}, {15, 83.333333333}, {30, 166.666666667}, {50, 277.777777778},
		{60, 333.333333333}, {75, 416.666666667}, {90, 500},
		{105, 750}, {120, 1000}, {135, 1500}, {150, 2000}, {180, 2000}, {300, 2000},
	} {
		value = tc.speed
		now = now.Add(3 * time.Second)
		if err := a.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		meters, speed := a.Current()
		if math.Abs(meters-tc.meters) > 1e-6 || speed == nil || *speed != tc.speed {
			t.Fatalf("speed=%v: distance=%v want=%v speed=%v", tc.speed, meters, tc.meters, speed)
		}
	}
	before := count
	a.Refresh(context.Background())
	if count != before {
		t.Fatal("speed polling interval bypassed")
	}
	now = now.Add(5 * time.Second)
	if meters, speed := a.Current(); meters != 150 || speed != nil {
		t.Fatal("stale sample did not fall back")
	}
	readErr = errors.New("unreachable")
	a.Refresh(context.Background())
	if meters, speed := a.Current(); meters != 150 || speed != nil {
		t.Fatal("failed read reused old speed")
	}
	a.Fallback = 0
	before = count
	now = now.Add(10 * time.Second)
	a.Refresh(context.Background())
	if meters, speed := a.Current(); meters != 0 || speed != nil || count != before {
		t.Fatal("execute-only should disable speed reads and follow")
	}
}

func TestAdaptiveDistanceTuning(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		speed, lead, min, max, meters float64
	}{
		{"shorter-lead", 120, 10, 50, 2000, 500},
		{"low-speed-lead", 36, 12, 50, 2000, 120},
		{"longer-lead", 90, 24, 50, 2000, 600},
		{"minimum", 30, 20, 250, 2000, 250},
		{"maximum", 150, 20, 50, 1500, 1500},
		{"scaled-maximum", 150, 24, 50, 2000, 2000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			a := testAdaptive(speedFunc(func(context.Context) (float64, error) { return tc.speed, nil }), func() time.Time { return now })
			a.LeadSeconds, a.Min, a.Max = tc.lead, tc.min, tc.max
			if err := a.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := a.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if meters, speed := a.Current(); meters != tc.meters || speed == nil || *speed != tc.speed {
				t.Fatalf("distance=%v speed=%v; want distance=%v speed=%v", meters, speed, tc.meters, tc.speed)
			}
		})
	}
}

func TestAdaptiveSpeedReadBudgetAndCancellation(t *testing.T) {
	a := testAdaptive(speedFunc(func(ctx context.Context) (float64, error) { <-ctx.Done(); return 0, ctx.Err() }), time.Now)
	a.Timeout = 100 * time.Millisecond
	started := time.Now()
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("speed read exceeded total budget")
	}
	if meters, _ := a.Current(); meters != 150 {
		t.Fatal("timeout must fall back")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not preserved", err)
	}
}

func TestSpeedDiscoveryReadOnlyAndCached(t *testing.T) {
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		reads.Add(1)
		var data any
		switch r.URL.Path {
		case "/":
			data = []Object{{"name": "IS5/Displayspeed", "uri": "/IS5/Displayspeed/"}}
		case "/IS5/Displayspeed/":
			data = []Object{{"name": "Speeds", "uri": "/IS5/Displayspeed/Speeds/"}}
		case "/IS5/Displayspeed/Speeds/":
			data = []Object{{"type": "velocityPrimary", "currentValue": 120, "unit": "km/h"}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(Object{"status": "success", "data": data})
	}))
	defer server.Close()
	s, err := NewDiscoveringSpeedSource(server.URL+"/", "auto", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if speed, err := s.Read(context.Background()); speed != 120 || err != nil {
			t.Fatal(speed, err)
		}
	}
	if reads.Load() != 4 || writes.Load() != 0 {
		t.Fatalf("expected discovery=3 then cached=1 GETs, got reads=%d writes=%d", reads.Load(), writes.Load())
	}
}

func TestSpeedDiscoveryRejectsForeignAndRootReferences(t *testing.T) {
	for _, ref := range []string{"http://foreign.invalid/Displayspeed/", "/", "/../Displayspeed/", "/Displayspeed/?token=secret"} {
		t.Run(ref, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				_ = json.NewEncoder(w).Encode(Object{"data": []Object{{"name": "Displayspeed", "uri": ref}}})
			}))
			defer server.Close()
			s, _ := NewDiscoveringSpeedSource(server.URL+"/", "auto", "", time.Second, nil)
			if _, err := s.Read(context.Background()); err == nil || count.Load() != 1 {
				t.Fatal("invalid reference followed", err, count.Load())
			}
		})
	}
}

func TestSpeedSourceDoesNotFollowRedirects(t *testing.T) {
	var foreignReads atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignReads.Add(1) }))
	defer foreign.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	s, _ := NewSpeedSource(source.URL+"/speed", "auto", "Bearer speed-secret", time.Second, nil)
	if _, err := s.Read(context.Background()); err == nil || foreignReads.Load() != 0 {
		t.Fatal("redirect followed", err, foreignReads.Load())
	}
}

func TestAdaptiveDisabledAndExecuteOnlyNeverReadSpeed(t *testing.T) {
	var reads atomic.Int32
	speed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		http.Error(w, "unexpected request", 500)
	}))
	defer speed.Close()
	for _, disabled := range []bool{false, true} {
		f := newFixture(t)
		var out, stderr bytes.Buffer
		args := []string{"carplay", "--base", f.server.URL + "/smartlight/", "--nav-base", f.server.URL + "/simplenavigation/", "--iterations", "1", "--state-dir", t.TempDir()}
		if disabled {
			args = append(args, "--adaptive-follow=false")
		} else {
			args = append(args, "--speed-base", speed.URL+"/", "--follow-distance", "0")
		}
		if code := Run(context.Background(), args, &out, &stderr); code != 0 {
			t.Fatal(code, out.String(), stderr.String())
		}
	}
	if reads.Load() != 0 {
		t.Fatal("disabled or execute-only mode contacted speed source")
	}
}

func TestSpeedCredentialsAreIndependent(t *testing.T) {
	f := newFixture(t)
	var got atomic.Value
	speed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"data":{"type":"velocityPrimary","currentValue":120,"unit":"km/h"}}`)
	}))
	defer speed.Close()
	dir := t.TempDir()
	for _, name := range []string{"light", "nav", "speed"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("Bearer "+name+"-secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out, stderr bytes.Buffer
	args := []string{"carplay", "--base", f.server.URL + "/smartlight/", "--nav-base", f.server.URL + "/simplenavigation/", "--speed-uri", speed.URL + "/speed", "--auth-file", filepath.Join(dir, "light"), "--nav-auth-file", filepath.Join(dir, "nav"), "--speed-auth-file", filepath.Join(dir, "speed"), "--iterations", "1", "--state-dir", filepath.Join(dir, "state")}
	if code := Run(context.Background(), args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String())
	}
	if got.Load() != "Bearer speed-secret" {
		t.Fatal("wrong speed credential", got.Load())
	}
	for path, values := range f.snapshot().auths {
		for _, value := range values {
			want := "Bearer light-secret"
			if strings.HasPrefix(path, "/simplenavigation/") {
				want = "Bearer nav-secret"
			}
			if value != want {
				t.Fatalf("%s credential=%s want=%s", path, value, want)
			}
		}
	}
	if strings.Contains(out.String(), "secret") || strings.Contains(stderr.String(), "secret") {
		t.Fatal("credential leaked to logs")
	}
}

func TestAdaptiveSlowSpeedCannotDelayActivePulseCleanup(t *testing.T) {
	f := newFixture(t)
	nav, _ := NewClient(f.server.URL+"/simplenavigation/", "", "", time.Second, nil)
	started := time.Now()
	a := testAdaptive(speedFunc(func(ctx context.Context) (float64, error) { <-ctx.Done(); return 0, ctx.Err() }), time.Now)
	// An existing sample is about to require refreshing while a pulse is live.
	a.valid, a.speed, a.received, a.nextRead = true, 120, started, started.Add(20*time.Millisecond)
	sink := &timingPulseSink{}
	c := &Controller{Sink: sink, Hold: 150 * time.Millisecond, MaxDuration: time.Second, FollowMeters: 150, Adaptive: a, Clock: time.Now}
	source := &NavigationSource{Client: nav, FollowMeters: 150, Adaptive: a}
	if err := RunMonitor(context.Background(), source, c, 3, 30*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	if sink.active || sink.stopped.Sub(sink.started) > 450*time.Millisecond {
		t.Fatalf("speed read postponed cleanup: %s", sink.stopped.Sub(sink.started))
	}
}

func TestAdaptiveSpeedIsPausedForAppliedTurn(t *testing.T) {
	for _, repeat := range []bool{true, false} {
		name := "repeat"
		if !repeat {
			name = "one-shot"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			started := time.Now()
			now := started
			controller, source, manager := repeatController(t, f, func() time.Time { return now })
			controller.Repeat = repeat
			var reads int
			adaptive := testAdaptive(speedFunc(func(context.Context) (float64, error) {
				reads++
				if reads == 1 {
					return 120, nil
				}
				return 36, nil
			}), controller.Clock)
			source.Adaptive, controller.Adaptive = adaptive, adaptive

			// Drive the monitor through sample expiry, possible re-post,
			// stage expiry, and a different turn in the same context.
			if repeat {
				f.configure(func(f *fixture) { f.stage, f.distance = "follow", 300 })
			}
			snapshots := 0
			wantPosts := 1
			log := func(event string, fields Object) {
				if event != "carplay_snapshot" {
					return
				}
				snapshots++
				switch snapshots {
				case 1:
					if manager.State == nil || f.snapshot().count != 1 {
						t.Fatal("first eligible turn did not start")
					}
					now = started.Add(6 * time.Second)
					f.configure(func(f *fixture) {
						f.distance = 280
						f.queue = f.queue[:1] // The service finished the first request.
					})
				case 2:
					if repeat {
						wantPosts = 2
					}
					if f.snapshot().count != wantPosts || (manager.State != nil) != repeat {
						t.Fatal("the frozen threshold did not carry through service completion")
					}
					now = started.Add(9 * time.Second)
					f.configure(func(f *fixture) { f.stage, f.id = "execute", "refreshed" })
				case 3:
					now = controller.Started.Add(controller.MaxDuration)
				case 4:
					if (manager.State != nil) != repeat || f.snapshot().count != wantPosts {
						t.Fatal("stage expiry changed request ownership")
					}
					now = started.Add(32 * time.Second)
					f.configure(func(f *fixture) {
						if repeat {
							f.stage, f.direction, f.distance, f.id = "follow", 192, 300, "right"
						} else {
							f.id = "" // Confirmed absence ends the turn even with an active route.
						}
					})
				case 5:
					if fields["speed_sampling_paused"] != nil || f.snapshot().count != wantPosts {
						t.Fatalf("turn boundary retained the paused sampling or posted: fields=%v posts=%d", fields, f.snapshot().count)
					}
					if repeat {
						if reads != 2 || fields["follow_distance_m"] != 200.0 {
							t.Fatalf("opposite turn did not use live speed: reads=%d fields=%v", reads, fields)
						}
					} else {
						f.configure(func(f *fixture) {
							f.stage, f.distance, f.id = "execute", 80, "next-left"
						})
					}
				case 6:
					if reads != 2 || fields["follow_distance_m"] != 200.0 || f.snapshot().count != 2 {
						t.Fatalf("turn after confirmed absence did not rearm: reads=%d fields=%v posts=%d", reads, fields, f.snapshot().count)
					}
				}
				if snapshots <= 4 && (reads != 1 || fields["follow_distance_m"] != 1000.0 || fields["speed_sampling_paused"] != true) {
					t.Fatalf("turn updated speed on round %d: reads=%d fields=%v", snapshots, reads, fields)
				}
			}
			iterations := 5
			if !repeat {
				iterations = 6
			}
			if err := RunMonitor(context.Background(), source, controller, iterations, 0, log); err != nil {
				t.Fatal(err)
			}
			if snapshots != iterations {
				t.Fatalf("observed %d snapshots, want %d", snapshots, iterations)
			}
		})
	}
}

func TestAdaptiveSpeedDoesNotRearmUnappliedTurnWindow(t *testing.T) {
	now := time.Now()
	a := testAdaptive(speedFunc(func(context.Context) (float64, error) { return 120, nil }), func() time.Time { return now })
	a.valid, a.speed, a.received = true, 120, now
	sink := &fakeSink{}
	c := &Controller{Sink: sink, Hold: time.Second, MaxDuration: 5 * time.Second, Repeat: true, FollowMeters: 150, Clock: func() time.Time { return now }, Adaptive: a}
	distance := 300.0
	event := &NavigationEvent{ContextURI: "same", Direction: "left", Stage: "follow", Distance: &distance}
	if err := c.Step(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	started := c.Started
	now = now.Add(time.Second)
	a.speed = 30
	if err := c.Step(context.Background(), event); err != nil || sink.active || c.Started != started {
		t.Fatalf("lower threshold failed cleanup or reset window: %v", err)
	}
	now = now.Add(6 * time.Second)
	a.speed, a.received = 120, now
	if err := c.Step(context.Background(), event); err != nil || sink.active || len(sink.starts) != 1 || c.Started != started {
		t.Fatalf("speed oscillation rearmed exhausted follow stage: %v", err)
	}
}

func TestAdaptiveFollowCLIOptionsAndDefaults(t *testing.T) {
	base := []string{"carplay", "--base", "http://fixture/smartlight/", "--nav-base", "http://fixture/navigation/"}
	o, err := parseOptions(base, io.Discard)
	if err != nil || !o.AdaptiveFollow || o.SpeedBase != DefaultSpeedBase || o.SpeedUnit != "auto" || o.MaxDuration != 15 || o.FollowLead != 20 || o.FollowMin != 50 || o.FollowMax != 2000 {
		t.Fatalf("unexpected CarPlay defaults: %#v %v", o, err)
	}
	o, err = parseOptions(append(append([]string{}, base...), "--adaptive-follow=false"), io.Discard)
	if err != nil || o.AdaptiveFollow {
		t.Fatal("disable flag", err)
	}
	for _, args := range [][]string{
		{"--speed-uri", "http://user:pass@fixture/speed"}, {"--speed-unit", "knots"},
		{"--follow-min-distance", "2001"}, {"--follow-max-distance", "2001"},
		{"--follow-min-distance", "600", "--follow-max-distance", "500"}, {"--follow-lead-time", "NaN"},
		{"--speed-interval", "0.5"}, {"--speed-max-age", "2"},
		{"--adaptive-follow=false", "--speed-unit", "km/h"},
	} {
		if _, err := parseOptions(append(append([]string{}, base...), args...), io.Discard); err == nil {
			t.Fatalf("invalid speed flags accepted: %v", args)
		}
	}
}

func TestAdaptiveCarPlayCLIDistanceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		speed, meters float64
	}{{"36-kph", 36, 200}, {"90-kph", 90, 500}, {"120-kph", 120, 1000}, {"150-kph", 150, 2000}, {"180-kph", 180, 2000}} {
		t.Run(tc.name, func(t *testing.T) {
			for _, boundary := range []struct {
				name     string
				offset   float64
				wantPost int
			}{{"at-threshold", 0, 1}, {"above-threshold", 1, 0}} {
				t.Run(boundary.name, func(t *testing.T) {
					f := newFixture(t)
					f.configure(func(f *fixture) { f.stage, f.distance = "follow", tc.meters+boundary.offset })
					speed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_ = json.NewEncoder(w).Encode(Object{"data": Object{"type": "velocityPrimary", "currentValue": tc.speed, "unit": "km/h"}})
					}))
					defer speed.Close()
					var out, stderr bytes.Buffer
					args := []string{"carplay", "--base", f.server.URL + "/smartlight/", "--nav-base", f.server.URL + "/simplenavigation/", "--speed-uri", speed.URL + "/speed", "--iterations", "1", "--state-dir", t.TempDir(), "--apply"}
					if code := Run(context.Background(), args, &out, &stderr); code != 0 {
						t.Fatalf("code=%d %s %s", code, &out, &stderr)
					}
					if f.snapshot().count != boundary.wantPost {
						t.Fatalf("posts=%d want=%d log=%s", f.snapshot().count, boundary.wantPost, &out)
					}
					var snapshots int
					decoder := json.NewDecoder(&out)
					for {
						var fields Object
						err := decoder.Decode(&fields)
						if errors.Is(err, io.EOF) {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						if fields["event"] == "carplay_snapshot" {
							snapshots++
							if fields["follow_distance_m"] != tc.meters || fields["speed_available"] != true {
								t.Fatalf("unexpected threshold in snapshot: %v", fields)
							}
						}
					}
					if snapshots != 1 {
						t.Fatalf("snapshots=%d want=1", snapshots)
					}
					f.assertBaseline(t)
				})
			}
		})
	}
}

func TestAdaptiveCarPlayCLIHighSpeedLowSpeedAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantPost   bool
	}{{"high-speed", `{"data":{"type":"velocityPrimary","currentValue":120,"unit":"km/h"}}`, true},
		{"viwi-high-speed", `{"status":"ok","data":[{"type":"velocityPrimary","currentValue":120,"unit":"kmPerHour"},{"type":"velocityPrimaryHiRes","currentValue":120,"unit":"kmPerHour"},{"type":"velocitySecondary","currentValue":75,"unit":"milesPerHour"}]}`, true},
		{"viwi-stationary", `{"status":"ok","data":[{"type":"velocityPrimary","currentValue":0,"unit":"kmPerHour"}]}`, false},
		{"low-speed", `{"data":{"type":"velocityPrimary","currentValue":30,"unit":"km/h"}}`, false},
		{"missing-unit-fallback", `{"data":{"type":"velocityPrimary","currentValue":120}}`, false},
		{"invalid-fallback", `{"data":{"type":"velocityPrimary","currentValue":-1,"unit":"km/h"}}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.configure(func(f *fixture) { f.stage, f.distance = "follow", json.Number("300") })
			var reads atomic.Int32
			speed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
					t.Error("unexpected mutation or inherited credential")
				}
				reads.Add(1)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer speed.Close()
			var out, stderr bytes.Buffer
			args := []string{"carplay", "--base", f.server.URL + "/smartlight/", "--nav-base", f.server.URL + "/simplenavigation/", "--speed-uri", speed.URL + "/speed", "--iterations", "1", "--state-dir", t.TempDir(), "--apply"}
			if code := Run(context.Background(), args, &out, &stderr); code != 0 {
				t.Fatalf("code=%d %s %s", code, &out, &stderr)
			}
			want := 0
			if tc.wantPost {
				want = 1
			}
			if f.snapshot().count != want || reads.Load() != 1 || !strings.Contains(out.String(), "follow_distance_m") {
				t.Fatalf("posts=%d speed reads=%d log=%s", f.snapshot().count, reads.Load(), &out)
			}
			if strings.HasPrefix(tc.name, "viwi-") && !strings.Contains(out.String(), `"speed_available":true`) {
				t.Fatalf("VIWI response unexpectedly fell back: %s", &out)
			}
			if tc.name == "viwi-stationary" && !strings.Contains(out.String(), `"follow_distance_m":50`) {
				t.Fatalf("zero speed did not use the minimum threshold: %s", &out)
			}
			f.assertBaseline(t)
		})
	}
}
