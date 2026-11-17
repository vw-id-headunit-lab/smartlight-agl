package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeNoticeFixture(t *testing.T, root, path, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectGoNotices(t *testing.T) {
	root := t.TempDir()
	want := map[string]string{
		"LICENSE":                            "Go license\r\n",
		"PATENTS":                            "Go patents\n",
		"src/vendor/example.org/lib/LICENSE": "Vendored license\n",
		"src/vendor/example.org/lib/PATENTS": "Vendored patents\n",
		"src/runtime/LICENSE":                "Runtime-specific notice\n",
	}
	for path, text := range want {
		writeNoticeFixture(t, root, path, text)
	}
	writeNoticeFixture(t, root, "src/vendor/unused/LICENSE", "Must not be packaged")
	deps := []goDependency{
		{Dir: filepath.Join(root, "src", "runtime"), Standard: true},
		{Dir: filepath.Join(root, "src", "vendor", "example.org", "lib", "sub"), Standard: true},
		{Dir: filepath.Join(root, "src", "vendor", "example.org", "lib", "sub"), Standard: true},
		{Dir: filepath.Join(root, "src", "vendor", "unused"), Standard: false},
		{}, // An entry without a source directory is ignored.
	}
	notices, err := collectGoNotices(root, "go-fixture", "linux", "arm64", deps)
	if err != nil {
		t.Fatal(err)
	}
	if notices.Version != "go-fixture" || len(notices.Files) != len(want)+1 {
		t.Fatalf("unexpected notices: %#v", notices)
	}
	for _, file := range notices.Files[:len(want)] {
		path := strings.TrimPrefix(file.Path, "licenses/go/")
		text, ok := want[path]
		if !ok || !bytes.Equal(file.Data, []byte(text)) {
			t.Fatalf("unexpected or modified notice %s: %q", file.Path, file.Data)
		}
	}
	manifest := notices.Files[len(want)]
	if manifest.Path != "licenses/go/SOURCES.json" || bytes.Contains(manifest.Data, []byte(root)) {
		t.Fatalf("unexpected manifest path or host path disclosure: %s", manifest.Data)
	}
	var sources struct {
		Kind   string         `json:"kind"`
		Go     string         `json:"go_version"`
		GOOS   string         `json:"goos"`
		GOARCH string         `json:"goarch"`
		Files  []noticeDigest `json:"files"`
	}
	if err := json.Unmarshal(manifest.Data, &sources); err != nil {
		t.Fatal(err)
	}
	if sources.Go != "go-fixture" || sources.GOOS != "linux" || sources.GOARCH != "arm64" || len(sources.Files) != len(want) {
		t.Fatalf("unexpected manifest: %s", manifest.Data)
	}
	for _, file := range sources.Files {
		if file.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(want[file.Path]))) {
			t.Fatalf("incorrect digest for %s", file.Path)
		}
	}
	for i, j := 0, len(deps)-1; i < j; i, j = i+1, j-1 {
		deps[i], deps[j] = deps[j], deps[i]
	}
	reordered, err := collectGoNotices(root, "go-fixture", "linux", "arm64", deps)
	if err != nil || !reflect.DeepEqual(notices, reordered) {
		t.Fatalf("notice output depends on dependency order: %v", err)
	}
}

func TestCollectGoNoticesRejectsIncompleteToolchain(t *testing.T) {
	for _, missing := range []string{"LICENSE", "PATENTS"} {
		t.Run("missing-"+missing, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"LICENSE", "PATENTS"} {
				if name != missing {
					writeNoticeFixture(t, root, name, "notice")
				}
			}
			if _, err := collectGoNotices(root, "go-fixture", "linux", "arm64", nil); err == nil {
				t.Fatalf("missing %s accepted", missing)
			}
		})
	}
	root := t.TempDir()
	writeNoticeFixture(t, root, "LICENSE", " \n")
	writeNoticeFixture(t, root, "PATENTS", "notice")
	if _, err := collectGoNotices(root, "go-fixture", "linux", "arm64", nil); err == nil {
		t.Fatal("empty LICENSE accepted")
	}
	writeNoticeFixture(t, root, "LICENSE", "notice")
	if _, err := collectGoNotices(root, "go-fixture", "linux", "arm64", []goDependency{{Dir: t.TempDir(), Standard: true}}); err == nil {
		t.Fatal("standard-library directory outside GOROOT accepted")
	}
}

func TestAddNotice(t *testing.T) {
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	notice := noticeFile{Path: "licenses/go/LICENSE", Data: []byte("original notice\r\n")}
	if err := addNotice(tw, notice); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buffer)
	header, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "smartlight-agl/licenses/go/LICENSE" || header.Mode != 0644 || header.ModTime.Unix() != 0 || !bytes.Equal(data, notice.Data) {
		t.Fatalf("notice header/content changed: %#v, %q", header, data)
	}
}
