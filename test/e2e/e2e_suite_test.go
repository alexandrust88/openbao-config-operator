//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	operatorNamespace = "openbao-config-operator"
	releaseName       = "openbao-config-operator"
	openbaoNamespace  = "openbao"
	openbaoReplicas   = 3
	tenantNamespace   = "e2e-tenant"
	// driftInterval is short so the suite can watch the operator repair drift.
	driftInterval = 10 * time.Second
)

var (
	managerImage = envOr("E2E_IMG", "example.com/openbao-config-operator:v0.0.1")
	kindBinary   = envOr("KIND", "kind")
	kindCluster  = envOr("KIND_CLUSTER", "openbao-config-operator-test-e2e")
	helmBinary   = envOr("HELM", "helm")
)

// TestE2E installs the Helm chart and a three-node OpenBao on the current kind cluster and drives a tenant
// connection, a Policy, a KVSecret and an OpenBaoEndpoint through create, drift repair and deletion.
// Set E2E_SKIP_IMAGE_BUILD=true when the manager image is already loaded into the cluster, and E2E_KEEP=true to
// leave the chart and OpenBao installed for test/livekit/kind.sh.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(3 * time.Minute)
	SetDefaultEventuallyPollingInterval(2 * time.Second)
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	Expect(checkKindContext()).To(Succeed())
	if os.Getenv("E2E_SKIP_IMAGE_BUILD") != "true" {
		By("building the manager image")
		mustRun("docker", "build", "-t", managerImage, ".")
		By("loading the manager image into kind")
		mustRun(kindBinary, "load", "docker-image", managerImage, "--name", kindCluster)
	}

	By("deploying OpenBao, which initializes kubernetes auth and the tenant policy itself")
	mustRun("kubectl", "apply", "-f", "hack/e2e/openbao.yaml", "-f", "test/e2e/testdata/openbao-auth.yaml")
	mustRun("kubectl", "-n", openbaoNamespace, "rollout", "status", "statefulset/openbao", "--timeout=300s")
	Eventually(activeNodes).Should(Equal(1))

	By("logging in to OpenBao with the admin AppRole")
	baoToken = strings.TrimSpace(mustRun("kubectl", "-n", openbaoNamespace, "exec", "statefulset/openbao", "--",
		"sh", "-c", `BAO_ADDR=http://127.0.0.1:8200 bao write -field=token auth/approle/login role_id="$E2E_ROLE_ID" secret_id="$E2E_SECRET_ID"`))

	By("installing the Helm chart")
	repo, tag := splitImage(managerImage)
	mustRun(helmBinary, "upgrade", "--install", releaseName, "charts/openbao-config-operator",
		"--namespace", operatorNamespace, "--create-namespace",
		"--set", "image.repository="+repo, "--set", "image.tag="+tag,
		"--set", "crds.keep=false",
		"--set", "extraArgs={--drift-interval-critical="+driftInterval.String()+",--drift-interval-standard="+driftInterval.String()+"}",
		"--wait", "--timeout", "5m")
})

var _ = AfterSuite(func() {
	if err := checkKindContext(); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "skipping cleanup: %v\n", err)
		return
	}
	By("removing the tenant")
	// Managed resources go first: deleting the namespace would remove their connection before their finalizers
	// can clean up OpenBao, and they would block the namespace with CleanupBlocked.
	runBestEffort("kubectl", "delete", "-f", "test/e2e/testdata/resources.yaml", "--ignore-not-found", "--wait", "--timeout=2m")
	runBestEffort("kubectl", "delete", "-f", "test/e2e/testdata/tenant.yaml", "--ignore-not-found", "--wait", "--timeout=2m")
	if os.Getenv("E2E_KEEP") == "true" {
		_, _ = fmt.Fprintln(GinkgoWriter, "E2E_KEEP=true: keeping the chart and OpenBao for test/livekit/kind.sh")
		return
	}
	By("removing the chart and OpenBao")
	runBestEffort(helmBinary, "uninstall", releaseName, "--namespace", operatorNamespace, "--wait", "--timeout", "5m")
	runBestEffort("kubectl", "delete", "namespace", operatorNamespace, "--ignore-not-found", "--wait", "--timeout=2m")
	runBestEffort("kubectl", "delete", "-f", "hack/e2e/openbao.yaml", "-f", "test/e2e/testdata/openbao-auth.yaml",
		"--ignore-not-found", "--wait", "--timeout=2m")
})

// checkKindContext keeps the suite and its cleanup off any cluster but the e2e kind cluster.
func checkKindContext() error {
	out, err := run("kubectl", "config", "current-context")
	if err != nil {
		return err
	}
	if current := strings.TrimSpace(out); current != "kind-"+kindCluster {
		return fmt.Errorf("kubectl context is %q, want kind-%s", current, kindCluster)
	}
	return nil
}
