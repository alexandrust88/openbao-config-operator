// Package testenv locates the external binaries that envtest and bao-backed tests need and starts bao. Outside
// CI a missing binary skips the test; with the CI environment variable set it fails it, so a CI job that lacks a
// binary it is meant to have cannot report a skipped suite as a pass.
package testenv

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// InCI reports whether the CI environment variable is set (GitHub Actions sets CI=true).
func InCI() bool { return os.Getenv("CI") != "" }

// Assets returns the envtest binary directory: $KUBEBUILDER_ASSETS, or outside CI the first bin/k8s/* below root.
// It returns "" when that directory holds no kube-apiserver; in CI only an explicit KUBEBUILDER_ASSETS counts.
func Assets(root string) string {
	dir := os.Getenv("KUBEBUILDER_ASSETS")
	if dir == "" && !InCI() {
		matches, _ := filepath.Glob(filepath.Join(root, "bin", "k8s", "*", "kube-apiserver"))
		if len(matches) > 0 {
			dir = filepath.Dir(matches[0])
		}
	}
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, "kube-apiserver")); err != nil {
		return ""
	}
	return dir
}

// RequireAssets returns Assets(root), or skips t (fails it in CI) when there is none.
func RequireAssets(t testing.TB, root string) string {
	t.Helper()
	dir := Assets(root)
	if dir == "" {
		SkipOrFail(t, "envtest binaries not found; set KUBEBUILDER_ASSETS")
	}
	return dir
}

// Bao returns $BAO_BIN, or outside CI bao from PATH; it returns "" when that file does not exist.
func Bao() string {
	bin := os.Getenv("BAO_BIN")
	if bin == "" && !InCI() {
		bin, _ = exec.LookPath("bao")
	}
	if bin == "" {
		return ""
	}
	if _, err := os.Stat(bin); err != nil {
		return ""
	}
	return bin
}

// RequireBao returns Bao(), or skips t (fails it in CI) when there is none.
func RequireBao(t testing.TB) string {
	t.Helper()
	bin := Bao()
	if bin == "" {
		SkipOrFail(t, "bao binary not found; set BAO_BIN")
	}
	return bin
}

// StartBao starts `bin server -dev` with the given root token and extra arguments on a free loopback port, kills
// it at test cleanup and returns its http address once sys/health answers 200.
func StartBao(t testing.TB, bin, rootToken string, args ...string) string {
	t.Helper()
	var lastErr error
	// The port is free when picked but can be taken before bao binds it (go test runs packages in parallel).
	for range 3 {
		addr, err := startBaoOnce(t, bin, rootToken, args)
		if err == nil {
			return addr
		}
		lastErr = err
	}
	t.Fatalf("start bao: %v", lastErr)
	return ""
}

func startBaoOnce(t testing.TB, bin, rootToken string, args []string) (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	listen := l.Addr().String()
	if err := l.Close(); err != nil {
		return "", err
	}

	output := &lockedBuffer{}
	cmd := exec.Command(bin, append([]string{"server", "-dev", "-dev-root-token-id=" + rootToken,
		"-dev-listen-address=" + listen}, args...)...)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return "", err
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	stop := func() {
		_ = cmd.Process.Kill()
		<-exited
	}

	addr := "http://" + listen
	httpClient := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return "", fmt.Errorf("bao on %s exited: %s", listen, output.String())
		default:
		}
		// bao prints its configuration banner only after it has bound every listener, so a healthy answer
		// before the banner may come from another process that won the port.
		if strings.Contains(output.String(), "server configuration:") {
			resp, err := httpClient.Get(addr + "/v1/sys/health")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					t.Cleanup(stop)
					return addr, nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	return "", fmt.Errorf("bao on %s not healthy after 30s: %s", listen, output.String())
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// SkipOrFail skips t, or fails it when the CI environment variable is set.
func SkipOrFail(t testing.TB, msg string) {
	t.Helper()
	if InCI() {
		t.Fatalf("%s (required when CI is set)", msg)
	}
	t.Skip(msg)
}
