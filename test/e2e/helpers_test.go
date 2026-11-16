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

// tagBuiltImage names a just-built image by its id. A fixed tag leaves the previous binary running:
// the pod spec does not change, so the rollout never starts the new digest.
func tagBuiltImage(image string) string {
	id := strings.TrimSpace(mustRun("docker", "image", "inspect", "--format", "{{.Id}}", image))
	hex := strings.TrimPrefix(id, "sha256:")
	if len(hex) < 12 {
		Fail("image id is not a digest: " + id)
	}
	repo, _ := splitImage(image)
	tagged := repo + ":" + hex[:12]
	if tagged != image {
		mustRun("docker", "tag", image, tagged)
	}
	return tagged
}

// splitImage splits repo:tag; a registry port is not mistaken for the tag.
func splitImage(image string) (string, string) {
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i:], "/") {
		return image, "latest"
	}
	return image[:i], image[i+1:]
}

// condition returns the status of a condition type of an object in the tenant namespace.
func condition(resource, name, conditionType string) (string, error) {
	return conditionField(tenantNamespace, resource, name, conditionType, "status")
}

// conditionField returns one field (status, reason or message) of a condition type of an object in namespace.
func conditionField(namespace, resource, name, conditionType, field string) (string, error) {
	return run("kubectl", "-n", namespace, "get", resource, name,
		"-o", fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].%s}`, conditionType, field))
}

// dumpDiagnosticsOnFailure writes the operator and OpenBao logs, every openbao object and the events of a failed
// spec to the GinkgoWriter.
func dumpDiagnosticsOnFailure() {
	if !CurrentSpecReport().Failed() {
		return
	}
	for _, args := range [][]string{
		{"-n", operatorNamespace, "logs", "-l", "app.kubernetes.io/name=openbao-config-operator",
			"--all-containers", "--prefix", "--tail=200"},
		{"-n", openbaoNamespace, "get", "pods,pvc,services,endpointslices", "-o", "wide"},
		{"-n", openbaoNamespace, "logs", "-l", "app.kubernetes.io/name=openbao",
			"--all-containers", "--prefix", "--tail=200"},
		{"get", "openbao", "-A", "-o", "yaml"},
		{"get", "events", "-A", "--sort-by=.lastTimestamp"},
	} {
		out, _ := run("kubectl", args...)
		_, _ = fmt.Fprintf(GinkgoWriter, "kubectl %s:\n%s\n", strings.Join(args, " "), out)
	}
}

// baoAbsent succeeds when OpenBao answers a read of path in the OpenBao namespace ns ("" is the root) with not
// found. Any other failure, such as a failed exec or a denied read, is returned, so a broken check never passes as
// absence.
func baoAbsent(ns, path string) error {
	out, err := baoRun("read", "-namespace="+ns, path)
	if err == nil {
		return fmt.Errorf("%s exists in OpenBao namespace %q", path, ns)
	}
	if !strings.Contains(out, "No value found at") {
		return err
	}
	return nil
}
