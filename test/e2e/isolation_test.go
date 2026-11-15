//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
)

const (
	platformNamespace = "e2e-platform"
	tenantANamespace  = "e2e-tenant-a"
	tenantBNamespace  = "e2e-tenant-b"
	// baoTenantA and baoTenantB are the OpenBao namespaces of the two tenants.
	baoTenantA = "tenant-a"
	baoTenantB = "tenant-b"

	openbaoNamespaceResource = "openbaonamespaces.sys.openbaoconfig.io"

	isolationSetupTimeout   = 5 * time.Minute
	isolationReadyTimeout   = 3 * time.Minute
	isolationRefusedTimeout = 2 * time.Minute
)

// deniedPolicies are the OpenBao policies tenant-b tries to write where it must not; none may exist in the root,
// tenant-a or tenant-b OpenBao namespace.
var deniedPolicies = []string{
	"escalate-b", "app-b-cross", "app-b-dotdot", "app-b-foreign", "app-b-borrowed", "app-b-login",
	"app-b-platform", "app-b-server",
}

// deniedObjects are the tenant-b resources that must never get Ready.
var deniedObjects = [][2]string{
	{policyResource, "escalate"},
	{policyResource, "cross-namespace"},
	{policyResource, "foreign-connection"},
	{policyResource, "via-borrowed-sa"},
	{policyResource, "via-into-tenant-a"},
	{policyResource, "via-platform"},
	{policyResource, "via-a-only"},
	{kvSecretResource, "foreign-secret"},
	{connectionResource, "borrowed-sa"},
	{connectionResource, "into-tenant-a"},
	{connectionResource, "a-only"},
}

const dotdotPolicy = `apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: dotdot
  namespace: e2e-tenant-b
spec:
  connectionRef:
    name: tenant-b
  namespace: ../tenant-a
  name: app-b-dotdot
  policy: |
    path "secret/data/app" {
      capabilities = ["read"]
    }
`

const foreignSecretKVSecret = `apiVersion: secrets.openbaoconfig.io/v1alpha1
kind: KVSecret
metadata:
  name: foreign-secret
  namespace: e2e-tenant-b
spec:
  connectionRef:
    name: tenant-b
  mountPath: secret
  path: b-from-a
  kvVersion: 2
  dataFrom:
    - secretRef:
        name: app-a
        namespace: e2e-tenant-a
`

const borrowedServiceAccountConnection = `apiVersion: core.openbaoconfig.io/v1alpha1
kind: OpenBaoConnection
metadata:
  name: borrowed-sa
  namespace: e2e-tenant-b
spec:
  serverRef:
    name: e2e-isolation
  namespace: tenant-a
  auth:
    kubernetes:
      role: operator
      serviceAccountRef:
        name: obco-a
        namespace: e2e-tenant-a
      audiences:
        - openbao
`

const borrowedServiceAccountPolicy = `apiVersion: sys.openbaoconfig.io/v1alpha1
kind: Policy
metadata:
  name: via-borrowed-sa
  namespace: e2e-tenant-b
spec:
  connectionRef:
    name: borrowed-sa
  name: app-b-borrowed
  policy: |
    path "secret/data/app" {
      capabilities = ["read"]
    }
`

var _ = Describe("tenant isolation", Ordered, func() {
	AfterEach(dumpDiagnosticsOnFailure)
	AfterAll(removeIsolationTenants)

	It("provisions two tenants in Kubernetes and OpenBao through a platform ClusterOpenBaoConnection", func() {
		mustRun("kubectl", "apply", "-f", "test/e2e/testdata/isolation-platform.yaml")
		mustRun("kubectl", "wait", "--for=condition=Ready", "--timeout="+isolationSetupTimeout.String(),
			serverResource+"/e2e-isolation", serverResource+"/e2e-isolation-tenant-a-only",
			"clusteropenbaoconnections.core.openbaoconfig.io/e2e-isolation-platform")
		mustRun("kubectl", "-n", platformNamespace, "wait", "--for=condition=Ready", "--all",
			"--timeout="+isolationSetupTimeout.String(), openbaoNamespaceResource+",authmounts.sys.openbaoconfig.io,"+
				"secretsmounts.sys.openbaoconfig.io,kubernetesauthconfigs.auth.openbaoconfig.io,"+
				"kubernetesauthroles.auth.openbaoconfig.io,"+policyResource)
		for _, ns := range []string{baoTenantA, baoTenantB} {
			Expect(bao("read", "-namespace="+ns, "-field=policy", "sys/policies/acl/tenant")).
				To(ContainSubstring(`path "sys/policies/acl/app-*"`))
			Expect(bao("read", "-namespace="+ns, "-field=audience", "auth/kubernetes/role/operator")).To(Equal("openbao"))
		}
	})

	It("reconciles tenant-a's resources Ready while tenant-b's attempts on it run in parallel", func() {
		mustRun("kubectl", "apply", "-f", "test/e2e/testdata/isolation-tenants.yaml")
		for _, obj := range [][3]string{
			{tenantANamespace, connectionResource, "tenant-a"},
			{tenantANamespace, connectionResource, "a-only"},
			{tenantANamespace, policyResource, "app"},
			{tenantANamespace, kvSecretResource, "app"},
			// tenant-b's own login works, so its refusals below come from the boundary and not from a broken setup.
			{tenantBNamespace, connectionResource, "tenant-b"},
			{tenantBNamespace, policyResource, "app"},
		} {
			Eventually(conditionField).WithArguments(obj[0], obj[1], obj[2], commonv1alpha1.ConditionReady, "status").
				WithTimeout(isolationReadyTimeout).Should(Equal("True"), "%s %s/%s", obj[1], obj[0], obj[2])
		}

		Expect(bao("read", "-namespace="+baoTenantA, "-field=policy", "sys/policies/acl/app-a")).
			To(ContainSubstring(`path "secret/data/app"`))
		Expect(bao("kv", "get", "-namespace="+baoTenantA, "-mount=secret", "-field=username", "app")).To(Equal("app-a"))
		Expect(bao("read", "-namespace="+baoTenantB, "-field=policy", "sys/policies/acl/app-b")).
			To(ContainSubstring(`path "secret/data/app"`))
		for _, ns := range []string{"", baoTenantB} {
			Expect(baoAbsent(ns, "sys/policies/acl/app-a")).To(Succeed(), "tenant-a's policy leaked into %q", ns)
		}
	})

	It("refuses tenant-b writes outside its OpenBao namespace and its policy", func() {
		By("a policy outside the app-* names the tenant-b login may write")
		expectRefused(policyResource, "escalate", commonv1alpha1.ReasonPermissionDenied)
		By("spec.namespace tenant-a, which stays below tenant-b's connection namespace")
		expectRefused(policyResource, "cross-namespace",
			commonv1alpha1.ReasonPermissionDenied, commonv1alpha1.ReasonReferenceNotReady)
		By("spec.namespace ../tenant-a, which the CRD rejects")
		out, err := runStdin(dotdotPolicy, "kubectl", "apply", "-f", "-")
		Expect(err).To(HaveOccurred())
		Expect(out).To(And(ContainSubstring("spec.namespace"), ContainSubstring("should match")))
		_, err = run("kubectl", "-n", tenantBNamespace, "get", policyResource, "dotdot")
		Expect(err).To(HaveOccurred())

		for _, name := range []string{"escalate-b", "app-b-cross", "app-b-dotdot"} {
			expectNotWritten("sys/policies/acl/" + name)
		}
	})

	It("refuses tenant-b references to tenant-a's connection, Secret and ServiceAccount", func() {
		By("a connectionRef naming tenant-a's connection")
		expectRefused(policyResource, "foreign-connection", commonv1alpha1.ReasonConnectionNotReady)
		Expect(conditionField(tenantBNamespace, policyResource, "foreign-connection", commonv1alpha1.ConditionSynced,
			"message")).To(ContainSubstring("not found"))

		By("a secretRef with tenant-a's namespace: rejected, and resolved in tenant-b once the field is pruned")
		expectUnknownField(foreignSecretKVSecret, "spec.dataFrom[0].secretRef.namespace")
		Expect(run("kubectl", "-n", tenantBNamespace, "get", kvSecretResource, "foreign-secret",
			"-o", "jsonpath={.spec.dataFrom[0].secretRef}")).To(MatchJSON(`{"name":"app-a"}`))
		expectRefused(kvSecretResource, "foreign-secret", commonv1alpha1.ReasonReferenceNotReady)
		Expect(conditionField(tenantBNamespace, kvSecretResource, "foreign-secret", commonv1alpha1.ConditionSynced,
			"message")).To(ContainSubstring("secret " + tenantBNamespace + "/app-a"))
		expectNotWritten("secret/data/b-from-a")

		By("a serviceAccountRef with tenant-a's namespace: rejected, and requested in tenant-b once the field is pruned")
		expectUnknownField(borrowedServiceAccountConnection, "spec.auth.kubernetes.serviceAccountRef.namespace")
		Expect(run("kubectl", "-n", tenantBNamespace, "get", connectionResource, "borrowed-sa",
			"-o", "jsonpath={.spec.auth.kubernetes.serviceAccountRef}")).To(MatchJSON(`{"name":"obco-a"}`))
		expectConnectionRefused("borrowed-sa", corev1alpha1.ReasonAuthenticationFailed)
		Expect(conditionField(tenantBNamespace, connectionResource, "borrowed-sa", commonv1alpha1.ConditionReady,
			"message")).To(ContainSubstring("serviceaccount " + tenantBNamespace + "/obco-a"))
		mustRunStdin(borrowedServiceAccountPolicy, "kubectl", "create", "-f", "-")
		expectRefused(policyResource, "via-borrowed-sa", commonv1alpha1.ReasonConnectionNotReady)

		By("tenant-b's own ServiceAccount on tenant-a's OpenBao namespace and role")
		expectConnectionRefused("into-tenant-a", corev1alpha1.ReasonAuthenticationFailed)
		expectRefused(policyResource, "via-into-tenant-a", commonv1alpha1.ReasonConnectionNotReady)

		for _, name := range []string{"app-b-foreign", "app-b-borrowed", "app-b-login"} {
			expectNotWritten("sys/policies/acl/" + name)
		}
	})

	It("refuses tenant-b on a ClusterOpenBaoConnection and an OpenBaoServer whose allowedNamespaces exclude it", func() {
		By("the platform ClusterOpenBaoConnection, which logs in as the operator")
		expectRefused(policyResource, "via-platform", commonv1alpha1.ReasonConnectionDenied)
		Expect(conditionField(tenantBNamespace, policyResource, "via-platform", commonv1alpha1.ConditionSynced,
			"message")).To(ContainSubstring("not in allowedNamespaces"))

		By("an OpenBaoServer that admits tenant-a only, whose a-only connection in tenant-a is Ready")
		expectConnectionRefused("a-only", commonv1alpha1.ReasonConnectionDenied)
		expectRefused(policyResource, "via-a-only", commonv1alpha1.ReasonConnectionNotReady)

		for _, name := range []string{"app-b-platform", "app-b-server"} {
			expectNotWritten("sys/policies/acl/" + name)
		}
	})

	It("keeps refusing tenant-b and leaves OpenBao unchanged across drift retries", func() {
		// Drift checks are jittered by up to +20%, so the window must hold two of the longest ones.
		Consistently(isolationHolds).WithTimeout(driftInterval * 5 / 2).WithPolling(5 * time.Second).Should(Succeed())
	})

	It("removes both tenants from Kubernetes and OpenBao", func() {
		removeIsolationTenants()
		for _, ns := range []string{baoTenantA, baoTenantB} {
			Eventually(baoAbsent).WithArguments("", "sys/namespaces/"+ns).WithTimeout(isolationRefusedTimeout).
				Should(Succeed(), "OpenBao namespace %s", ns)
		}
		for _, ns := range []string{platformNamespace, tenantANamespace, tenantBNamespace} {
			_, err := run("kubectl", "get", "namespace", ns)
			Expect(err).To(HaveOccurred(), "namespace %s", ns)
		}
	})
})

// expectRefused waits until a tenant-b resource's Synced reason is one of reasons and checks it is not Ready.
func expectRefused(resource, name string, reasons ...string) {
	EventuallyWithOffset(1, conditionField).
		WithArguments(tenantBNamespace, resource, name, commonv1alpha1.ConditionSynced, "reason").
		WithTimeout(isolationRefusedTimeout).Should(BeElementOf(reasons), "%s %s", resource, name)
	ready, err := conditionField(tenantBNamespace, resource, name, commonv1alpha1.ConditionReady, "status")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, ready).To(Equal("False"), "%s %s", resource, name)
}

// expectConnectionRefused waits until a tenant-b connection reports reason and checks it never authenticated.
func expectConnectionRefused(name, reason string) {
	EventuallyWithOffset(1, conditionField).
		WithArguments(tenantBNamespace, connectionResource, name, commonv1alpha1.ConditionReady, "reason").
		WithTimeout(isolationRefusedTimeout).Should(Equal(reason), "connection %s", name)
	authenticated, err := run("kubectl", "-n", tenantBNamespace, "get", connectionResource, name,
		"-o", "jsonpath={.status.authenticated}")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, authenticated).To(Equal("false"), "connection %s", name)
}

// expectUnknownField checks that strict validation rejects manifest for field, then creates it without validation,
// which prunes the field as the API server does for every client that skips validation.
func expectUnknownField(manifest, field string) {
	out, err := runStdin(manifest, "kubectl", "create", "--validate=strict", "-f", "-")
	ExpectWithOffset(1, err).To(HaveOccurred())
	ExpectWithOffset(1, out).To(ContainSubstring(fmt.Sprintf("unknown field %q", field)))
	_, err = runStdin(manifest, "kubectl", "create", "--validate=false", "-f", "-")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

// expectNotWritten checks that path exists in none of the root, tenant-a and tenant-b OpenBao namespaces.
func expectNotWritten(path string) {
	for _, ns := range []string{"", baoTenantA, baoTenantB} {
		ExpectWithOffset(1, baoAbsent(ns, path)).To(Succeed(), "%s in OpenBao namespace %q", path, ns)
	}
}

func mustRunStdin(stdin, name string, args ...string) {
	_, err := runStdin(stdin, name, args...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

// isolationHolds reports a denied tenant-b resource that got Ready, or a denied policy in any OpenBao namespace.
func isolationHolds() error {
	for _, obj := range deniedObjects {
		ready, err := conditionField(tenantBNamespace, obj[0], obj[1], commonv1alpha1.ConditionReady, "status")
		if err != nil {
			return err
		}
		if ready != "False" {
			return fmt.Errorf("%s %s/%s has Ready %q", obj[0], tenantBNamespace, obj[1], ready)
		}
	}
	for _, ns := range []string{"", baoTenantA, baoTenantB} {
		out, err := baoRun("list", "-namespace="+ns, "-format=json", "sys/policies/acl")
		if err != nil {
			return err
		}
		var names []string
		if err := json.Unmarshal([]byte(out), &names); err != nil {
			return fmt.Errorf("list policies in OpenBao namespace %q: %w", ns, err)
		}
		for _, name := range deniedPolicies {
			if slices.Contains(names, name) {
				return fmt.Errorf("policy %s exists in OpenBao namespace %q", name, ns)
			}
		}
	}
	return baoAbsent(baoTenantB, "secret/data/b-from-a")
}

// removeIsolationTenants deletes the tenants' resources before the platform's, so the OpenBaoNamespaces still have
// their connection when they delete the tenants' OpenBao namespaces and everything in them.
func removeIsolationTenants() {
	runBestEffort("kubectl", "-n", tenantBNamespace, "delete", "--ignore-not-found", "--wait", "--timeout=2m",
		policyResource+"/via-borrowed-sa", kvSecretResource+"/foreign-secret", connectionResource+"/borrowed-sa")
	runBestEffort("kubectl", "delete", "-f", "test/e2e/testdata/isolation-tenants.yaml", "--ignore-not-found",
		"--wait", "--timeout=2m")
	runBestEffort("kubectl", "-n", platformNamespace, "delete", openbaoNamespaceResource, "--all", "--wait",
		"--timeout=3m")
	runBestEffort("kubectl", "delete", "-f", "test/e2e/testdata/isolation-platform.yaml", "--ignore-not-found",
		"--wait", "--timeout=3m")
}
