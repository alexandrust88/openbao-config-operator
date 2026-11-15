//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const baoCAFile = "/openbao/tls/ca.crt"
const baoActiveAddress = "https://openbao-active.openbao.svc:8200"

func podReady(p corev1.Pod) bool {
	if p.DeletionTimestamp != nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// validateActiveTopology checks ready replicas and Service routing independently of OpenBao's leader API.
func validateActiveTopology(pods corev1.PodList, endpoints discoveryv1.EndpointSliceList) (string, error) {
	if len(pods.Items) != openbaoReplicas {
		return "", fmt.Errorf("got %d OpenBao pods, want %d", len(pods.Items), openbaoReplicas)
	}
	active := ""
	for _, p := range pods.Items {
		if !podReady(p) {
			return "", fmt.Errorf("pod %s is not ready", p.Name)
		}
		if p.Labels["openbao-active"] == "true" {
			if active != "" {
				return "", fmt.Errorf("multiple active pods: %s and %s", active, p.Name)
			}
			active = p.Name
		}
	}
	if active == "" {
		return "", fmt.Errorf("no pod has the active label")
	}
	routed := 0
	for _, slice := range endpoints.Items {
		for _, e := range slice.Endpoints {
			if e.Conditions.Ready == nil || !*e.Conditions.Ready {
				continue
			}
			if e.Conditions.Terminating != nil && *e.Conditions.Terminating {
				return "", fmt.Errorf("active Service routes to terminating endpoint")
			}
			if e.TargetRef == nil || e.TargetRef.Name != active || e.TargetRef.Kind != "Pod" {
				return "", fmt.Errorf("active Service routes outside leader %s", active)
			}
			routed++
		}
	}
	if routed == 0 {
		return "", fmt.Errorf("active Service has no ready endpoints")
	}
	return active, nil
}

func activeTopology() (string, error) {
	out, err := run("kubectl", "-n", openbaoNamespace, "get", "pods", "-l", "app.kubernetes.io/name=openbao", "-o", "json")
	if err != nil {
		return "", err
	}
	var pods corev1.PodList
	if err = json.Unmarshal([]byte(out), &pods); err != nil {
		return "", err
	}
	out, err = run("kubectl", "-n", openbaoNamespace, "get", "endpointslices", "-l", "kubernetes.io/service-name=openbao-active", "-o", "json")
	if err != nil {
		return "", err
	}
	var endpoints discoveryv1.EndpointSliceList
	if err = json.Unmarshal([]byte(out), &endpoints); err != nil {
		return "", err
	}
	active, err := validateActiveTopology(pods, endpoints)
	if err != nil {
		return "", err
	}
	for _, p := range pods.Items {
		out, err = run("kubectl", "-n", openbaoNamespace, "exec", p.Name, "--", "env",
			"BAO_ADDR=https://127.0.0.1:8200", "BAO_CACERT="+baoCAFile, "BAO_SKIP_VERIFY=false",
			"bao", "read", "-field=is_self", "sys/leader")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(out) != fmt.Sprint(p.Name == active) {
			return "", fmt.Errorf("pod %s leader response disagrees with active label", p.Name)
		}
	}
	return active, nil
}

func testVerifiedTLS() {
	Eventually(activeTopology).Should(Not(BeEmpty()))
	out, err := run("kubectl", "-n", openbaoNamespace, "exec", "statefulset/openbao", "--", "env",
		"BAO_ADDR="+baoActiveAddress, "BAO_CACERT="+baoCAFile, "BAO_SKIP_VERIFY=false",
		"BAO_TLS_SERVER_NAME=wrong-hostname.invalid", "bao", "read", "sys/leader")
	Expect(err).To(HaveOccurred())
	Expect(out).To(ContainSubstring("x509"))
	Expect(out).To(ContainSubstring("wrong-hostname.invalid"))
	out, err = run("kubectl", "-n", openbaoNamespace, "exec", "statefulset/openbao", "--", "env",
		"BAO_ADDR="+baoActiveAddress, "BAO_CACERT=", "BAO_CAPATH=", "BAO_SKIP_VERIFY=false",
		"bao", "read", "sys/leader")
	Expect(err).To(HaveOccurred())
	Expect(out).To(ContainSubstring("certificate signed by unknown authority"))
}

func testOpenBaoFailover() {
	By("recording the leader, persistent volume and replicated data before deleting the active pod")
	var oldActive string
	Eventually(func() error { var err error; oldActive, err = activeTopology(); return err }).Should(Succeed())
	uid := mustRun("kubectl", "-n", openbaoNamespace, "get", "pod", oldActive, "-o", "jsonpath={.metadata.uid}")
	pvcUID := mustRun("kubectl", "-n", openbaoNamespace, "get", "pvc", "data-"+oldActive, "-o", "jsonpath={.metadata.uid}")
	clusterID := strings.TrimSpace(bao("read", "-field=cluster_id", "sys/health"))
	Expect(clusterID).NotTo(BeEmpty())
	version := bao("read", "-field=current_version", "secret/metadata/e2e/app")
	mustRun("kubectl", "-n", openbaoNamespace, "delete", "pod", oldActive, "--wait=false")
	Eventually(func() (string, error) {
		return run("kubectl", "-n", openbaoNamespace, "get", "pods", "-l", "app.kubernetes.io/name=openbao,openbao-active=true",
			"-o", "jsonpath={.items[*].metadata.name}")
	}).Should(And(Not(BeEmpty()), Not(Equal(oldActive)), Not(ContainSubstring(" "))))
	Eventually(activeTopology).Should(Not(BeEmpty()))
	Expect(mustRun("kubectl", "-n", openbaoNamespace, "get", "pod", oldActive, "-o", "jsonpath={.metadata.uid}")).NotTo(Equal(uid))
	Expect(mustRun("kubectl", "-n", openbaoNamespace, "get", "pvc", "data-"+oldActive, "-o", "jsonpath={.metadata.uid}")).To(Equal(pvcUID))
	Expect(strings.TrimSpace(bao("read", "-field=cluster_id", "sys/health"))).To(Equal(clusterID))
	Expect(bao("read", "-field=current_version", "secret/metadata/e2e/app")).To(Equal(version))
	Expect(bao("kv", "get", "-mount=secret", "-field=password", "e2e/app")).To(Equal("rotated-not-real"))
	By("checking the existing operator client repairs drift and processes a Secret rotation after election")
	bao("policy", "delete", "e2e-policy")
	Eventually(func() (string, error) { return baoRun("policy", "read", "e2e-policy") }).Should(ContainSubstring(`path "secret/data/e2e/app"`))
	mustRun("kubectl", "-n", tenantNamespace, "patch", "secret", "e2e-app", "--type=merge", "-p", `{"stringData":{"password":"after-failover-not-real"}}`)
	Eventually(func() (string, error) { return baoRun("kv", "get", "-mount=secret", "-field=password", "e2e/app") }).Should(Equal("after-failover-not-real"))
}

func testManagerFailover() {
	holder := strings.TrimSpace(mustRun("kubectl", "-n", operatorNamespace, "get", "lease", "openbao-config-operator.openbaoconfig.io", "-o", "jsonpath={.spec.holderIdentity}"))
	pod, _, ok := strings.Cut(holder, "_")
	Expect(ok).To(BeTrue(), "leader identity must contain the pod hostname and UUID")
	mustRun("kubectl", "-n", operatorNamespace, "delete", "pod", pod, "--wait=false")
	Eventually(func() (string, error) {
		return run("kubectl", "-n", operatorNamespace, "get", "lease", "openbao-config-operator.openbaoconfig.io", "-o", "jsonpath={.spec.holderIdentity}")
	}).Should(And(Not(BeEmpty()), Not(Equal(holder))))
	bao("policy", "delete", "e2e-policy")
	Eventually(func() (string, error) { return baoRun("policy", "read", "e2e-policy") }).Should(ContainSubstring(`path "secret/data/e2e/app"`))
	Eventually(func() (string, error) {
		return run("kubectl", "-n", operatorNamespace, "get", "deployment", releaseName, "-o", "jsonpath={.status.readyReplicas}")
	}).Should(Equal("2"))
}

func TestValidateActiveTopology(t *testing.T) {
	fixture := func() (corev1.PodList, discoveryv1.EndpointSliceList) {
		var pods corev1.PodList
		for i := range openbaoReplicas {
			pods.Items = append(pods.Items, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("openbao-%d", i), Labels: map[string]string{"openbao-active": fmt.Sprint(i == 0)}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}})
		}
		ready := true
		return pods, discoveryv1.EndpointSliceList{Items: []discoveryv1.EndpointSlice{{Endpoints: []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "openbao-0"}}}}}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*corev1.PodList, *discoveryv1.EndpointSliceList)
	}{
		{"valid", nil},
		{"missing replica", func(p *corev1.PodList, _ *discoveryv1.EndpointSliceList) { p.Items = p.Items[:2] }},
		{"unready follower", func(p *corev1.PodList, _ *discoveryv1.EndpointSliceList) { p.Items[1].Status.Conditions = nil }},
		{"two leaders", func(p *corev1.PodList, _ *discoveryv1.EndpointSliceList) {
			p.Items[1].Labels["openbao-active"] = "true"
		}},
		{"no leader", func(p *corev1.PodList, _ *discoveryv1.EndpointSliceList) {
			p.Items[0].Labels["openbao-active"] = "false"
		}},
		{"routes to standby", func(_ *corev1.PodList, e *discoveryv1.EndpointSliceList) {
			e.Items[0].Endpoints[0].TargetRef.Name = "openbao-1"
		}},
		{"no endpoints", func(_ *corev1.PodList, e *discoveryv1.EndpointSliceList) { e.Items = nil }},
		{"unready endpoint", func(_ *corev1.PodList, e *discoveryv1.EndpointSliceList) {
			*e.Items[0].Endpoints[0].Conditions.Ready = false
		}},
		{"unknown endpoint", func(_ *corev1.PodList, e *discoveryv1.EndpointSliceList) { e.Items[0].Endpoints[0].TargetRef = nil }},
		{"terminating leader", func(p *corev1.PodList, _ *discoveryv1.EndpointSliceList) {
			now := metav1.Now()
			p.Items[0].DeletionTimestamp = &now
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, e := fixture()
			if tc.mutate != nil {
				tc.mutate(&p, &e)
			}
			got, err := validateActiveTopology(p, e)
			if tc.mutate == nil {
				if err != nil || got != "openbao-0" {
					t.Fatalf("got %q, %v", got, err)
				}
			} else if err == nil {
				t.Fatal("invalid topology accepted")
			}
		})
	}
}

func TestBAOCommandSecurity(t *testing.T) {
	oldToken := baoToken
	baoToken = "synthetic-test-token"
	t.Cleanup(func() { baoToken = oldToken })
	args := baoArgs("read", "sys/leader")
	joined := strings.Join(args, " ")
	for _, required := range []string{"BAO_ADDR=https://openbao-active.openbao.svc:8200", "BAO_CACERT=/openbao/tls/ca.crt", "BAO_SKIP_VERIFY=false"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing verified TLS configuration %q", required)
		}
	}
	logged := strings.Join(redactArgs(args), " ")
	if strings.Contains(logged, baoToken) {
		t.Fatal("command log exposes the token")
	}
	if !strings.Contains(strings.Join(args, " "), "BAO_TOKEN="+baoToken) {
		t.Fatal("logging redaction mutated command arguments")
	}
}
