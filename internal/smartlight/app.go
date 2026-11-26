package smartlight

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Options struct {
	Action, Base, Advertised, AuthFile, State, StateDir                string
	NavBase, NavRegistry, NavAdvertised, NavAuthFile, RegistryAuthFile string
	Direction, Purpose, PurposeURI                                     string
	Hold, Timeout, Interval, MaxDuration, FollowMeters                 float64
	Apply, ConfirmAbsent, OneShot                                      bool
	Iterations                                                         int
	AdaptiveFollow                                                     bool
	SpeedURI, SpeedBase, SpeedUnit, SpeedAuthFile                      string
	FollowLead, FollowMin, FollowMax                                   float64
	SpeedInterval, SpeedMaxAge, SpeedTimeout                           float64
}

const Usage = `Usage: smartlight-agl {list|probe|trigger|cleanup|carplay} [options]

Default is read-only. Only --apply creates a queue request.
Use "smartlight-agl COMMAND --help" for command options.
CarPlay reads existing simplenavigation; it never intercepts raw iAP2.
`

const defaultStageDuration = 15

func parseOptions(args []string, stderr io.Writer) (Options, error) {
	o := Options{Hold: 2, Timeout: 3, Interval: 2, MaxDuration: defaultStageDuration, FollowMeters: 150}
	if len(args) == 0 {
		return o, errors.New("one command is required")
	}
	o.Action = args[0]
	if o.Action == "monitor" {
		o.Action = "carplay"
	}
	if !contains([]string{"list", "probe", "trigger", "cleanup", "carplay"}, o.Action) {
		return o, errors.New("unknown command")
	}
	fs := flag.NewFlagSet(o.Action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.Base, "base", "", "reachable SmartLight service base including /smartlight/")
	fs.StringVar(&o.Advertised, "advertised-base", "", "explicit SmartLight advertised prefix mapping")
	fs.StringVar(&o.AuthFile, "auth-file", "", "one-line SmartLight Authorization file")
	fs.Float64Var(&o.Timeout, "timeout", 3, "per-request timeout seconds, 0.1..15")
	if o.Action == "trigger" {
		fs.StringVar(&o.Direction, "direction", "", "left/right or 左转/右转")
		fs.StringVar(&o.Purpose, "purpose", "", "exact real purpose name or resource id")
		fs.StringVar(&o.PurposeURI, "purpose-uri", "", "exact purpose URI from service")
		fs.Float64Var(&o.Hold, "hold", 2, "client hold seconds, 0.1..5")
		fs.StringVar(&o.State, "state", "", "new durable state filename")
		fs.BoolVar(&o.Apply, "apply", false, "create one request; default dry-run")
	}
	if o.Action == "cleanup" {
		fs.StringVar(&o.State, "state", "", "recorded request state file")
		fs.BoolVar(&o.ConfirmAbsent, "confirm-absent", false, "confirm settled absence for legacy v1 only")
	}
	if o.Action == "carplay" {
		o.Timeout = 2
		fs.Lookup("timeout").DefValue = "2"
		fs.StringVar(&o.NavBase, "nav-base", "", "existing simplenavigation service URI")
		fs.StringVar(&o.NavRegistry, "nav-registry", "", "registry base; resolves GET /?name=simplenavigation")
		fs.StringVar(&o.NavAdvertised, "nav-advertised-base", "", "explicit navigation advertised prefix mapping")
		fs.StringVar(&o.NavAuthFile, "nav-auth-file", "", "navigation-only Authorization file")
		fs.StringVar(&o.RegistryAuthFile, "nav-registry-auth-file", "", "registry-only Authorization file; never inherited by service")
		fs.StringVar(&o.StateDir, "state-dir", "", "private state directory (default TMPDIR/smartlight-carplay)")
		fs.Float64Var(&o.Hold, "hold", 3, "completion-check/retry cadence or one-shot hold seconds, 0.1..5; below 3 may cause display flicker")
		fs.Float64Var(&o.Interval, "interval", 2, "poll delay seconds, 0.1..5; below 2 may cause display flicker")
		fs.Float64Var(&o.MaxDuration, "max-duration", defaultStageDuration, "time to start or replace requests in each follow/execute stage, 1..300 seconds")
		fs.Float64Var(&o.FollowMeters, "follow-distance", 150, "follow-stage threshold in meters, 0..1000; 0 disables follow trigger")
		fs.IntVar(&o.Iterations, "iterations", 0, "stop after N polls; 0 means continuous")
		fs.BoolVar(&o.Apply, "apply", false, "actually create requests; default dry-run")
		fs.BoolVar(&o.OneShot, "one-shot", false, "one execute-stage pulse per semantic turn instead of repeating")
		fs.BoolVar(&o.AdaptiveFollow, "adaptive-follow", true, "adapt follow distance using read-only VIWI speed; false disables")
		fs.StringVar(&o.SpeedURI, "speed-uri", "", "confirmed VIWI speed element/collection URI; GET only")
		fs.StringVar(&o.SpeedBase, "speed-base", DefaultSpeedBase, "VIWI service tree used when speed-uri is absent; GET only")
		fs.StringVar(&o.SpeedUnit, "speed-unit", "auto", "auto requires response unit; or confirmed km/h, mph, m/s")
		fs.StringVar(&o.SpeedAuthFile, "speed-auth-file", "", "speed-only Authorization file; no credential inheritance")
		fs.Float64Var(&o.FollowLead, "follow-lead-time", defaultFollowLead, "scale the adaptive distance curve by seconds / 12, 1..30")
		fs.Float64Var(&o.FollowMin, "follow-min-distance", defaultFollowMin, "adaptive minimum distance meters, 1..2000")
		fs.Float64Var(&o.FollowMax, "follow-max-distance", defaultFollowMax, "adaptive maximum distance meters, min..2000")
		fs.Float64Var(&o.SpeedInterval, "speed-interval", 2, "minimum delay after speed read seconds, 1..30")
		fs.Float64Var(&o.SpeedMaxAge, "speed-max-age", 5, "received speed sample max age seconds, interval+timeout..60")
		fs.Float64Var(&o.SpeedTimeout, "speed-timeout", 1, "total speed GET budget including retries seconds, 0.1..5")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return o, err
	}
	if len(fs.Args()) != 0 {
		return o, errors.New("unexpected positional arguments")
	}
	if !finiteRange(o.Timeout, .1, 15) || !finiteRange(o.Hold, .1, 5) || !finiteRange(o.Interval, .1, 5) || !finiteRange(o.MaxDuration, 1, 300) || !finiteRange(o.FollowMeters, 0, 1000) || o.Iterations < 0 {
		return o, errors.New("numeric option outside supported range")
	}
	if o.Direction == "左转" {
		o.Direction = "left"
	}
	if o.Direction == "右转" {
		o.Direction = "right"
	}
	if o.Direction != "" && o.Direction != "left" && o.Direction != "right" {
		return o, errors.New("direction must be left/right/左转/右转")
	}
	if o.Action == "trigger" {
		count := 0
		for _, s := range []string{o.Direction, o.Purpose, o.PurposeURI} {
			if s != "" {
				count++
			}
		}
		if count != 1 {
			return o, errors.New("trigger requires exactly one direction/name/URI selector")
		}
	}
	if o.Action == "cleanup" {
		if o.State == "" {
			return o, errors.New("cleanup requires --state")
		}
	} else if o.Base == "" {
		return o, errors.New("--base is required")
	}
	if o.Action == "carplay" && ((o.NavBase == "") == (o.NavRegistry == "")) {
		return o, errors.New("provide exactly one of --nav-base or --nav-registry")
	}
	if o.Action == "carplay" {
		var adaptiveFlags bool
		fs.Visit(func(f *flag.Flag) {
			if strings.HasPrefix(f.Name, "speed-") || f.Name == "follow-lead-time" || f.Name == "follow-min-distance" || f.Name == "follow-max-distance" {
				adaptiveFlags = true
			}
		})
		if adaptiveFlags && !o.AdaptiveFollow {
			return o, errors.New("speed/adaptive tuning flags require --adaptive-follow")
		}
		if o.AdaptiveFollow {
			if _, err := makeAdaptiveFollow(o, "", nil); err != nil {
				return o, err
			}
		}
	}
	return o, nil
}

func makeAdaptiveFollow(o Options, auth string, log Logger) (*AdaptiveFollow, error) {
	if !finiteRange(o.SpeedInterval, 1, 30) || !finiteRange(o.SpeedTimeout, .1, 5) || !finiteRange(o.SpeedMaxAge, o.SpeedInterval+o.SpeedTimeout, 60) {
		return nil, errors.New("speed interval/timeout/max-age outside supported range")
	}
	var source *SpeedSource
	var err error
	if o.SpeedURI != "" {
		source, err = NewSpeedSource(o.SpeedURI, o.SpeedUnit, auth, seconds(o.SpeedTimeout), log)
	} else {
		source, err = NewDiscoveringSpeedSource(o.SpeedBase, o.SpeedUnit, auth, seconds(o.SpeedTimeout), log)
	}
	if err != nil {
		return nil, err
	}
	a := &AdaptiveFollow{Source: source, Fallback: o.FollowMeters, LeadSeconds: o.FollowLead, Min: o.FollowMin, Max: o.FollowMax,
		Interval: seconds(o.SpeedInterval), MaxAge: seconds(o.SpeedMaxAge), Timeout: seconds(o.SpeedTimeout), Log: log}
	if err = a.Validate(); err != nil {
		return nil, err
	}
	return a, nil
}

func finiteRange(v, low, high float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= low && v <= high
}
func seconds(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }
func readAuth(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if s == "" || strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("Authorization file must contain exactly one nonempty header value")
	}
	return s, nil
}

// Run executes one CLI invocation. No process exit in the library, so the full
// HTTP + state + cleanup path can be tested against local httptest servers.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	log := func(event string, fields Object) {
		copy := Object{"event": event}
		for k, v := range fields {
			copy[k] = v
		}
		_ = encoder.Encode(copy)
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, Usage)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, Usage)
		return 0
	}
	o, err := parseOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		log("error", Object{"error": err.Error()})
		return 2
	}
	if err = execute(ctx, o, log); err != nil {
		log("error", Object{"error": err.Error()})
		var recovery *RecoveryRequired
		if errors.As(err, &recovery) {
			return 78
		}
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

func execute(ctx context.Context, o Options, log Logger) (err error) {
	auth, err := readAuth(o.AuthFile)
	if err != nil {
		return err
	}
	if o.Action == "cleanup" {
		s, err := LoadState(o.State)
		if err != nil {
			return err
		}
		base := o.Base
		if base == "" {
			base = s.Base
		}
		advertised := o.Advertised
		if advertised == "" {
			advertised = s.Advertised
		}
		c, err := NewClient(base, advertised, auth, seconds(o.Timeout), log)
		if err != nil {
			return err
		}
		return Cleanup(ctx, c, s, o.State, o.ConfirmAbsent)
	}
	c, err := NewClient(o.Base, o.Advertised, auth, seconds(o.Timeout), log)
	if err != nil {
		return err
	}
	if o.Action == "list" {
		rows, err := c.Collection(ctx, "purposes")
		if err != nil {
			return err
		}
		log("purposes", Object{"data": rows})
		return nil
	}
	if o.Action == "probe" {
		for _, resource := range []string{"configurations", "versions", "settinggroups", "purposes", "queuedpurposes", "players"} {
			rows, err := c.Collection(ctx, resource)
			if err != nil {
				return err
			}
			log("probe", Object{"resource": resource, "data": rows})
		}
		return nil
	}
	manager := &RequestManager{Client: c, Apply: o.Apply, Hold: seconds(o.Hold)}
	if o.Action == "trigger" {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), seconds(o.Timeout*7+1))
			defer cancel()
			if e := manager.Stop(cleanupCtx); e != nil {
				err = fmt.Errorf("cleanup pending: %w", e)
			}
		}()
		ok, err := manager.Start(ctx, Selector{Direction: o.Direction, Purpose: o.Purpose, PurposeURI: o.PurposeURI}, o.State)
		if err != nil {
			return err
		}
		if !ok && o.Apply {
			return errors.New("purpose already queued or disabled; existing request untouched")
		}
		if !o.Apply {
			return nil
		}
		rows, err := c.Collection(ctx, "players")
		if err != nil {
			return err
		}
		log("players_snapshot", Object{"players": rows, "physical_light_verified": false})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(seconds(o.Hold)):
			return nil
		}
	}
	navAuth, err := readAuth(o.NavAuthFile)
	if err != nil {
		return err
	}
	navBase := o.NavBase
	if o.NavRegistry != "" {
		registryAuth, err := readAuth(o.RegistryAuthFile)
		if err != nil {
			return err
		}
		navBase, err = ResolveService(ctx, o.NavRegistry, "simplenavigation", registryAuth, seconds(o.Timeout), log)
		if err != nil {
			return err
		}
	}
	nav, err := NewClient(navBase, o.NavAdvertised, navAuth, seconds(o.Timeout), log)
	if err != nil {
		return err
	}
	stateDir := o.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "smartlight-carplay")
	}
	directory, err := OpenStateDirectory(stateDir, o.Apply)
	if err != nil {
		return err
	}
	defer func() {
		if e := directory.Close(); e != nil && err == nil {
			err = e
		}
	}()
	source := &NavigationSource{Client: nav, FollowMeters: o.FollowMeters}
	sink := &NavigationSink{Manager: manager, Directory: directory, Source: source}
	controller := &Controller{Sink: sink, Hold: seconds(o.Hold), MaxDuration: seconds(o.MaxDuration), FollowMeters: o.FollowMeters, Repeat: !o.OneShot, Clock: time.Now}
	if o.AdaptiveFollow {
		speedAuth, err := readAuth(o.SpeedAuthFile)
		if err != nil {
			return err
		}
		adaptive, err := makeAdaptiveFollow(o, speedAuth, log)
		if err != nil {
			return err
		}
		source.Adaptive, controller.Adaptive = adaptive, adaptive
	}
	log("carplay_started", Object{"apply": o.Apply, "state_dir": stateDir, "input": "simplenavigation_http_poll", "physical_light_verified": false})
	return RunMonitor(ctx, source, controller, o.Iterations, seconds(o.Interval), log)
}
