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
	tenantNamespace   = "e2e-tenant"
	// driftInterval is short so the suite can watch the operator repair drift.
	driftInterval = "15s"
)

var (
	managerImage = envOr("E2E_IMG", "example.com/openbao-config-operator:v0.0.1")
	kindBinary   = envOr("KIND", "kind")
	kindCluster  = envOr("KIND_CLUSTER", "openbao-config-operator-test-e2e")
	helmBinary   = envOr("HELM", "helm")
)

// TestE2E installs the Helm chart and an OpenBao dev server on the current kind cluster and drives a tenant
// connection, a Policy, a KVSecret and an OpenBaoEndpoint through create, drift repair and deletion.
// Set E2E_SKIP_IMAGE_BUILD=true when the manager image is already loaded into the cluster.
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

	By("deploying the OpenBao dev server")
	mustRun("kubectl", "apply", "-f", "hack/e2e/openbao-dev.yaml", "-f", "test/e2e/testdata/openbao-auth.yaml")
	mustRun("kubectl", "-n", openbaoNamespace, "rollout", "status", "deployment/openbao", "--timeout=180s")

	By("configuring kubernetes auth and the tenant policy in OpenBao")
	bao("auth", "enable", "kubernetes")
	bao("write", "auth/kubernetes/config", "kubernetes_host=https://kubernetes.default.svc")
	policy, err := os.ReadFile(projectPath("test/e2e/testdata/tenant-policy.hcl"))
	Expect(err).NotTo(HaveOccurred())
	baoStdin(string(policy), "policy", "write", "e2e-tenant", "-")
	bao("write", "auth/kubernetes/role/e2e-tenant",
		"bound_service_account_names=obco-tenant",
		"bound_service_account_namespaces="+tenantNamespace,
		"audience=openbao",
		"token_policies=e2e-tenant",
		"token_ttl=10m")

	By("installing the Helm chart")
	repo, tag := splitImage(managerImage)
	mustRun(helmBinary, "upgrade", "--install", releaseName, "charts/openbao-config-operator",
		"--namespace", operatorNamespace, "--create-namespace",
		"--set", "image.repository="+repo, "--set", "image.tag="+tag,
		"--set", "crds.keep=false",
		"--set", "extraArgs={--drift-interval-critical="+driftInterval+",--drift-interval-standard="+driftInterval+"}",
		"--wait", "--timeout", "5m")
})

var _ = AfterSuite(func() {
	if err := checkKindContext(); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "skipping cleanup: %v\n", err)
		return
	}
	By("removing the tenant, the chart and OpenBao")
	// Managed resources go first: deleting the namespace would remove their connection before their finalizers
	// can clean up OpenBao, and they would block the namespace with CleanupBlocked.
	runBestEffort("kubectl", "delete", "-f", "test/e2e/testdata/resources.yaml", "--ignore-not-found", "--wait", "--timeout=2m")
	runBestEffort("kubectl", "delete", "-f", "test/e2e/testdata/tenant.yaml", "--ignore-not-found", "--wait", "--timeout=2m")
	runBestEffort(helmBinary, "uninstall", releaseName, "--namespace", operatorNamespace, "--wait", "--timeout", "5m")
	runBestEffort("kubectl", "delete", "namespace", operatorNamespace, "--ignore-not-found", "--wait", "--timeout=2m")
	runBestEffort("kubectl", "delete", "-f", "hack/e2e/openbao-dev.yaml", "-f", "test/e2e/testdata/openbao-auth.yaml",
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
