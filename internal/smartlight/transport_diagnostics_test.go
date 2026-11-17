package smartlight

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTransportErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"}, {context.Canceled, "canceled"},
		{syscall.ECONNREFUSED, "connection_refused"}, {syscall.ENETUNREACH, "network_unreachable"},
		{syscall.EHOSTUNREACH, "network_unreachable"}, {syscall.ECONNRESET, "connection_closed"},
		{io.EOF, "connection_closed"}, {&net.DNSError{Err: "fixture", Name: "secret"}, "dns_error"},
		{errors.New("secret"), "transport_failure"},
	} {
		wrapped := &url.Error{Op: "Get", URL: "http://secret.invalid/?token=secret", Err: tc.err}
		if got := transportErrorKind(wrapped); got != tc.want {
			t.Fatalf("got %s, want %s", got, tc.want)
		}
	}
}

type diagnosticTransport struct{}

func (diagnosticTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

func TestSpeedTransportLogsSourceWithoutSecrets(t *testing.T) {
	var events []Object
	s, err := NewSpeedSource("http://fixture.invalid/speed", "auto", "Bearer secret", time.Second, func(event string, fields Object) {
		if event == "transport_error" {
			events = append(events, fields)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Client.HTTP.Transport = diagnosticTransport{}
	s.Client.RetryDelay = 0
	if _, err = s.Read(context.Background()); err == nil {
		t.Fatal("expected transport failure")
	}
	if len(events) != 3 {
		t.Fatalf("attempts=%d", len(events))
	}
	for _, e := range events {
		if e["source"] != "speed" || e["error_kind"] != "connection_refused" {
			t.Fatal(e)
		}
		for _, v := range e {
			if s, ok := v.(string); ok && strings.Contains(s, "secret") {
				t.Fatal("secret logged")
			}
		}
	}
}
