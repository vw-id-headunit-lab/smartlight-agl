package smartlight

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultSpeedBase = "http://[fd53:7cb8:383:3::99]:8080/"

// SpeedSource reads an explicit URI or discovers Displayspeed from a VIWI tree.
// Discovered references stay on the configured origin and service subtree.
type SpeedSource struct {
	Client   *Client
	URI      string
	Unit     string
	explicit bool
}

func NewSpeedSource(uri, unit, auth string, timeout time.Duration, log Logger) (*SpeedSource, error) {
	if err := validURI(uri); err != nil {
		return nil, err
	}
	u, err := url.Parse(uri)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("speed URI must be an absolute HTTP(S) element/collection without credentials/query/fragment")
	}
	if unit != "auto" {
		_, err = SpeedKPH(0, unit)
	}
	if err != nil {
		return nil, err
	}
	base := *u
	base.Path, base.RawPath = "/", ""
	speedLog := func(event string, fields Object) {
		if log != nil {
			copy := Object{"source": "speed"}
			for key, value := range fields {
				copy[key] = value
			}
			log(event, copy)
		}
	}
	c, err := NewClient(base.String(), "", auth, timeout, speedLog)
	if err != nil {
		return nil, err
	}
	return &SpeedSource{Client: c, URI: u.String(), Unit: unit, explicit: true}, nil
}

func NewDiscoveringSpeedSource(base, unit, auth string, timeout time.Duration, log Logger) (*SpeedSource, error) {
	u, err := baseURL(base)
	if err != nil {
		return nil, err
	}
	s, err := NewSpeedSource(u.String(), unit, auth, timeout, log)
	if err != nil {
		return nil, err
	}
	s.Client.Base, s.Client.Advertised = u, u
	s.URI, s.explicit = "", false
	return s, nil
}

func SpeedKPH(value any, unit string) (float64, error) {
	v, ok := number(value)
	if !ok || v < 0 {
		return 0, errors.New("speed requires a finite nonnegative JSON number")
	}
	switch unit {
	case "km/h":
	case "mph":
		v *= 1.609344
	case "m/s":
		v *= 3.6
	default:
		return 0, errors.New("speed unit must be explicitly configured as km/h, mph, or m/s")
	}
	if !finiteRange(v, 0, 300) {
		return 0, errors.New("converted speed outside supported 0..300 km/h range")
	}
	return v, nil
}

func (s *SpeedSource) Read(ctx context.Context) (float64, error) {
	if s.URI == "" {
		return s.discover(ctx)
	}
	v, err := s.Client.Request(ctx, http.MethodGet, s.URI, nil, false, nil)
	if err != nil {
		if !s.explicit {
			s.URI = ""
		}
		return 0, err
	}
	speed, err := s.parse(v)
	if err != nil && !s.explicit {
		s.URI = ""
	}
	return speed, err
}

func speedRows(v any) ([]any, error) {
	// Both a collection and a single element are accepted, without following
	// returned URIs or interpreting a missing numeric field as zero speed.
	if envelope, ok := v.(map[string]any); ok {
		if paging, ok := envelope["paging"].(map[string]any); ok && paging["next"] != nil && paging["next"] != "" {
			return nil, errors.New("paginated speed/discovery response requires an explicit unambiguous speed URI")
		}
		if data, present := envelope["data"]; present {
			v = data
		}
	}
	switch data := v.(type) {
	case []any:
		return data, nil
	case map[string]any:
		return []any{data}, nil
	default:
		return nil, errors.New("speed response requires an element or collection")
	}
}

func (s *SpeedSource) parse(v any) (float64, error) {
	rows, err := speedRows(v)
	if err != nil {
		return 0, err
	}
	var selected map[string]any
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			return 0, errors.New("speed collection contains a non-object")
		}
		if str(row["type"]) == "velocityPrimary" {
			if selected != nil {
				return 0, errors.New("speed response has multiple velocityPrimary elements")
			}
			selected = row
		}
	}
	if selected == nil {
		return 0, errors.New("speed response has no velocityPrimary element")
	}
	if raw, present := selected["isInvalid"]; present {
		invalid, ok := raw.(bool)
		if !ok || invalid {
			return 0, errors.New("speed element is marked invalid or has malformed validity")
		}
	}
	if raw, present := selected["isValid"]; present {
		valid, ok := raw.(bool)
		if !ok || !valid {
			return 0, errors.New("speed element is marked invalid or has malformed validity")
		}
	}
	unit := s.Unit
	if unit == "auto" {
		unit = str(selected["unit"])
	}
	if actual, present := selected["unit"]; present && actual != unit {
		return 0, errors.New("speed element unit differs from configured source unit")
	}
	return SpeedKPH(selected["currentValue"], unit)
}

func (s *SpeedSource) discoveredURI(raw, parent, prefix string) (string, error) {
	if err := validURI(raw); err != nil || raw == "" {
		return "", errors.New("invalid speed discovery reference")
	}
	p, _ := url.Parse(parent)
	u, _ := url.Parse(raw)
	u = p.ResolveReference(u)
	if origin(u) != origin(s.Client.Base) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !strings.HasPrefix(u.Path, prefix) {
		return "", errors.New("speed discovery reference outside configured origin/subtree")
	}
	return u.String(), nil
}

func (s *SpeedSource) discover(ctx context.Context) (float64, error) {
	root := s.Client.Base.String()
	v, err := s.Client.Request(ctx, http.MethodGet, root, nil, false, nil)
	if err != nil {
		return 0, err
	}
	rows, err := speedRows(v)
	if err != nil {
		return 0, err
	}
	var service string
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			return 0, errors.New("speed discovery has non-object service")
		}
		name := strings.Trim(str(row["name"]), "/")
		parts := strings.Split(name, "/")
		if !strings.EqualFold(parts[len(parts)-1], "Displayspeed") {
			continue
		}
		if service != "" {
			return 0, errors.New("speed discovery has ambiguous Displayspeed services")
		}
		service, err = s.discoveredURI(str(row["uri"]), root, s.Client.Base.Path)
		if err != nil {
			return 0, err
		}
	}
	if service == "" {
		return 0, errors.New("Displayspeed not found in VIWI service tree")
	}
	u, _ := url.Parse(service)
	if strings.TrimRight(u.Path, "/") == strings.TrimRight(s.Client.Base.Path, "/") {
		return 0, errors.New("Displayspeed service reference points at the discovery root")
	}
	prefix := strings.TrimRight(u.Path, "/") + "/"
	queue := []string{service}
	seen := map[string]bool{}
	// Follow only links returned by Displayspeed, with a small request budget.
	for len(queue) > 0 && len(seen) < 8 {
		target := queue[0]
		queue = queue[1:]
		if seen[target] {
			continue
		}
		seen[target] = true
		v, err = s.Client.Request(ctx, http.MethodGet, target, nil, false, nil)
		if err != nil {
			return 0, err
		}
		children, err := speedRows(v)
		if err != nil {
			return 0, err
		}
		for _, item := range children {
			row, ok := item.(map[string]any)
			if !ok {
				return 0, errors.New("speed discovery has non-object resource")
			}
			if str(row["type"]) == "velocityPrimary" {
				// A present-but-invalid or duplicate primary speed is terminal;
				// do not hide it by selecting a different child element.
				speed, err := s.parse(v)
				if err == nil {
					s.URI = target
				}
				return speed, err
			}
		}
		for _, item := range children {
			row := item.(map[string]any)
			if raw := str(row["uri"]); raw != "" {
				child, err := s.discoveredURI(raw, target, prefix)
				if err != nil {
					return 0, err
				}
				if !seen[child] && len(queue) < 8 {
					queue = append(queue, child)
				}
			}
		}
	}
	return 0, errors.New("no valid velocityPrimary speed found within discovery budget")
}

type SpeedReader interface {
	Read(context.Context) (float64, error)
}

// AdaptiveFollow is confined to the single monitor goroutine. Failed reads
// invalidate the last sample immediately; an old high speed is never reused.
type AdaptiveFollow struct {
	Source                          SpeedReader
	Fallback, LeadSeconds, Min, Max float64
	Interval, MaxAge, Timeout       time.Duration
	Clock                           func() time.Time
	Log                             Logger
	nextRead, received              time.Time
	speed                           float64
	valid                           bool
}

func (a *AdaptiveFollow) now() time.Time {
	if a.Clock != nil {
		return a.Clock()
	}
	return time.Now()
}

func (a *AdaptiveFollow) Refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.Fallback == 0 || a.now().Before(a.nextRead) {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, a.Timeout)
	speed, err := a.Source.Read(readCtx)
	if err == nil && readCtx.Err() != nil {
		err = readCtx.Err()
	}
	cancel()
	now := a.now()
	// Measure the minimum read interval from completion, not request start.
	a.nextRead = now.Add(a.Interval)
	a.valid = false
	if err == nil && !finiteRange(speed, 0, 300) {
		err = errors.New("speed reader returned an invalid km/h value")
	}
	if err == nil {
		a.speed, a.received, a.valid = speed, now, true
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if a.Log != nil {
		fields := a.Fields()
		if err != nil {
			// Do not expose URI, response body, or credentials in fallback logs.
			fields["reason"] = "read_failed_or_invalid"
			if errors.Is(err, context.DeadlineExceeded) {
				fields["reason"] = "speed_read_timeout"
			}
		}
		a.Log("adaptive_follow", fields)
	}
	return nil // Optional speed failure never stops navigation or cleanup.
}

func (a *AdaptiveFollow) Current() (meters float64, speed *float64) {
	if a.Fallback == 0 || !a.valid || a.now().Before(a.received) || a.now().Sub(a.received) >= a.MaxAge {
		return a.Fallback, nil
	}
	v := a.speed
	meters = v / 3.6 * a.LeadSeconds
	if meters < a.Min {
		meters = a.Min
	}
	if meters > a.Max {
		meters = a.Max
	}
	return meters, &v
}

func (a *AdaptiveFollow) Fields() Object {
	meters, speed := a.Current()
	fields := Object{"follow_distance_m": meters, "speed_available": speed != nil}
	if speed != nil {
		fields["speed_kph"] = *speed
	}
	return fields
}

func (a *AdaptiveFollow) Validate() error {
	if a.Source == nil || !finiteRange(a.Fallback, 0, 1000) || !finiteRange(a.LeadSeconds, 1, 30) || !finiteRange(a.Min, 1, 1000) || !finiteRange(a.Max, a.Min, 1000) || a.Interval < time.Second || a.MaxAge < a.Interval+a.Timeout || a.Timeout < 100*time.Millisecond {
		return fmt.Errorf("invalid adaptive follow configuration: fallback 0..1000m, lead 1..30s, distance 1..1000m, interval >=1s, and max-age >= interval+timeout")
	}
	return nil
}
