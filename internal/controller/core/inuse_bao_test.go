package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	inUseNS     = "inuse-tenant"
	inUseName   = "inuse"
	inUsePolicy = "sys/policies/acl/inuse-test"
)

// TestDeleteEverythingTogetherAgainstDevServer deletes a Delete-policy resource together with its connection,
// the connection's token Secret and the BaoServer, as a Namespace deletion does (envtest cannot delete
// Namespaces). The connection and server stay until the resource has removed its OpenBao object, then all go.
func TestDeleteEverythingTogetherAgainstDevServer(t *testing.T) {
	requireEnvtest(t)
	address := startDevBao(t)
	ctx := context.Background()

	mgr, err := ctrl.NewManager(envtestConfig, ctrl.Options{
		Scheme:                 connectionTestScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: new(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := SetupConnectionControllers(mgr, openbao.NewPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetupBaoEndpoint(mgr, resolver, engine.Options{DriftIntervalCritical: time.Minute, DriftIntervalStandard: time.Minute}); err != nil {
		t.Fatal(err)
	}
	mgrCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Logf("manager: %v", err)
		}
	})

	server := &corev1alpha1.BaoServer{
		ObjectMeta: metav1.ObjectMeta{Name: inUseName},
		Spec: corev1alpha1.BaoServerSpec{
			Address:           address,
			AllowInsecureHTTP: true,
			AllowedNamespaces: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: inUseName}},
		},
	}
	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: litRoot, Namespace: inUseNS},
		Data:       map[string][]byte{tokenKey: []byte(litRoot)},
	}
	conn := &corev1alpha1.BaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: inUseName, Namespace: inUseNS},
		Spec: corev1alpha1.BaoConnectionSpec{
			ConnectionSpec: corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: inUseName}},
			Auth: corev1alpha1.ConnectionAuth{Token: &corev1alpha1.TokenAuth{
				SecretRef: commonv1alpha1.SecretKeySelector{Name: litRoot, Key: tokenKey},
			}},
		},
	}
	endpoint := &corev1alpha1.BaoEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: inUseName, Namespace: inUseNS,
			Annotations: map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}},
		Spec: corev1alpha1.BaoEndpointSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{
				ConnectionRef:  commonv1alpha1.ConnectionReference{Name: inUseName},
				DeletionPolicy: commonv1alpha1.DeletionPolicyDelete,
			},
			Path: inUsePolicy,
			Data: &runtime.RawExtension{Raw: []byte(`{"policy":"path \"secret/*\" { capabilities = [\"read\"] }"}`)},
		},
	}
	objs := []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: inUseNS, Labels: map[string]string{tenantLabel: inUseName}}},
		server, token, conn, endpoint,
	}
	for _, obj := range objs {
		if err := envtestClient.Create(ctx, obj); err != nil {
			t.Fatalf("create %s: %v", obj.GetName(), err)
		}
	}

	root := injectedRootClient(t, address)
	eventually(t, "the endpoint to be Ready and its policy written", func() bool {
		got := &corev1alpha1.BaoEndpoint{}
		if err := envtestClient.Get(ctx, client.ObjectKeyFromObject(endpoint), got); err != nil {
			return false
		}
		_, err := root.Read(ctx, inUsePolicy)
		return meta.IsStatusConditionTrue(got.Status.Conditions, commonv1alpha1.ConditionReady) && err == nil
	})
	gauge := connectionReady.WithLabelValues(string(commonv1alpha1.ConnectionKindNamespaced), inUseNS, inUseName)
	if v := testutil.ToFloat64(gauge); v != 1 {
		t.Errorf("obco_connection_ready = %v, want 1", v)
	}

	for _, obj := range []client.Object{token, server, conn, endpoint} {
		if err := envtestClient.Delete(ctx, obj); err != nil {
			t.Fatalf("delete %s: %v", obj.GetName(), err)
		}
	}
	eventually(t, "the endpoint, connection and server to be gone", func() bool {
		for _, obj := range []client.Object{endpoint, conn, server} {
			if err := envtestClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
				return false
			}
		}
		return true
	})
	if _, err := root.Read(ctx, inUsePolicy); !errors.Is(err, openbao.ErrNotFound) {
		t.Errorf("read policy after the deletion: err = %v, want it deleted from OpenBao", err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
