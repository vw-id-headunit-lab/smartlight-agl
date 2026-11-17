package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type goDependency struct {
	Dir      string
	Standard bool
}

type noticeFile struct {
	Path string
	Data []byte
}

type goNotices struct {
	Version string
	Files   []noticeFile
}

type noticeDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Use the same go command and target environment as the binary build, including
// automatic toolchain selection. Do not substitute a license pinned in this repo.
func loadGoNotices(goexe string, env []string, goos, goarch string) (goNotices, error) {
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(goexe, args...)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		data, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("collect Go notices: %w: %s", err, stderr.String())
		}
		return data, nil
	}
	data, err := run("env", "-json", "GOROOT", "GOVERSION")
	if err != nil {
		return goNotices{}, err
	}
	var toolchain struct{ GOROOT, GOVERSION string }
	if err = json.Unmarshal(data, &toolchain); err != nil {
		return goNotices{}, err
	}
	if toolchain.GOROOT == "" || toolchain.GOVERSION == "" {
		return goNotices{}, fmt.Errorf("Go notice toolchain metadata is empty")
	}
	data, err = run("list", "-deps", "-json", "./cmd/smartlight-agl")
	if err != nil {
		return goNotices{}, err
	}
	var deps []goDependency
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var dep goDependency
		if err = decoder.Decode(&dep); err == io.EOF {
			break
		} else if err != nil {
			return goNotices{}, err
		}
		deps = append(deps, dep)
	}
	return collectGoNotices(toolchain.GOROOT, toolchain.GOVERSION, goos, goarch, deps)
}

func collectGoNotices(goroot, version, goos, goarch string, deps []goDependency) (goNotices, error) {
	root, err := filepath.Abs(goroot)
	if err != nil {
		return goNotices{}, err
	}
	files := make(map[string][]byte)
	read := func(path string, required bool) error {
		data, err := os.ReadFile(filepath.Join(root, path))
		if !required && os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read Go notice %s: %w", path, err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return fmt.Errorf("empty Go notice %s", path)
		}
		files[filepath.ToSlash(path)] = data
		return nil
	}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		if err = read(name, true); err != nil {
			return goNotices{}, err
		}
	}
	for _, dep := range deps {
		if !dep.Standard || dep.Dir == "" {
			continue
		}
		rel, err := filepath.Rel(root, dep.Dir)
		if err != nil || !strings.HasPrefix(filepath.ToSlash(rel), "src/") {
			return goNotices{}, fmt.Errorf("standard-library dependency is outside GOROOT/src: %s", dep.Dir)
		}
		for dir := rel; dir != "."; dir = filepath.Dir(dir) {
			for _, name := range []string{"LICENSE", "PATENTS"} {
				path := filepath.Join(dir, name)
				if _, exists := files[filepath.ToSlash(path)]; !exists {
					if err = read(path, false); err != nil {
						return goNotices{}, err
					}
				}
			}
		}
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	manifest := struct {
		Kind   string         `json:"kind"`
		Go     string         `json:"go_version"`
		GOOS   string         `json:"goos"`
		GOARCH string         `json:"goarch"`
		Files  []noticeDigest `json:"files"`
	}{Kind: "go-toolchain-notices-v1", Go: version, GOOS: goos, GOARCH: goarch}
	result := goNotices{Version: version}
	for _, path := range paths {
		data := files[path]
		manifest.Files = append(manifest.Files, noticeDigest{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
		result.Files = append(result.Files, noticeFile{Path: "licenses/go/" + path, Data: data})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return goNotices{}, err
	}
	result.Files = append(result.Files, noticeFile{Path: "licenses/go/SOURCES.json", Data: append(data, '\n')})
	return result, nil
}

func addNotice(tw *tar.Writer, notice noticeFile) error {
	header := &tar.Header{Name: "smartlight-agl/" + notice.Path, Size: int64(len(notice.Data)), Mode: 0644, ModTime: time.Unix(0, 0)}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(notice.Data)
	return err
}
