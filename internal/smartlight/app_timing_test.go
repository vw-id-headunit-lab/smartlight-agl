package smartlight

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestCarPlayTimingDefaults(t *testing.T) {
	for _, command := range []string{"carplay", "monitor"} {
		t.Run(command, func(t *testing.T) {
			o, err := parseOptions([]string{command, "--base", "http://fixture/smartlight/", "--nav-base", "http://fixture/simplenavigation/"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if o.Interval != 2 || o.Hold != 3 || o.Apply {
				t.Fatalf("CarPlay defaults: interval=%v hold=%v apply=%v; want 2, 3, false", o.Interval, o.Hold, o.Apply)
			}
		})
	}
}

func TestCarPlayTimingOverrides(t *testing.T) {
	// Smaller values remain available for local mocks; the recommendation is
	// not a new validation limit and does not change the manual trigger tool.
	for _, value := range []string{"0.1", "1", "2", "3", "5"} {
		t.Run(value, func(t *testing.T) {
			want, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatal(err)
			}
			o, err := parseOptions([]string{"carplay", "--base", "http://fixture/smartlight/", "--nav-base", "http://fixture/simplenavigation/", "--interval", value, "--hold", value}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if o.Interval != want || o.Hold != want {
				t.Fatalf("timing overrides: interval=%v hold=%v; want %v", o.Interval, o.Hold, want)
			}
		})
	}
	o, err := parseOptions([]string{"trigger", "--base", "http://fixture/smartlight/", "--direction", "left"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.Hold != 2 {
		t.Fatalf("manual trigger hold=%v; want unchanged default 2", o.Hold)
	}
}

func TestCarPlayTimingHelp(t *testing.T) {
	var help bytes.Buffer
	_, err := parseOptions([]string{"carplay", "--help"}, &help)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected help sentinel; got %v", err)
	}
	for _, text := range []string{"below 2 may cause display flicker (default 2)", "below 3 may cause display flicker (default 3)"} {
		if !strings.Contains(help.String(), text) {
			t.Errorf("help missing %q: %s", text, help.String())
		}
	}
}
