//go:build e2e

package e2e

import (
	"encoding/json"
	"strconv"
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
	AfterEach(dumpDiagnosticsOnFailure)

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

	It("deletes managed objects and destroys KV history when the resources are deleted", func() {
		cutoff, err := strconv.Atoi(bao("read", "-field=current_version", "secret/metadata/e2e/app"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cutoff).To(BeNumerically(">=", 2))
		mustRun("kubectl", "-n", tenantNamespace, "delete", "--wait", "--timeout=2m",
			policyResource+"/e2e-policy", kvSecretResource+"/e2e-app", endpointResource+"/e2e-password-policy")

		_, err = baoRun("policy", "read", "e2e-policy")
		Expect(err).To(HaveOccurred())
		// ADR 0024 retains metadata: deletion destroys only history through its captured cutoff.
		var metadata struct {
			Data struct {
				CurrentVersion int `json:"current_version"`
				Versions       map[string]struct {
					Destroyed bool `json:"destroyed"`
				} `json:"versions"`
			} `json:"data"`
		}
		metadataJSON := bao("kv", "metadata", "get", "-format=json", "-mount=secret", "e2e/app")
		Expect(json.Unmarshal([]byte(metadataJSON), &metadata)).To(Succeed())
		Expect(metadata.Data.CurrentVersion).To(Equal(cutoff + 1))
		for version := 1; version <= metadata.Data.CurrentVersion; version++ {
			number := strconv.Itoa(version)
			entry, exists := metadata.Data.Versions[number]
			Expect(exists).To(BeTrue(), "retained metadata for version %s", number)
			Expect(entry.Destroyed).To(BeTrue(), "destroyed history version %s", number)
			var value struct {
				Data struct {
					Data     map[string]any `json:"data"`
					Metadata struct {
						Destroyed bool `json:"destroyed"`
						Version   int  `json:"version"`
					} `json:"metadata"`
				} `json:"data"`
			}
			// The CLI returns success for destroyed versions with null data and destruction metadata.
			out := bao("kv", "get", "-format=json", "-version="+number, "-mount=secret", "e2e/app")
			Expect(json.Unmarshal([]byte(out), &value)).To(Succeed())
			Expect(value.Data.Data).To(BeNil(), "no recoverable values in version %s", number)
			Expect(value.Data.Metadata.Destroyed).To(BeTrue())
			Expect(value.Data.Metadata.Version).To(Equal(version))
		}
		_, err = baoRun("read", "sys/policies/password/e2e-endpoint")
		Expect(err).To(HaveOccurred())
	})
})
