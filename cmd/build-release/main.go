// Build a static target binary and deterministic runtime-only tar.gz.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	goos := flag.String("goos", "linux", "target OS")
	arch := flag.String("goarch", "arm64", "target CPU")
	output := flag.String("output", "dist/smartlight-agl-go-linux-arm64.tar.gz", "runtime archive")
	flag.Parse()
	if *goos != "linux" && *goos != "windows" && *goos != "darwin" {
		return fmt.Errorf("unsupported release OS")
	}
	if *arch != "arm64" && *arch != "amd64" {
		return fmt.Errorf("unsupported release CPU")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return fmt.Errorf("run from repository root")
	}
	temp, err := os.MkdirTemp("", "smartlight-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	binaryName := "smartlight-agl"
	if *goos == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(temp, binaryName)
	goexe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goexe += ".exe"
	}
	cmd := exec.Command(goexe, "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", binary, "./cmd/smartlight-agl")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+*goos, "GOARCH="+*arch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		return err
	}
	file, err := os.Create(*output)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(file)
	gz.Header.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	files := []string{"trigger_light.sh", "carplay_light.sh", "rollback.sh", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "docs/carplay.md", "docs/go-migration.md", "docs/design.md", "docs/testing.md", "docs/troubleshooting.md"}
	add := func(name, path string, mode int64) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header := &tar.Header{Name: "smartlight-agl/" + name, Size: info.Size(), Mode: mode, ModTime: time.Unix(0, 0)}
		if err = tw.WriteHeader(header); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}
	if err = add(binaryName, binary, 0755); err != nil {
		return err
	}
	for _, name := range files {
		mode := int64(0644)
		if filepath.Ext(name) == ".sh" {
			mode = 0755
		}
		if err = add(name, filepath.Join(root, filepath.FromSlash(name)), mode); err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	data, err := os.ReadFile(*output)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	if err = os.WriteFile(*output+".sha256", []byte(fmt.Sprintf("%x  %s\n", hash, filepath.Base(*output))), 0644); err != nil {
		return err
	}
	fmt.Println(*output)
	fmt.Printf("%x\n", hash)
	return nil
}
