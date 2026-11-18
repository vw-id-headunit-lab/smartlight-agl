package smartlight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const responseLimit = 2 * 1024 * 1024

type Object map[string]any
type Logger func(string, Object)

func silent(string, Object) {}

// Classify without logging raw net/url errors, which may contain credentials,
// query strings, or response details. Preserve a useful distinction for diagnosis.
func transportErrorKind(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection_refused"
	}
	if errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) {
		return "network_unreachable"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns_error"
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "connection_closed"
	}
	return "transport_failure"
}

type Client struct {
	Base, Advertised *url.URL
	Auth             string
	Timeout          time.Duration
	HTTP             *http.Client
	Log              Logger
	RetryDelay       time.Duration
}

func validURI(raw string) error {
	for _, r := range raw {
		if r > 127 || r <= 32 || r == '\\' || r == 127 {
			return errors.New("service URI must be ASCII/percent-encoded without whitespace or backslashes")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid service URI")
	}
	decoded, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return errors.New("invalid URI path encoding")
	}
	for _, r := range decoded {
		if r == '\\' || r < 32 || r == 127 {
			return errors.New("invalid decoded URI path")
		}
	}
	for _, part := range strings.Split(decoded, "/") {
		if part == "." || part == ".." {
			return errors.New("URI traversal rejected")
		}
	}
	return nil
}

func baseURL(raw string) (*url.URL, error) {
	if err := validURI(raw); err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, errors.New("base must be absolute HTTP(S) without credentials/query/fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	u.RawPath = ""
	return u, nil
}

func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

func NewClient(base, advertised, auth string, timeout time.Duration, log Logger) (*Client, error) {
	u, err := baseURL(base)
	if err != nil {
		return nil, err
	}
	a := u
	if advertised != "" {
		a, err = baseURL(advertised)
		if err != nil {
			return nil, err
		}
	}
	if log == nil {
		log = silent
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Never send vehicle requests via desktop proxy variables.
	transport.MaxResponseHeaderBytes = 64 * 1024
	return &Client{Base: u, Advertised: a, Auth: auth, Timeout: timeout, Log: log, RetryDelay: 200 * time.Millisecond,
		HTTP: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Resource(name string) string { return c.Base.String() + name + "/" }

// replaceURLPathPrefix preserves escaped resource identifiers (for example an
// encoded slash) while replacing a previously checked, decoded path prefix.
func replaceURLPathPrefix(u, from, to *url.URL) {
	escaped := u.EscapedPath()
	offset := 0
	for i := 0; i < len(from.Path); i++ {
		if escaped[offset] == '%' {
			offset += 3
		} else {
			offset++
		}
	}
	u.Path = to.Path + strings.TrimPrefix(u.Path, from.Path)
	u.RawPath = to.EscapedPath() + escaped[offset:]
}

func (c *Client) resolve(raw, resource string, element bool) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("missing %s URI", resource)
	}
	if err := validURI(raw); err != nil {
		return "", err
	}
	rel, _ := url.Parse(raw)
	u := c.Advertised.ResolveReference(rel)
	expected := c.Base.Path + resource + "/"
	withinResource := func(path string) bool {
		if element {
			return strings.HasPrefix(path, expected) && strings.Trim(path[len(expected):], "/") != ""
		}
		return strings.TrimRight(path, "/") == strings.TrimRight(expected, "/")
	}
	// Explicit origin + prefix mapping, never a textual hostname substring match.
	// A saved transport URI is already mapped. Check its full resource prefix
	// first so nested Base/Advertised paths on one origin remain idempotent.
	alreadyMapped := origin(u) == origin(c.Base) && withinResource(u.Path)
	if !alreadyMapped && origin(u) == origin(c.Advertised) && strings.HasPrefix(u.Path, c.Advertised.Path) {
		clone := *u
		clone.Scheme = c.Base.Scheme
		clone.Host = c.Base.Host
		replaceURLPathPrefix(&clone, c.Advertised, c.Base)
		u = &clone
	}
	if origin(u) != origin(c.Base) || u.User != nil || u.Fragment != "" || !withinResource(u.Path) || (element && (u.RawQuery != "" || u.ForceQuery)) {
		return "", fmt.Errorf("cross-origin or out-of-resource %s URI rejected", resource)
	}
	return u.String(), nil
}

func (c *Client) Checked(raw, resource string) (string, error) { return c.resolve(raw, resource, true) }
func (c *Client) Element(row Object, resource string) (string, error) {
	return c.Checked(str(row["uri"]), resource)
}

// ServicePath returns the service-side resource reference for a JSON payload.
// The request URL uses Base, but a proxy's client-facing path is not a resource
// name on the upstream service. Validate the original reference before reversing
// the explicit prefix mapping; callers retain Checked URLs for transport/state.
func (c *Client) ServicePath(raw, resource string) (string, error) {
	target, err := c.Checked(raw, resource)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	replaceURLPathPrefix(u, c.Base, c.Advertised)
	return u.EscapedPath(), nil
}

func reference(v any) string {
	if row, ok := v.(map[string]any); ok {
		return str(row["uri"])
	}
	return str(v)
}
func str(v any) string { s, _ := v.(string); return s }

func successStatus(v any) bool {
	switch x := v.(type) {
	case string:
		return x == "success" || x == "ok" || x == "OK" || x == "200" || x == "201" || x == "204"
	case json.Number:
		return x == "200" || x == "201" || x == "204"
	}
	return false
}

func decode(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("response is not UTF-8 JSON")
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, errors.New("response is not UTF-8 JSON")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("response has trailing JSON data")
	}
	if m, ok := v.(map[string]any); ok {
		if status, present := m["status"]; present && !successStatus(status) {
			return nil, errors.New("API status is not success")
		}
	}
	return v, nil
}

func Items(v any) ([]Object, error) {
	if m, ok := v.(map[string]any); ok {
		if status, present := m["status"]; present && !successStatus(status) {
			return nil, errors.New("API status is not success")
		}
		if data, present := m["data"]; present {
			v = data
		}
	}
	if list, ok := v.([]any); ok {
		rows := make([]Object, 0, len(list))
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("collection contains non-object")
			}
			rows = append(rows, Object(m))
		}
		return rows, nil
	}
	if m, ok := v.(map[string]any); ok {
		for _, name := range []string{"id", "uri", "name"} {
			if _, present := m[name]; present {
				return []Object{m}, nil
			}
		}
	}
	return nil, errors.New("unknown response envelope")
}

type ResponseHook func(int, string) error

// Request retries transport failures for GET only. Each mutation gets one new
// request and never an idempotency key; credentials are never logged.
func (c *Client) Request(ctx context.Context, method, target string, body any, missingOK bool, hook ResponseHook) (any, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	attempts := 1
	if method == http.MethodGet {
		attempts = 3
	}
	var response *http.Response
	for attempt := 0; attempt < attempts; attempt++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		req, e := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(data))
		if e != nil {
			return nil, errors.New("invalid request URI")
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.Auth != "" {
			req.Header.Set("Authorization", c.Auth)
		}
		response, err = c.HTTP.Do(req)
		if err == nil {
			break
		}
		c.Log("transport_error", Object{"method": method, "attempt": attempt + 1, "error_kind": transportErrorKind(err)})
		if method != http.MethodGet || attempt == attempts-1 || ctx.Err() != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("transport failure on %s; no mutation retry", method)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.RetryDelay):
		}
	}
	defer response.Body.Close()
	c.Log("http", Object{"method": method, "status": response.StatusCode})
	if hook != nil {
		if err = hook(response.StatusCode, response.Header.Get("Location")); err != nil {
			return nil, err
		}
	}
	if missingOK && (response.StatusCode == 404 || response.StatusCode == 410) {
		return nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d on %s; redirects and mutation retries disabled", response.StatusCode, method)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, errors.New("reading response body failed")
	}
	if len(raw) > responseLimit {
		return nil, errors.New("response exceeds 2 MiB")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	return decode(raw)
}

func (c *Client) Collection(ctx context.Context, resource string) ([]Object, error) {
	rows := []Object{}
	seen := map[string]bool{}
	target := c.Resource(resource)
	for target != "" {
		if seen[target] || len(seen) >= 100 {
			return nil, errors.New("pagination cycle or 100 page limit")
		}
		seen[target] = true
		v, err := c.Request(ctx, http.MethodGet, target, nil, false, nil)
		if err != nil {
			return nil, err
		}
		page, err := Items(v)
		if err != nil {
			return nil, err
		}
		rows = append(rows, page...)
		target = ""
		if m, ok := v.(map[string]any); ok {
			if paging, present := m["paging"]; present && paging != nil {
				p, ok := paging.(map[string]any)
				if !ok {
					return nil, errors.New("invalid paging envelope")
				}
				if next, present := p["next"]; present && next != nil && next != "" {
					s, ok := next.(string)
					if !ok {
						return nil, errors.New("invalid paging next URI")
					}
					target, err = c.resolve(s, resource, false)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return rows, nil
}

func ResolveService(ctx context.Context, registry, service, auth string, timeout time.Duration, log Logger) (string, error) {
	c, err := NewClient(registry, "", auth, timeout, log)
	if err != nil {
		return "", err
	}
	u := *c.Base
	q := u.Query()
	q.Set("name", service)
	u.RawQuery = q.Encode()
	v, err := c.Request(ctx, http.MethodGet, u.String(), nil, false, nil)
	if err != nil {
		return "", err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return "", errors.New("registry response must contain data list")
	}
	list, ok := m["data"].([]any)
	if !ok {
		return "", errors.New("registry response must contain data list")
	}
	var matches []string
	for _, item := range list {
		r, ok := item.(map[string]any)
		if ok && str(r["name"]) == service {
			u, err := baseURL(str(r["uri"]))
			if err != nil {
				return "", err
			}
			matches = append(matches, u.String())
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("service %s must have exactly one registry match", service)
	}
	// Registry credentials are not forwarded to this returned service.
	return matches[0], nil
}
