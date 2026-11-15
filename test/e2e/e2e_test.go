//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	serverResource     = "openbaoservers.core.openbaoconfig.io"
	connectionResource = "openbaoconnections.core.openbaoconfig.io"
	policyResource     = "policies.sys.openbaoconfig.io"
	kvSecretResource   = "kvsecrets.secrets.openbaoconfig.io"
	endpointResource   = "openbaoendpoints.core.openbaoconfig.io"
)

var _ = Describe("openbao-config-operator", Ordered, func() {
	AfterEach(func() {
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
	})

	It("verifies TLS identity and active Service routing", testVerifiedTLS)

	It("runs a highly available manager", func() {
		Eventually(func() (string, error) {
			return run("kubectl", "-n", operatorNamespace, "get", "deployment", releaseName,
				"-o", "jsonpath={.status.readyReplicas}")
		}).Should(Equal("2"))
		Eventually(func() (string, error) {
			return run("kubectl", "-n", operatorNamespace, "get", "lease", "openbao-config-operator.openbaoconfig.io",
				"-o", "jsonpath={.spec.holderIdentity}")
		}).ShouldNot(BeEmpty())
		mustRun("kubectl", "-n", operatorNamespace, "get", "secret", "openbao-config-operator-hash-key")
	})

	It("connects to OpenBao with kubernetes auth as the tenant ServiceAccount", func() {
		mustRun("kubectl", "apply", "-f", "test/e2e/testdata/tenant.yaml")
		Eventually(func() (string, error) {
			return run("kubectl", "get", serverResource, "openbao-e2e",
				"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
		}).Should(Equal("True"))
		Eventually(condition).WithArguments(connectionResource, "openbao", "Ready").Should(Equal("True"))
		Expect(run("kubectl", "-n", tenantNamespace, "get", connectionResource, "openbao",
			"-o", "jsonpath={.status.authenticated}")).To(Equal("true"))
	})

	It("writes a Policy, a KVSecret and an OpenBaoEndpoint to OpenBao", func() {
		mustRun("kubectl", "apply", "-f", "test/e2e/testdata/resources.yaml")
		for _, obj := range [][2]string{
			{policyResource, "e2e-policy"},
			{kvSecretResource, "e2e-app"},
			{endpointResource, "e2e-password-policy"},
		} {
			Eventually(condition).WithArguments(obj[0], obj[1], "Ready").Should(Equal("True"), obj[0])
		}

		Expect(bao("policy", "read", "e2e-policy")).To(ContainSubstring(`path "secret/data/e2e/app"`))
		Expect(bao("kv", "get", "-mount=secret", "-field=username", "e2e/app")).To(Equal("app"))
		Expect(bao("read", "-field=policy", "sys/policies/password/e2e-endpoint")).To(ContainSubstring("length = 24"))
	})

	It("repairs drift made directly in OpenBao", func() {
		bao("policy", "delete", "e2e-policy")
		Eventually(func() (string, error) {
			return baoRun("policy", "read", "e2e-policy")
		}).Should(ContainSubstring(`path "secret/data/e2e/app"`))
	})

	It("rewrites the KVSecret when its source Secret rotates", func() {
		mustRun("kubectl", "-n", tenantNamespace, "patch", "secret", "e2e-app", "--type=merge",
			"-p", `{"stringData":{"password":"rotated-not-real"}}`)
		Eventually(func() (string, error) {
			return baoRun("kv", "get", "-mount=secret", "-field=password", "e2e/app")
		}).Should(Equal("rotated-not-real"))
	})

	It("does not write to OpenBao in steady state", func() {
		version := func() (string, error) {
			return baoRun("read", "-field=current_version", "secret/metadata/e2e/app")
		}
		Expect(version()).To(Equal("2"))
		// Drift checks are jittered by up to +20%, so the window must hold two of the longest ones.
		Consistently(version).WithTimeout(driftInterval * 5 / 2).WithPolling(5 * time.Second).Should(Equal("2"))
	})

	It("retains data and resumes reconciliation after active OpenBao pod deletion", testOpenBaoFailover)

	It("resumes reconciliation after manager leader deletion", testManagerFailover)

	It("deletes the objects from OpenBao when the resources are deleted", func() {
		mustRun("kubectl", "-n", tenantNamespace, "delete", "--wait", "--timeout=2m",
			policyResource+"/e2e-policy", kvSecretResource+"/e2e-app", endpointResource+"/e2e-password-policy")

		_, err := baoRun("policy", "read", "e2e-policy")
		Expect(err).To(HaveOccurred())
		_, err = baoRun("kv", "metadata", "get", "-mount=secret", "e2e/app")
		Expect(err).To(HaveOccurred())
		_, err = baoRun("read", "sys/policies/password/e2e-endpoint")
		Expect(err).To(HaveOccurred())
	})
})
