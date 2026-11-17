package smartlight

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type State struct {
	Kind        string   `json:"kind"`
	Phase       string   `json:"phase"`
	Base        string   `json:"base"`
	Advertised  string   `json:"advertised_base,omitempty"`
	RequestName string   `json:"request_name"`
	RunID       string   `json:"run_id,omitempty"`
	QueueURI    string   `json:"queue_uri,omitempty"`
	Direction   string   `json:"direction,omitempty"`
	PurposeURI  string   `json:"purpose_uri"`
	Baseline    []string `json:"baseline_queue_uris"`
	Created     float64  `json:"created_time"`
	CleanupTime float64  `json:"cleanup_time,omitempty"`
	Hold        float64  `json:"hold_seconds,omitempty"`
	HTTPStatus  int      `json:"http_status,omitempty"`
}

// RecoveryRequired marks a known request whose ownership/cleanup needs an
// operator, not a supervisor restart loop. Mirrors boot unit exit policy 78.
type RecoveryRequired struct{ Err error }

func (e *RecoveryRequired) Error() string { return e.Err.Error() }
func (e *RecoveryRequired) Unwrap() error { return e.Err }

// PostFailure distinguishes an attempted POST from a preflight/source failure.
// Only the navigation monitor can downgrade this into an ignored pulse.
type PostFailure struct{ Err error }

func (e *PostFailure) Error() string { return e.Err.Error() }
func (e *PostFailure) Unwrap() error { return e.Err }

func RandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func WriteState(path string, s *State, exclusive bool) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("state symlink rejected")
	}
	target := path
	if !exclusive {
		id, e := RandomID()
		if e != nil {
			return e
		}
		target = path + ".next-" + id
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if !exclusive {
		defer os.Remove(target)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if !exclusive {
		return os.Rename(target, path)
	}
	return nil
}

func LoadState(path string) (*State, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s State
	d := json.NewDecoder(f)
	if err = d.Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func ownRows(ctx context.Context, c *Client, s *State) ([]string, error) {
	if s.Kind == "smartlight-short-request-v2" && s.QueueURI == "" {
		if s.Phase == "rejected" {
			return nil, nil
		}
		return nil, errors.New("POST identity unknown: retain state; never delete by shared name")
	}
	if s.QueueURI != "" {
		u, err := c.Checked(s.QueueURI, "queuedpurposes")
		if err != nil {
			return nil, err
		}
		if contains(s.Baseline, u) {
			return nil, errors.New("cleanup identity overlaps a pre-existing request")
		}
	}
	rows, err := c.Collection(ctx, "queuedpurposes")
	if err != nil {
		return nil, err
	}
	var result []string
	for _, row := range rows {
		u, err := c.Element(row, "queuedpurposes")
		if err != nil {
			return nil, err
		}
		if s.Kind == "smartlight-short-request-v2" {
			if u != s.QueueURI {
				continue
			}
		} else if str(row["name"]) != s.RequestName {
			continue
		}
		if contains(s.Baseline, u) {
			return nil, errors.New("cleanup overlaps a pre-existing request")
		}
		if str(row["name"]) != s.RequestName {
			return nil, errors.New("saved queue identity has a different name")
		}
		if ref := reference(row["purpose"]); ref != "" {
			p, err := c.Checked(ref, "purposes")
			if err != nil {
				return nil, err
			}
			if p != s.PurposeURI {
				return nil, errors.New("cleanup purpose mismatch")
			}
		}
		result = append(result, u)
	}
	if s.Kind == "smartlight-short-request-v1" && len(result) > 1 {
		return nil, errors.New("duplicate legacy request identity")
	}
	return result, nil
}

func Cleanup(ctx context.Context, c *Client, s *State, path string, confirmAbsent bool) error {
	if !contains([]string{"creating", "posted", "rejected", "cleaned"}, s.Phase) {
		return errors.New("invalid saved state phase")
	}
	if s.Kind != "smartlight-short-request-v1" && s.Kind != "smartlight-short-request-v2" {
		return errors.New("unrecognized cleanup state format")
	}
	if s.RequestName == "" {
		return errors.New("missing recorded request name")
	}
	if s.Kind == "smartlight-short-request-v1" && !regexp.MustCompile(`^agl-light-[0-9a-f]{32}$`).MatchString(s.RequestName) {
		return errors.New("invalid legacy request identity")
	}
	b, err := baseURL(s.Base)
	if err != nil || b.String() != c.Base.String() {
		return errors.New("cleanup service differs from saved service")
	}
	a := s.Advertised
	if a == "" {
		a = s.Base
	}
	u, err := baseURL(a)
	if err != nil || u.String() != c.Advertised.String() {
		return errors.New("cleanup advertised service differs")
	}
	if s.Phase == "cleaned" {
		c.Log("cleanup", Object{"result": "already_cleaned", "state": path})
		return nil
	}
	if _, err = c.Checked(s.PurposeURI, "purposes"); err != nil {
		return err
	}
	candidates, err := ownRows(ctx, c, s)
	if err != nil {
		return err
	}
	if s.Kind == "smartlight-short-request-v2" && s.QueueURI != "" {
		candidates = []string{s.QueueURI}
	}
	if len(candidates) == 0 && s.Phase == "creating" && !confirmAbsent {
		return errors.New("POST outcome uncertain; retain state")
	}
	for _, target := range candidates {
		if _, err = c.Request(ctx, http.MethodDelete, target, nil, true, nil); err != nil {
			return err
		}
	}
	remaining, err := ownRows(ctx, c, s)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return errors.New("request still visible after DELETE")
	}
	s.Phase = "cleaned"
	s.CleanupTime = float64(time.Now().UnixNano()) / 1e9
	if err = WriteState(path, s, false); err != nil {
		return err
	}
	c.Log("cleanup", Object{"result": "confirmed_absent", "deleted": len(candidates), "state": path})
	return nil
}

type Selector struct{ Direction, Purpose, PurposeURI string }

func SelectPurpose(ctx context.Context, c *Client, sel Selector) (Object, error) {
	rows, err := c.Collection(ctx, "purposes")
	if err != nil {
		return nil, err
	}
	var found []Object
	for _, row := range rows {
		u, err := c.Element(row, "purposes")
		if err != nil {
			return nil, err
		}
		match := false
		if sel.Direction != "" {
			value := row["functionValue"]
			same := str(value) == sel.Direction
			if list, ok := value.([]any); ok {
				same = len(list) == 1 && str(list[0]) == sel.Direction
			}
			match = str(row["functionCategory"]) == "navigation" && str(row["functionName"]) == "direction" && same
		} else if sel.PurposeURI != "" {
			requested, err := c.Checked(sel.PurposeURI, "purposes")
			if err != nil {
				return nil, err
			}
			match = u == requested
		} else {
			match = sel.Purpose != "" && (sel.Purpose == str(row["name"]) || sel.Purpose == str(row["id"]))
		}
		if match {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("select exactly one real purpose; matches=%d", len(found))
	}
	return found[0], nil
}

func Preflight(ctx context.Context, c *Client, purpose Object) error {
	configs, err := c.Collection(ctx, "configurations")
	if err != nil {
		return err
	}
	for _, row := range configs {
		v := str(row["smartlightState"])
		if v == "unavailable" || v == "defect" {
			return errors.New("SmartLight reports unavailable/defect")
		}
	}
	groups, err := c.Collection(ctx, "settinggroups")
	if err != nil {
		return err
	}
	ref := purpose["settingGroup"]
	ids := []string{str(ref)}
	if m, ok := ref.(map[string]any); ok {
		if v, ok := m["isActivated"].(bool); ok && !v {
			return errors.New("selected setting group is off")
		}
		ids = []string{str(m["id"]), str(m["name"]), str(m["uri"])}
	}
	for _, g := range groups {
		for _, id := range ids {
			if id != "" && (id == str(g["id"]) || id == str(g["name"]) || id == str(g["uri"])) {
				if active, ok := g["isActivated"].(bool); ok && !active {
					return errors.New("selected setting group is off")
				}
			}
		}
	}
	c.Log("preflight", Object{"switches_changed": false, "brightness_changed": false})
	return nil
}

// RequestManager owns at most one queue request and preserves its exact identity.
// On error after POST the caller MUST call Stop with a fresh cleanup context.
type RequestManager struct {
	Client     *Client
	State      *State
	Path       string
	Apply      bool
	Hold       time.Duration
	BeforePost func(context.Context) (bool, error)
}

func (m *RequestManager) Start(ctx context.Context, sel Selector, path string) (bool, error) {
	if m.State != nil {
		return false, errors.New("previous request requires cleanup")
	}
	purpose, err := SelectPurpose(ctx, m.Client, sel)
	if err != nil {
		return false, err
	}
	purposeURI, err := m.Client.Element(purpose, "purposes")
	if err != nil {
		return false, err
	}
	if err = Preflight(ctx, m.Client, purpose); err != nil {
		return false, err
	}
	if sel.Direction != "" {
		groups, err := m.Client.Collection(ctx, "settinggroups")
		if err != nil {
			return false, err
		}
		for _, g := range groups {
			if str(g["name"]) == "navigation" || str(g["name"]) == "nav_turn" {
				if active, ok := g["isActivated"].(bool); ok && !active {
					m.Client.Log("skipped", Object{"reason": "navigation_group_off"})
					return false, nil
				}
			}
		}
	}
	rows, err := m.Client.Collection(ctx, "queuedpurposes")
	if err != nil {
		return false, err
	}
	var baseline []string
	name := str(purpose["name"])
	if name == "" {
		return false, errors.New("purpose has no real name")
	}
	for _, row := range rows {
		u, err := m.Client.Element(row, "queuedpurposes")
		if err != nil {
			return false, err
		}
		baseline = append(baseline, u)
		if str(row["name"]) == name {
			m.Client.Log("skipped", Object{"reason": "purpose_already_queued"})
			return false, nil
		}
	}
	m.Client.Log("plan", Object{"apply": m.Apply, "purpose_name": name, "purpose_uri": purposeURI, "direction": sel.Direction, "physical_light_verified": false})
	if !m.Apply {
		m.Client.Log("dry_run", Object{"mutations": 0})
		return true, nil
	}
	if m.BeforePost != nil {
		ok, err := m.BeforePost(ctx)
		if err != nil {
			return false, err
		}
		if !ok {
			m.Client.Log("skipped", Object{"reason": "navigation_changed_during_preflight"})
			return false, nil
		}
	}
	id, err := RandomID()
	if err != nil {
		return false, err
	}
	if path == "" {
		path = filepath.Join(os.TempDir(), "smartlight-"+id+".json")
	}
	s := &State{Kind: "smartlight-short-request-v2", Phase: "creating", Base: m.Client.Base.String(), Advertised: m.Client.Advertised.String(), RequestName: name, RunID: id, PurposeURI: purposeURI, Baseline: baseline, Direction: sel.Direction, Created: float64(time.Now().UnixNano()) / 1e9, Hold: m.Hold.Seconds()}
	if err = WriteState(path, s, true); err != nil {
		return false, err
	}
	m.State = s
	m.Path = path
	m.Client.Log("state_saved", Object{"state": path})
	p, _ := url.Parse(purposeURI)
	hook := func(code int, location string) error {
		s.HTTPStatus = code
		if location != "" {
			u, err := m.Client.Checked(location, "queuedpurposes")
			if err != nil {
				return err
			}
			if contains(baseline, u) {
				return errors.New("POST Location overlaps pre-existing request")
			}
			s.QueueURI = u
		}
		if code >= 200 && code < 300 {
			s.Phase = "posted"
		} else if containsInt([]int{400, 401, 403, 404, 405, 415, 422}, code) && location == "" {
			s.Phase = "rejected"
		}
		return WriteState(path, s, false)
	}
	// Deferred cancellation: let this bounded POST finish and persist Location.
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.Client.Timeout)
	defer cancel()
	_, err = m.Client.Request(postCtx, http.MethodPost, m.Client.Resource("queuedpurposes"), Object{"name": name, "purpose": p.EscapedPath()}, false, hook)
	if err != nil {
		return false, &PostFailure{Err: err}
	}
	if s.QueueURI == "" {
		return false, &PostFailure{Err: errors.New("POST has no Location; retain request evidence")}
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return true, nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func (m *RequestManager) Stop(ctx context.Context) error {
	if m.State == nil {
		return nil
	}
	if err := Cleanup(ctx, m.Client, m.State, m.Path, false); err != nil {
		m.Client.Log("cleanup_pending", Object{"state": m.Path})
		return &RecoveryRequired{Err: err}
	}
	m.State = nil
	m.Path = ""
	return nil
}
