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
	return &AdaptiveFollow{Source: reader, Fallback: 150, LeadSeconds: 12, Min: 50, Max: 500,
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
	for _, tc := range []struct{ speed, meters float64 }{{0, 50}, {30, 100}, {60, 200}, {120, 400}, {180, 500}} {
		value = tc.speed
		now = now.Add(3 * time.Second)
		if err := a.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		meters, speed := a.Current()
		if meters != tc.meters || speed == nil || *speed != tc.speed {
			t.Fatalf("speed=%v: distance=%v speed=%v", tc.speed, meters, speed)
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

func TestAdaptiveSpeedDoesNotRearmTurnWindow(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		now := time.Now()
		a := testAdaptive(speedFunc(func(context.Context) (float64, error) { return 120, nil }), func() time.Time { return now })
		a.valid, a.speed, a.received = true, 120, now
		sink := &fakeSink{}
		c := &Controller{Sink: sink, Hold: time.Second, MaxDuration: 5 * time.Second, Repeat: repeat, FollowMeters: 150, Clock: func() time.Time { return now }, Adaptive: a}
		distance := 300.0
		event := &NavigationEvent{ContextURI: "same", Direction: "left", Stage: "follow", Distance: &distance}
		if err := c.Step(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		started := c.Started
		now = now.Add(time.Second)
		a.speed = 30
		c.Step(context.Background(), event)
		if sink.active || c.Started != started {
			t.Fatal("lower threshold failed cleanup or reset window")
		}
		now = now.Add(6 * time.Second)
		a.speed, a.received = 120, now
		c.Step(context.Background(), event)
		if sink.active || len(sink.starts) != 1 || c.Started != started {
			t.Fatal("speed oscillation rearmed exhausted turn")
		}
	}
}

func TestAdaptiveFollowCLIOptionsAndDefaults(t *testing.T) {
	base := []string{"carplay", "--base", "http://fixture/smartlight/", "--nav-base", "http://fixture/navigation/"}
	o, err := parseOptions(base, io.Discard)
	if err != nil || !o.AdaptiveFollow || o.SpeedBase != DefaultSpeedBase || o.SpeedUnit != "auto" {
		t.Fatalf("adaptive not default: %#v %v", o, err)
	}
	o, err = parseOptions(append(append([]string{}, base...), "--adaptive-follow=false"), io.Discard)
	if err != nil || o.AdaptiveFollow {
		t.Fatal("disable flag", err)
	}
	for _, args := range [][]string{
		{"--speed-uri", "http://user:pass@fixture/speed"}, {"--speed-unit", "knots"},
		{"--follow-min-distance", "600"}, {"--follow-lead-time", "NaN"},
		{"--speed-interval", "0.5"}, {"--speed-max-age", "2"},
		{"--adaptive-follow=false", "--speed-unit", "km/h"},
	} {
		if _, err := parseOptions(append(append([]string{}, base...), args...), io.Discard); err == nil {
			t.Fatalf("invalid speed flags accepted: %v", args)
		}
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
