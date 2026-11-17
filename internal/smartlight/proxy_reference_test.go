package smartlight

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServicePathSeparatesProxyAndServiceReferences(t *testing.T) {
	for _, tc := range []struct {
		name, base, advertised, reference, wantPath, wantTransport string
	}{
		{"direct", "http://host/smartlight/", "", "/smartlight/purposes/left", "/smartlight/purposes/left", "http://host/smartlight/purposes/left"},
		{"same-prefix", "http://proxy/smartlight/", "http://service/smartlight/", "/smartlight/purposes/left", "/smartlight/purposes/left", "http://proxy/smartlight/purposes/left"},
		{"different-prefix", "http://proxy/proxy/light/", "http://service/smartlight/", "http://service/smartlight/purposes/left", "/smartlight/purposes/left", "http://proxy/proxy/light/purposes/left"},
		{"relative", "http://proxy/proxy/light/", "http://service/smartlight/", "purposes/left", "/smartlight/purposes/left", "http://proxy/proxy/light/purposes/left"},
		{"mapped-reference", "http://proxy/proxy/light/", "http://service/smartlight/", "http://proxy/proxy/light/purposes/left", "/smartlight/purposes/left", "http://proxy/proxy/light/purposes/left"},
		{"same-origin-nested-base", "http://host/smartlight/proxy/", "http://host/smartlight/", "/smartlight/purposes/left", "/smartlight/purposes/left", "http://host/smartlight/proxy/purposes/left"},
		{"same-origin-mapped-reference", "http://host/smartlight/proxy/", "http://host/smartlight/", "http://host/smartlight/proxy/purposes/left", "/smartlight/purposes/left", "http://host/smartlight/proxy/purposes/left"},
		{"same-origin-nested-advertised", "http://host/smartlight/", "http://host/smartlight/upstream/", "/smartlight/upstream/purposes/left", "/smartlight/upstream/purposes/left", "http://host/smartlight/purposes/left"},
		{"ipv6-encoded-name", "http://[::1]:8080/proxy/light/", "http://[fd00::1]:49520/smartlight/", "/smartlight/purposes/turn%20left%2B%25", "/smartlight/purposes/turn%20left%2B%25", "http://[::1]:8080/proxy/light/purposes/turn%20left%2B%25"},
		{"encoded-slash", "http://proxy/proxy/light/", "http://service/smartlight/", "/smartlight/purposes/turn%2Fleft", "/smartlight/purposes/turn%2Fleft", "http://proxy/proxy/light/purposes/turn%2Fleft"},
		{"encoded-prefix", "http://proxy/proxy%20light/", "http://service/smart%20light/", "/smart%20light/purposes/turn%2Fleft", "/smart%20light/purposes/turn%2Fleft", "http://proxy/proxy%20light/purposes/turn%2Fleft"},
		{"unicode-name", "http://proxy/proxy/light/", "http://service/smartlight/", "/smartlight/purposes/%E5%B7%A6", "/smartlight/purposes/%E5%B7%A6", "http://proxy/proxy/light/purposes/%E5%B7%A6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(tc.base, tc.advertised, "", time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			path, err := c.ServicePath(tc.reference, "purposes")
			if err != nil || path != tc.wantPath {
				t.Fatalf("ServicePath = %q, %v; want %q", path, err, tc.wantPath)
			}
			transport, err := c.Checked(tc.reference, "purposes")
			if err != nil || transport != tc.wantTransport {
				t.Fatalf("Checked = %q, %v; want %q", transport, err, tc.wantTransport)
			}
			again, err := c.Checked(transport, "purposes")
			if err != nil || again != transport {
				t.Fatalf("Checked is not idempotent: %q -> %q, %v", transport, again, err)
			}
		})
	}
}

func TestServicePathRejectsInvalidReferences(t *testing.T) {
	c, err := NewClient("http://proxy/proxy/light/", "http://service/smartlight/", "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{
		"", "http://other/smartlight/purposes/left", "http://user:pass@service/smartlight/purposes/left",
		"/smartlight/queuedpurposes/left", "/smartlight/purposes/", "/smartlight/purposes/left?x=1", "/smartlight/purposes/left?",
		"/smartlight/purposes/left#fragment", "/smartlight/purposes/../left", "/smartlight/purposes/%2e%2e/left",
		"/smartlight/purposes/%5cleft", "/smartlight/purposes/%00left", "/smartlight/purposes/%zz", "/smartlight-other/purposes/left",
	} {
		t.Run(reference, func(t *testing.T) {
			if path, err := c.ServicePath(reference, "purposes"); err == nil {
				t.Fatalf("accepted %q as %q", reference, path)
			}
		})
	}
}

func TestProxyMappedTriggerAndExactCleanup(t *testing.T) {
	for _, sameOrigin := range []bool{false, true} {
		t.Run(map[bool]string{false: "different-origin", true: "same-origin-nested"}[sameOrigin], func(t *testing.T) {
			testProxyMappedTriggerAndExactCleanup(t, sameOrigin)
		})
	}
}

func testProxyMappedTriggerAndExactCleanup(t *testing.T, sameOrigin bool) {
	f := newFixture(t)
	upstream, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxyPath := "/proxy/smartlight/"
	if sameOrigin {
		proxyPath = "/smartlight/proxy/"
	}
	proxy.Director = func(r *http.Request) {
		r.URL.Path = "/smartlight/" + strings.TrimPrefix(r.URL.Path, proxyPath)
		r.URL.RawPath = ""
		director(r)
	}
	server := httptest.NewServer(proxy)
	defer server.Close()
	advertised := f.server.URL + "/smartlight/"
	if sameOrigin {
		advertised = server.URL + "/smartlight/"
	}
	c, err := NewClient(server.URL+proxyPath, advertised, "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := &RequestManager{Client: c, Apply: true}
	path := filepath.Join(t.TempDir(), "request.json")
	ok, err := manager.Start(context.Background(), Selector{Direction: "left"}, path)
	if err != nil || !ok {
		t.Fatalf("proxy Start: ok=%v, err=%v", ok, err)
	}
	saved, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PurposeURI != server.URL+proxyPath+"purposes/left" || saved.QueueURI != server.URL+proxyPath+"queuedpurposes/created-1" {
		t.Fatalf("state did not retain transport identities: %#v", saved)
	}
	if err = manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertBaseline(t)
	snapshot := f.snapshot()
	if len(snapshot.bodies) != 1 || snapshot.bodies[0]["purpose"] != "/smartlight/purposes/left" {
		t.Fatalf("service received proxy path in payload: %#v", snapshot.bodies)
	}
	if len(snapshot.writes) != 2 || snapshot.writes[1] != "DELETE /smartlight/queuedpurposes/created-1" {
		t.Fatalf("cleanup did not use exact mapped identity: %v", snapshot.writes)
	}
	saved, err = LoadState(path)
	if err != nil || saved.Phase != "cleaned" {
		t.Fatalf("cleanup state: %#v, %v", saved, err)
	}
}

func TestRequestManagerDoesNotStartPostAfterContextEnds(t *testing.T) {
	for _, stage := range []string{"before-state", "after-state"} {
		for _, reason := range []string{"cancel", "deadline"} {
			t.Run(stage+"-"+reason, func(t *testing.T) {
				f := newFixture(t)
				var ctx context.Context
				var cancel context.CancelFunc
				wantErr := context.Canceled
				if reason == "deadline" {
					ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
					wantErr = context.DeadlineExceeded
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				endContext := func() {
					if reason == "cancel" {
						cancel()
					}
					<-ctx.Done()
				}
				c := f.client(t)
				m := &RequestManager{Client: c, Apply: true}
				if stage == "before-state" {
					m.BeforePost = func(context.Context) (bool, error) { endContext(); return true, nil }
				} else {
					c.Log = func(event string, _ Object) {
						if event == "state_saved" {
							endContext()
						}
					}
				}
				path := filepath.Join(t.TempDir(), "request.json")
				ok, err := m.Start(ctx, Selector{Direction: "left"}, path)
				if ok || !errors.Is(err, wantErr) {
					t.Fatalf("Start = %v, %v; want %v", ok, err, wantErr)
				}
				if stage == "before-state" {
					if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) || m.State != nil {
						t.Fatalf("canceled preflight created intent: state=%#v err=%v", m.State, err)
					}
				} else {
					saved, err := LoadState(path)
					if err != nil || saved.Phase != "rejected" || saved.QueueURI != "" {
						t.Fatalf("unsent intent treated as unknown POST: %#v, %v", saved, err)
					}
				}
				if err = m.Stop(context.Background()); err != nil {
					t.Fatal(err)
				}
				if len(f.snapshot().writes) != 0 {
					t.Fatalf("ended context caused mutation: %v", f.snapshot().writes)
				}
			})
		}
	}
}
