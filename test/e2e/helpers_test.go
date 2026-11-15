//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// projectPath resolves a path relative to the repository root, so the suite runs from any directory.
func projectPath(rel string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", rel)
}

// run executes a command from the repository root and returns its combined output.
func run(name string, args ...string) (string, error) {
	return runStdin("", name, args...)
}

// runBestEffort runs a teardown command and only logs a failure, so one failed step does not skip the next.
func runBestEffort(name string, args ...string) {
	if _, err := run(name, args...); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "ignored: %v\n", err)
	}
}

func runStdin(stdin, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = projectPath("")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	loggedArgs := strings.Join(redactArgs(args), " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %s %s\n", name, loggedArgs)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, loggedArgs, err, out)
	}
	return string(out), nil
}

func mustRun(name string, args ...string) string {
	out, err := run(name, args...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return out
}

// baoToken is the admin token of the AppRole that OpenBao creates when it initializes itself.
var baoToken string

// baoArgs runs the bao CLI inside an OpenBao pod with the admin token.
func baoArgs(args ...string) []string {
	return append([]string{"-n", openbaoNamespace, "exec", "-i", "statefulset/openbao", "--",
		"env", "BAO_ADDR=" + baoActiveAddress, "BAO_CACERT=" + baoCAFile,
		"BAO_SKIP_VERIFY=false", "BAO_TOKEN=" + baoToken, "bao"}, args...)
}

func redactArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i, arg := range out {
		if strings.HasPrefix(arg, "BAO_TOKEN=") {
			out[i] = "BAO_TOKEN=[REDACTED]"
		}
	}
	return out
}

// activeNodes counts the OpenBao pods that report themselves as the raft leader.
func activeNodes() (int, error) {
	active := 0
	for i := range openbaoReplicas {
		out, err := run("kubectl", "-n", openbaoNamespace, "exec", fmt.Sprintf("openbao-%d", i), "--",
			"bao", "read", "-field=is_self", "sys/leader")
		if err != nil {
			return 0, err
		}
		if strings.TrimSpace(out) == "true" {
			active++
		}
	}
	return active, nil
}

// activeLabels counts the OpenBao pods labelled openbao-active=true, which the openbao-active Service routes to.
func activeLabels() (int, error) {
	out, err := run("kubectl", "-n", openbaoNamespace, "get", "pods", "-l", "openbao-active=true", "-o", "name")
	if err != nil {
		return 0, err
	}
	return len(strings.Fields(out)), nil
}

func baoRun(args ...string) (string, error) {
	return run("kubectl", baoArgs(args...)...)
}

func bao(args ...string) string {
	out, err := baoRun(args...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return out
}

// splitImage splits repo:tag; a registry port is not mistaken for the tag.
func splitImage(image string) (string, string) {
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i:], "/") {
		return image, "latest"
	}
	return image[:i], image[i+1:]
}

// condition returns the status of a condition type of a namespaced object.
func condition(resource, name, conditionType string) (string, error) {
	return run("kubectl", "-n", tenantNamespace, "get", resource, name,
		"-o", fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].status}`, conditionType))
}
