package smartlight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrSnapshotChanged = errors.New("navigation snapshot changed")

func number(v any) (float64, bool) {
	var f float64
	var err error
	switch x := v.(type) {
	case json.Number:
		f, err = x.Float64()
	case float64:
		f = x
	case int:
		f = float64(x)
	default:
		return 0, false
	}
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}
func integer(v any) (int, bool) {
	f, ok := number(v)
	return int(f), ok && f == math.Trunc(f) && f >= 0 && f <= math.MaxInt32
}

func TurnDirection(mainType, directionType any) string {
	m, ok := integer(mainType)
	if !ok {
		return ""
	}
	d, ok := integer(directionType)
	if !ok {
		return ""
	}
	switch m {
	case 13:
		if containsInt([]int{32, 64, 96}, d) {
			return "left"
		}
		if containsInt([]int{160, 192, 224}, d) {
			return "right"
		}
	case 25, 35:
		if d == 64 {
			return "left"
		}
		if d == 192 {
			return "right"
		}
	case 16, 27:
		return "left"
	case 15, 26:
		return "right"
	}
	return ""
}

func DistanceMeters(value, unit any) (float64, error) {
	f, ok := number(value)
	if !ok || f < 0 {
		return 0, errors.New("distance must be a finite nonnegative number, not a boolean")
	}
	scale, ok := map[string]float64{"m": 1, "km": 1000, "mi": 1609.344, "yd": 0.9144, "ft": 0.3048}[str(unit)]
	if !ok {
		return 0, errors.New("unknown or missing distanceUnit")
	}
	meters := f * scale
	if math.IsInf(meters, 0) {
		return 0, errors.New("distance overflow")
	}
	return meters, nil
}

type NavigationEvent struct {
	ContextURI, ManeuverURI, ID, Direction, Stage string
	MainType, DirectionType                       int
	Distance                                      *float64
}

func (e *NavigationEvent) Eligible(followMeters float64) bool {
	return e != nil && e.Direction != "" && (e.Stage == "execute" || (followMeters > 0 && e.Stage == "follow" && e.Distance != nil && *e.Distance <= followMeters))
}

type NavigationSource struct {
	Client       *Client
	FollowMeters float64
}

func activeContext(row Object) bool {
	return str(row["technology"]) == "appleCarPlay" && row["isRouteGuidanceActive"] == true
}

func (s *NavigationSource) event(row Object, contextURI, maneuverURI string) (*NavigationEvent, error) {
	uri, err := s.Client.Element(row, "maneuvers")
	if err != nil || uri != maneuverURI {
		return nil, errors.New("maneuver identity changed")
	}
	id := str(row["id"])
	if id == "" {
		return nil, errors.New("current maneuver requires id")
	}
	stage := str(row["stage"])
	if !contains([]string{"follow", "prepare", "instruction", "execute"}, stage) {
		return nil, errors.New("stage must be a simplenavigation string")
	}
	m, mok := integer(row["mainType"])
	d, dok := integer(row["directionType"])
	if !mok || !dok {
		return nil, errors.New("mainType/directionType require nonnegative integer platform values")
	}
	e := &NavigationEvent{ContextURI: contextURI, ManeuverURI: maneuverURI, ID: id, Direction: TurnDirection(row["mainType"], row["directionType"]), Stage: stage, MainType: m, DirectionType: d}
	if value, present := row["distance"]; present && value != nil {
		meters, err := DistanceMeters(value, row["distanceUnit"])
		if err != nil {
			return nil, err
		}
		e.Distance = &meters
	}
	if stage == "follow" && e.Distance == nil {
		return nil, errors.New("follow stage requires distance and unit")
	}
	return e, nil
}

func (s *NavigationSource) readElement(ctx context.Context, uri, resource string) (Object, error) {
	v, err := s.Client.Request(ctx, http.MethodGet, uri, nil, false, nil)
	if err != nil {
		return nil, err
	}
	rows, err := Items(v)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("expected exactly one %s element", resource)
	}
	return rows[0], nil
}

func (s *NavigationSource) Poll(ctx context.Context) (*NavigationEvent, error) {
	rows, err := s.Client.Collection(ctx, "drivingContexts")
	if err != nil {
		return nil, err
	}
	var candidates []Object
	for _, row := range rows {
		if activeContext(row) {
			candidates = append(candidates, row)
		}
	}
	if len(candidates) > 1 {
		return nil, errors.New("multiple active CarPlay contexts")
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	row := candidates[0]
	contextURI, err := s.Client.Element(row, "drivingContexts")
	if err != nil {
		return nil, err
	}
	if row["nextManeuver"] == nil {
		return nil, ErrSnapshotChanged
	}
	maneuverURI, err := s.Client.Checked(reference(row["nextManeuver"]), "maneuvers")
	if err != nil {
		return nil, err
	}
	maneuver, err := s.readElement(ctx, maneuverURI, "maneuvers")
	if err != nil {
		return nil, err
	}
	event, err := s.event(maneuver, contextURI, maneuverURI)
	if err != nil {
		return nil, err
	}
	confirm, err := s.readElement(ctx, contextURI, "drivingContexts")
	if err != nil {
		return nil, err
	}
	if !activeContext(confirm) {
		return nil, ErrSnapshotChanged
	}
	confirmedContext, err := s.Client.Element(confirm, "drivingContexts")
	if err != nil {
		return nil, err
	}
	confirmedURI, err := s.Client.Checked(reference(confirm["nextManeuver"]), "maneuvers")
	if err != nil {
		return nil, err
	}
	if confirmedContext != contextURI || confirmedURI != maneuverURI {
		return nil, ErrSnapshotChanged
	}
	return event, nil
}

// StillCurrent revalidates semantic direction/proximity, allowing a refreshed
// element id for the same turn but never trusting a different origin/resource.
func (s *NavigationSource) StillCurrent(ctx context.Context, previous *NavigationEvent) (bool, error) {
	row, err := s.readElement(ctx, previous.ContextURI, "drivingContexts")
	if err != nil {
		return false, err
	}
	if !activeContext(row) {
		return false, nil
	}
	contextURI, err := s.Client.Element(row, "drivingContexts")
	if err != nil {
		return false, err
	}
	if contextURI != previous.ContextURI {
		return false, nil
	}
	uri, err := s.Client.Checked(reference(row["nextManeuver"]), "maneuvers")
	if err != nil {
		return false, err
	}
	maneuver, err := s.readElement(ctx, uri, "maneuvers")
	if err != nil {
		return false, err
	}
	event, err := s.event(maneuver, contextURI, uri)
	if err != nil {
		return false, err
	}
	return event.Direction == previous.Direction && event.Eligible(s.FollowMeters), nil
}

type StateDirectory struct{ Path, Lock string }

func OpenStateDirectory(path string, apply bool) (*StateDirectory, error) {
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("state directory must be a real directory")
	}
	d := &StateDirectory{Path: path}
	if !apply {
		return d, nil
	}
	d.Lock = filepath.Join(path, "carplay-monitor.lock")
	if err = os.Mkdir(d.Lock, 0700); err != nil {
		return nil, errors.New("monitor lock exists; inspect active process or stale lock")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		d.Close()
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "carplay-") && strings.HasSuffix(entry.Name(), ".json") {
			s, err := LoadState(filepath.Join(path, entry.Name()))
			if err != nil || s.Phase != "cleaned" {
				d.Close()
				return nil, errors.New("pending state exists; run cleanup before apply")
			}
		}
	}
	return d, nil
}
func (d *StateDirectory) Close() error {
	if d.Lock != "" {
		err := os.Remove(d.Lock)
		if err == nil {
			d.Lock = ""
		}
		return err
	}
	return nil
}

type PulseSink interface {
	Start(context.Context, *NavigationEvent) (bool, error)
	Stop(context.Context) error
}

type NavigationSink struct {
	Manager   *RequestManager
	Directory *StateDirectory
	Source    *NavigationSource
}

func (s *NavigationSink) Start(ctx context.Context, event *NavigationEvent) (bool, error) {
	id, err := RandomID()
	if err != nil {
		return false, err
	}
	s.Manager.BeforePost = func(ctx context.Context) (bool, error) { return s.Source.StillCurrent(ctx, event) }
	return s.Manager.Start(ctx, Selector{Direction: event.Direction}, filepath.Join(s.Directory.Path, "carplay-"+id+".json"))
}
func (s *NavigationSink) Stop(ctx context.Context) error { return s.Manager.Stop(ctx) }

type Controller struct {
	Sink               PulseSink
	Hold, MaxDuration  time.Duration
	FollowMeters       float64
	Repeat             bool
	Clock              func() time.Time
	Until, Started     time.Time
	Context, Direction string
	Fired              bool
}

func (c *Controller) Expire(ctx context.Context) error {
	if err := c.Sink.Stop(ctx); err != nil {
		return err
	}
	c.Until = time.Time{}
	return nil
}
func (c *Controller) Step(ctx context.Context, event *NavigationEvent) error {
	now := c.Clock()
	if !c.Until.IsZero() && !now.Before(c.Until) {
		if err := c.Expire(ctx); err != nil {
			return err
		}
	}
	if !event.Eligible(c.FollowMeters) {
		if err := c.Expire(ctx); err != nil {
			return err
		}
		c.Context = ""
		c.Direction = ""
		c.Started = time.Time{}
		c.Fired = false
		return nil
	}
	// Window follows the semantic turn. Refreshed id/raw direction variants do
	// not reset max-duration. A confirmed far/inactive/different turn does.
	if event.ContextURI != c.Context || event.Direction != c.Direction {
		if err := c.Expire(ctx); err != nil {
			return err
		}
		c.Context = event.ContextURI
		c.Direction = event.Direction
		c.Started = time.Time{}
		c.Fired = false
	}
	if !c.Started.IsZero() && now.Sub(c.Started) >= c.MaxDuration {
		return c.Expire(ctx)
	}
	if !c.Until.IsZero() {
		return nil
	}
	if c.Fired && !c.Repeat {
		return nil
	}
	if c.Started.IsZero() {
		c.Started = now
	}
	c.Fired = true
	ok, err := c.Sink.Start(ctx, event)
	if err != nil {
		return err
	}
	if ok {
		c.Until = c.Clock().Add(c.Hold)
	}
	return nil
}

func RunMonitor(ctx context.Context, source *NavigationSource, controller *Controller, iterations int, interval time.Duration, log Logger) (err error) {
	if log == nil {
		log = silent
	}
	cleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return controller.Expire(cleanupCtx)
	}
	defer func() {
		if e := cleanup(); e != nil {
			err = fmt.Errorf("cleanup pending: %w", e)
		}
	}()
	for count := 0; iterations == 0 || count < iterations; count++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		pollCtx := ctx
		cancel := func() {}
		if !controller.Until.IsZero() {
			pollCtx, cancel = context.WithDeadline(ctx, controller.Until)
		}
		event, e := source.Poll(pollCtx)
		cancel()
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(e, context.DeadlineExceeded) || (!controller.Until.IsZero() && !controller.Clock().Before(controller.Until)) {
				if e = cleanup(); e != nil {
					return e
				}
				continue
			}
			if errors.Is(e, ErrSnapshotChanged) {
				if e = cleanup(); e != nil {
					return e
				}
				continue
			}
			return e // GET request transport retry already happens within Client.
		}
		if e = controller.Step(ctx, event); e != nil {
			return e
		}
		fields := Object{"active": event != nil}
		if event != nil {
			fields["stage"] = event.Stage
			fields["direction"] = event.Direction
			if event.Distance != nil {
				fields["distance_m"] = *event.Distance
			}
		}
		log("carplay_snapshot", fields)
		if iterations != 0 && count+1 >= iterations {
			break
		}
		pause := interval
		if !controller.Until.IsZero() {
			if remaining := controller.Until.Sub(controller.Clock()); remaining < pause {
				pause = remaining
			}
		}
		if pause < 0 {
			pause = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
	}
	return nil
}
