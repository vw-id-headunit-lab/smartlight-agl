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
	Adaptive     *AdaptiveFollow
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
	rawStage := row["stage"]
	stage, stageIsString := rawStage.(string)
	deriveStage := rawStage == nil || (stageIsString && stage == "")
	if !deriveStage && !contains([]string{"follow", "prepare", "instruction", "execute"}, stage) {
		return nil, fmt.Errorf("%w: unsupported maneuver stage", ErrSnapshotChanged)
	}
	var distance *float64
	if value := row["distance"]; value != nil {
		meters, err := DistanceMeters(value, row["distanceUnit"])
		if err != nil {
			if deriveStage {
				return nil, fmt.Errorf("%w: cannot derive maneuver stage: %v", ErrSnapshotChanged, err)
			}
			return nil, err
		}
		distance = &meters
	}
	if deriveStage {
		// HCN1.1.134 can report an empty stage even at the turn.
		if distance == nil {
			return nil, fmt.Errorf("%w: cannot derive maneuver stage without distance", ErrSnapshotChanged)
		}
		if *distance <= 50 {
			stage = "execute"
		} else {
			stage = "follow"
		}
	}
	m, mok := integer(row["mainType"])
	d, dok := integer(row["directionType"])
	if !mok || !dok {
		return nil, errors.New("mainType/directionType require nonnegative integer platform values")
	}
	e := &NavigationEvent{ContextURI: contextURI, ManeuverURI: maneuverURI, ID: id, Direction: TurnDirection(row["mainType"], row["directionType"]), Stage: stage, MainType: m, DirectionType: d, Distance: distance}
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
	if s.Adaptive != nil {
		if err := s.Adaptive.Refresh(ctx); err != nil {
			return nil, err
		}
	}
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
	maneuverURI, err := s.currentManeuverURI(row)
	if err != nil {
		return nil, err
	}
	var event *NavigationEvent
	if maneuverURI != "" {
		maneuver, err := s.readElement(ctx, maneuverURI, "maneuvers")
		if err != nil {
			return nil, err
		}
		event, err = s.event(maneuver, contextURI, maneuverURI)
		if err != nil {
			return nil, err
		}
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
	confirmedURI, err := s.currentManeuverURI(confirm)
	if err != nil {
		return nil, err
	}
	if confirmedContext != contextURI || confirmedURI != maneuverURI {
		return nil, ErrSnapshotChanged
	}
	return event, nil
}

// StillCurrent reports whether the turn still qualifies for a light request.
func (s *NavigationSource) StillCurrent(ctx context.Context, previous *NavigationEvent) (bool, error) {
	sample, err := s.recheckTurn(ctx, previous)
	return sample != nil, err
}

func (s *NavigationSource) followSample() followSample {
	sample := followSample{meters: s.FollowMeters}
	if s.Adaptive != nil {
		sample.meters, sample.speed = s.Adaptive.Current()
	}
	return sample
}

// recheckTurn accepts an element ID refresh for the same semantic turn and
// returns the threshold used to authorize the POST.
func (s *NavigationSource) recheckTurn(ctx context.Context, previous *NavigationEvent) (*followSample, error) {
	if s.Adaptive != nil {
		if err := s.Adaptive.Refresh(ctx); err != nil {
			return nil, err
		}
	}
	row, err := s.readElement(ctx, previous.ContextURI, "drivingContexts")
	if err != nil {
		return nil, err
	}
	if !activeContext(row) {
		return nil, nil
	}
	contextURI, err := s.Client.Element(row, "drivingContexts")
	if err != nil {
		return nil, err
	}
	if contextURI != previous.ContextURI {
		return nil, nil
	}
	uri, err := s.currentManeuverURI(row)
	if err != nil {
		return nil, err
	}
	if uri == "" {
		return nil, nil
	}
	maneuver, err := s.readElement(ctx, uri, "maneuvers")
	if err != nil {
		return nil, err
	}
	event, err := s.event(maneuver, contextURI, uri)
	if err != nil {
		return nil, err
	}
	if event.Direction != previous.Direction || event.Stage != previous.Stage || !event.Eligible(s.followSample().meters) {
		return nil, nil
	}
	// Navigation can stop or switch its reference while the maneuver is being
	// read. Confirm this exact snapshot before allowing a new light request.
	confirm, err := s.readElement(ctx, contextURI, "drivingContexts")
	if err != nil {
		return nil, err
	}
	if !activeContext(confirm) {
		return nil, nil
	}
	confirmedContext, err := s.Client.Element(confirm, "drivingContexts")
	if err != nil {
		return nil, err
	}
	if confirmedContext != contextURI {
		return nil, nil
	}
	confirmedURI, err := s.currentManeuverURI(confirm)
	if err != nil {
		return nil, err
	}
	sample := s.followSample()
	if confirmedURI != uri || !event.Eligible(sample.meters) {
		return nil, nil
	}
	return &sample, nil
}

// Empty references are normal while a route is stopping or being recalculated.
// Malformed nonempty references still fail validation rather than being followed.
func (s *NavigationSource) currentManeuverURI(row Object) (string, error) {
	value := row["nextManeuver"]
	if value == nil {
		return "", nil
	}
	if object, ok := value.(map[string]any); ok {
		value = object["uri"]
		if value == nil {
			return "", nil
		}
	}
	raw, ok := value.(string)
	if !ok {
		return "", errors.New("nextManeuver requires a URI string or reference object")
	}
	if raw == "" {
		return "", nil
	}
	return s.Client.Checked(raw, "maneuvers")
}

type StateDirectory struct{ Path, Lock string }

// Preserve uncertain POST evidence without keeping the monitor startup blocked.
// No Location means no DELETE identity; archive rather than pretending cleanup.
func archiveIgnoredPost(path string, state *State) (string, error) {
	id, err := RandomID()
	if err != nil {
		return "", err
	}
	target := filepath.Join(filepath.Dir(path), "ignored-post-"+id+".json")
	if err = WriteState(target, state, true); err != nil {
		return "", err
	}
	if err = os.Remove(path); err != nil {
		return "", err
	}
	return target, nil
}

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
			statePath := filepath.Join(path, entry.Name())
			s, err := LoadState(statePath)
			if err != nil || s.Phase != "cleaned" {
				if err == nil && s.Kind == "smartlight-short-request-v2" && s.QueueURI == "" && contains([]string{"creating", "posted", "rejected"}, s.Phase) {
					if _, archiveErr := archiveIgnoredPost(statePath, s); archiveErr == nil {
						continue
					}
				}
				d.Close()
				return nil, &RecoveryRequired{Err: errors.New("pending state exists; run cleanup before apply")}
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
	Queued(context.Context) (bool, error)
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
	adaptive := s.Source.Adaptive
	var sample *followSample
	s.Manager.BeforePost = func(ctx context.Context) (bool, error) {
		var err error
		sample, err = s.Source.recheckTurn(ctx, event)
		if errors.Is(err, ErrSnapshotChanged) {
			s.Manager.Client.Log("skipped", Object{"reason": "navigation_changed_during_preflight", "detail": err.Error()})
		}
		return sample != nil, err
	}
	ok, err := s.Manager.Start(ctx, Selector{Direction: event.Direction}, filepath.Join(s.Directory.Path, "carplay-"+id+".json"))
	if ok && sample != nil && adaptive != nil && adaptive.Fallback > 0 && adaptive.frozen == nil {
		adaptive.frozen = sample
	}
	var postErr *PostFailure
	if !errors.As(err, &postErr) {
		return ok, err
	}
	m := s.Manager
	status := m.State.HTTPStatus
	var archived string
	if m.State.QueueURI != "" {
		// Do not inherit a canceled source context into a known-item revoke.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = m.Stop(cleanupCtx)
		cancel()
		if err != nil {
			return false, err // Preserve known identity and return recovery exit 78.
		}
	} else {
		archived, err = archiveIgnoredPost(m.Path, m.State)
		if err != nil {
			return false, &RecoveryRequired{Err: fmt.Errorf("archive failed POST evidence: %w", err)}
		}
		m.State, m.Path = nil, ""
	}
	m.Client.Log("carplay_post_ignored", Object{"direction": event.Direction, "http_status": status, "error": postErr.Error(), "evidence": archived})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return false, nil
}
func (s *NavigationSink) Queued(ctx context.Context) (bool, error) {
	if s.Manager.State == nil {
		return false, nil
	}
	rows, err := ownRows(ctx, s.Manager.Client, s.Manager.State)
	if err == nil {
		s.Manager.Client.Log("carplay_queue_check", Object{"queue_uri": s.Manager.State.QueueURI, "queued": len(rows) > 0})
	}
	return len(rows) > 0, err
}
func (s *NavigationSink) Stop(ctx context.Context) error { return s.Manager.Stop(ctx) }

type Controller struct {
	Sink               PulseSink
	Hold, MaxDuration  time.Duration
	FollowMeters       float64
	Repeat             bool
	Clock              func() time.Time
	HoldUntil, Started time.Time
	NextCheck          time.Time
	Context, Direction string
	Fired              bool
	Adaptive           *AdaptiveFollow
	WindowFollowMeters float64
	executeSeen        bool
}

func (c *Controller) followMeters() float64 {
	if c.Adaptive != nil {
		meters, _ := c.Adaptive.Current()
		return meters
	}
	return c.FollowMeters
}

func (c *Controller) Expire(ctx context.Context) error {
	if err := c.Sink.Stop(ctx); err != nil {
		return err
	}
	c.HoldUntil = time.Time{}
	c.NextCheck = time.Time{}
	return nil
}

func (c *Controller) expireIndependent() error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.Expire(cleanupCtx)
}

func (c *Controller) Step(ctx context.Context, event *NavigationEvent) error {
	if !c.HoldUntil.IsZero() && !c.Clock().Before(c.HoldUntil) {
		if err := c.Expire(ctx); err != nil {
			return err
		}
	}
	limit := c.followMeters()
	sameTurn := event != nil && event.ContextURI == c.Context && event.Direction == c.Direction
	eligible := event.Eligible(limit)
	if !sameTurn || !eligible {
		if err := c.Expire(ctx); err != nil {
			return err
		}
		wasFrozen := c.Adaptive != nil && c.Adaptive.frozen != nil
		// An attempt that did not start playback can still spend the turn's
		// budget. Speed changes alone should not rearm it.
		if sameTurn && c.Adaptive != nil && !wasFrozen && !c.Started.IsZero() && event.Eligible(c.WindowFollowMeters) {
			return nil
		}
		c.Context, c.Direction = "", ""
		c.Started = time.Time{}
		c.Fired, c.executeSeen = false, false
		c.WindowFollowMeters = 0
		if wasFrozen {
			c.Adaptive.frozen = nil
			// A different turn can appear in the same snapshot as the old
			// turn's boundary. Resume live adaptation before handling it.
			if !sameTurn && event != nil && event.Direction != "" {
				if err := c.Adaptive.Refresh(ctx); err != nil {
					return err
				}
				limit = c.followMeters()
				eligible = event.Eligible(limit)
			}
		}
		if !eligible {
			return nil
		}
		c.Context, c.Direction = event.ContextURI, event.Direction
	}
	if limit > c.WindowFollowMeters {
		c.WindowFollowMeters = limit
	}
	now := c.Clock()
	executing := event.Stage == "execute"
	if executing && !c.executeSeen {
		c.executeSeen = true
		c.Started = now
		// A skipped follow attempt cannot delay the first execute attempt.
		c.HoldUntil = time.Time{}
	}
	// Once execute begins, a return to follow cannot open a request window.
	if !executing && (c.executeSeen || !c.Repeat) {
		return nil
	}
	if c.Started.IsZero() {
		c.Started = now
	}
	windowEnd := c.Started.Add(c.MaxDuration)
	if !now.Before(windowEnd) || !c.HoldUntil.IsZero() || (c.Fired && !c.Repeat) {
		return nil
	}
	if !c.NextCheck.IsZero() {
		if now.Before(c.NextCheck) {
			return nil
		}
		// Check for service-side completion only while a replacement can fit.
		checkCtx, cancel := context.WithTimeout(ctx, windowEnd.Sub(c.Clock()))
		queued, err := c.Sink.Queued(checkCtx)
		windowExpired := errors.Is(checkCtx.Err(), context.DeadlineExceeded) || !c.Clock().Before(windowEnd)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if windowExpired {
			return nil
		}
		if err != nil {
			return err
		}
		if queued {
			c.NextCheck = c.Clock().Add(c.Hold)
			return nil
		}
		// Settle the saved receipt before starting another request, even when
		// the service has already completed it. Never infer absence by name.
		if err = c.Expire(ctx); err != nil {
			return err
		}
		if !c.Clock().Before(windowEnd) {
			return nil
		}
	}
	// Bound preflight and rechecks. An already-sent POST finishes independently
	// so an accepted request can be tracked until navigation ends.
	startCtx, cancel := context.WithTimeout(ctx, windowEnd.Sub(c.Clock()))
	started, err := c.Sink.Start(startCtx, event)
	windowExpired := errors.Is(startCtx.Err(), context.DeadlineExceeded) || !c.Clock().Before(windowEnd)
	cancel()
	var recovery *RecoveryRequired
	if errors.As(err, &recovery) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrSnapshotChanged) {
		// Preflight skipped the POST. Retry on a later poll in the same window.
		return nil
	}
	if err != nil && !windowExpired {
		return err
	}
	c.Fired = true
	if started && c.Repeat {
		c.NextCheck = c.Clock().Add(c.Hold)
	} else if started || !windowExpired {
		c.HoldUntil = c.Clock().Add(c.Hold)
	}
	return nil
}

func RunMonitor(ctx context.Context, source *NavigationSource, controller *Controller, iterations int, interval time.Duration, log Logger) (err error) {
	if log == nil {
		log = silent
	}
	cleanup := controller.expireIndependent
	defer func() {
		if e := cleanup(); e != nil {
			err = fmt.Errorf("cleanup pending: %w", e)
		}
	}()
	for count := 0; iterations == 0 || count < iterations; count++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		deadline := controller.HoldUntil
		if !deadline.IsZero() && !controller.Clock().Before(deadline) {
			if e := cleanup(); e != nil {
				return e
			}
			deadline = time.Time{}
		}
		pollCtx := ctx
		cancel := func() {}
		if !deadline.IsZero() {
			pollCtx, cancel = context.WithTimeout(ctx, deadline.Sub(controller.Clock()))
		}
		event, e := source.Poll(pollCtx)
		pollExpired := errors.Is(pollCtx.Err(), context.DeadlineExceeded)
		cancel()
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			snapshotChanged := errors.Is(e, ErrSnapshotChanged)
			if snapshotChanged {
				log("skipped", Object{"reason": "navigation_snapshot_changed", "detail": e.Error()})
			}
			if pollExpired || errors.Is(e, context.DeadlineExceeded) || snapshotChanged || (!deadline.IsZero() && !controller.Clock().Before(deadline)) {
				if e = cleanup(); e != nil {
					return e
				}
			} else {
				return e // GET request transport retry already happens within Client.
			}
		} else {
			if e = controller.Step(ctx, event); e != nil {
				return e
			}
			fields := Object{"active": event != nil}
			if source.Adaptive != nil {
				for key, value := range source.Adaptive.Fields() {
					fields[key] = value
				}
			}
			if event != nil {
				fields["stage"] = event.Stage
				fields["direction"] = event.Direction
				if event.Distance != nil {
					fields["distance_m"] = *event.Distance
				}
			}
			log("carplay_snapshot", fields)
		}
		if iterations != 0 && count+1 >= iterations {
			break
		}
		pause := interval
		if deadline := controller.HoldUntil; !deadline.IsZero() {
			if remaining := deadline.Sub(controller.Clock()); remaining < pause {
				pause = remaining
			}
		}
		if pause < 0 {
			pause = 0
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
