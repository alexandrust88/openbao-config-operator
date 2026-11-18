package core

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/connection"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

func newServerReconciler(probe probeFunc, objs ...client.Object) *serverReconciler {
	c := connectionFakeClient(objs...)
	return &serverReconciler{Client: c, resolver: &connection.Resolver{Client: c}, probe: probe}
}

func unprobedServer() *corev1alpha1.BaoServer {
	s := testServer()
	s.Status = corev1alpha1.BaoServerStatus{}
	return s
}

func reconcileServer(t *testing.T, r *serverReconciler) (ctrl.Result, *corev1alpha1.BaoServer) {
	t.Helper()
	key := client.ObjectKey{Name: openbaoName}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := &corev1alpha1.BaoServer{}
	if err := r.Get(context.Background(), key, got); err != nil {
		t.Fatal(err)
	}
	return res, got
}

func TestServerReconcileReady(t *testing.T) {
	server := unprobedServer()
	server.Spec.CABundle = &corev1alpha1.CABundleSource{
		SecretRef: &corev1alpha1.NamespacedCABundleReference{Namespace: openbaoName, Name: "ca"},
	}
	ca := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: openbaoName, Name: "ca"},
		Data:       map[string][]byte{corev1alpha1.DefaultCABundleKey: []byte("PEM")},
	}
	var probed openbao.Config
	probe := func(_ context.Context, cfg openbao.Config) (openbao.ServerInfo, error) {
		probed = cfg
		return openbao.ServerInfo{Version: "2.7.2", Initialized: true, Standby: true}, nil
	}
	r := newServerReconciler(probe, server, ca)

	res, got := reconcileServer(t, r)
	if res.RequeueAfter < probeInterval || res.RequeueAfter > probeInterval*6/5 {
		t.Errorf("RequeueAfter = %v, want a jittered %v", res.RequeueAfter, probeInterval)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, commonv1alpha1.ConditionReady) {
		t.Fatalf("not Ready: %+v", got.Status.Conditions)
	}
	s := got.Status
	if s.Version != "2.7.2" || !s.Standby || s.Sealed || s.ObservedGeneration != 1 || s.LastProbeTime == nil {
		t.Errorf("unexpected status %+v", s)
	}
	if probed.Address != testAddress || string(probed.CABundle) != "PEM" || probed.Auth != nil {
		t.Errorf("probe config = %+v, want the server address and CA without auth", probed)
	}

	_, again := reconcileServer(t, r)
	if again.ResourceVersion != got.ResourceVersion {
		t.Errorf("an unchanged probe wrote the status (resourceVersion %s -> %s)", got.ResourceVersion, again.ResourceVersion)
	}
}

func TestServerReconcileNotReady(t *testing.T) {
	withCA := func(s *corev1alpha1.BaoServer) {
		s.Spec.CABundle = &corev1alpha1.CABundleSource{
			ConfigMapRef: &corev1alpha1.NamespacedCABundleReference{Namespace: openbaoName, Name: "absent"},
		}
	}
	badSelector := func(s *corev1alpha1.BaoServer) {
		s.Spec.AllowedNamespaces = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: tenantLabel, Operator: "Bogus"},
		}}
	}
	cases := []struct {
		name       string
		mutate     func(*corev1alpha1.BaoServer)
		probe      probeFunc
		wantReason string
		wantSealed bool
	}{
		{
			name: "unreachable",
			probe: func(context.Context, openbao.Config) (openbao.ServerInfo, error) {
				return openbao.ServerInfo{}, errors.New("connection refused")
			},
			wantReason: corev1alpha1.ReasonUnreachable,
		},
		{
			name: "sealed",
			probe: func(context.Context, openbao.Config) (openbao.ServerInfo, error) {
				return openbao.ServerInfo{Initialized: true, Sealed: true}, nil
			},
			wantReason: corev1alpha1.ReasonSealed,
			wantSealed: true,
		},
		{
			name: "not initialized",
			probe: func(context.Context, openbao.Config) (openbao.ServerInfo, error) {
				return openbao.ServerInfo{}, nil
			},
			wantReason: corev1alpha1.ReasonSealed,
			wantSealed: true,
		},
		{name: "ca bundle missing", mutate: withCA, probe: healthyProbe, wantReason: commonv1alpha1.ReasonReferenceNotReady},
		{name: "invalid selector", mutate: badSelector, probe: healthyProbe, wantReason: commonv1alpha1.ReasonInvalidSpec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := unprobedServer()
			if tc.mutate != nil {
				tc.mutate(server)
			}
			_, got := reconcileServer(t, newServerReconciler(tc.probe, server))
			cond := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionReady)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != tc.wantReason {
				t.Errorf("Ready = %+v, want False/%s", cond, tc.wantReason)
			}
			if got.Status.Sealed != tc.wantSealed {
				t.Errorf("sealed = %v, want %v", got.Status.Sealed, tc.wantSealed)
			}
		})
	}
}

func TestServerReferencingEnqueues(t *testing.T) {
	server := unprobedServer()
	server.Spec.CABundle = &corev1alpha1.CABundleSource{
		ConfigMapRef: &corev1alpha1.NamespacedCABundleReference{Namespace: openbaoName, Name: "ca"},
	}
	r := newServerReconciler(healthyProbe, server)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: openbaoName, Name: "ca"}}
	requests := r.referencing("ConfigMap")(context.Background(), cm)
	if len(requests) != 1 || requests[0].Name != openbaoName {
		t.Errorf("requests = %v, want the server", requests)
	}
	if requests := r.referencing("Secret")(context.Background(), cm); len(requests) != 0 {
		t.Errorf("a Secret of the same name enqueued %v", requests)
	}
}

// TestServerCARotationReprobesEnvtest proves that, with ConfigMaps watched by metadata only and read live, an
// update of the CA bundle ConfigMap re-probes the server with the new bundle long before the probe interval.
func TestServerCARotationReprobesEnvtest(t *testing.T) {
	requireEnvtest(t)
	ctx := context.Background()
	mgr, err := ctrl.NewManager(envtestConfig, ctrl.Options{
		Scheme:                 connectionTestScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             config.Controller{SkipNameValidation: new(true)},
		Client: client.Options{Cache: &client.CacheOptions{
			DisableFor: []client.Object{&corev1.Secret{}, &corev1.ConfigMap{}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundles := make(chan string, 16)
	probe := func(_ context.Context, cfg openbao.Config) (openbao.ServerInfo, error) {
		bundles <- string(cfg.CABundle)
		return openbao.ServerInfo{Initialized: true}, nil
	}
	if err := setupServerController(mgr, &connection.Resolver{Client: mgr.GetClient()}, probe); err != nil {
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

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "carotation-ca"},
		Data: map[string]string{corev1alpha1.DefaultCABundleKey: "ca-v1"}}
	server := &corev1alpha1.BaoServer{
		ObjectMeta: metav1.ObjectMeta{Name: "carotation"},
		Spec: corev1alpha1.BaoServerSpec{Address: testAddress, CABundle: &corev1alpha1.CABundleSource{
			ConfigMapRef: &corev1alpha1.NamespacedCABundleReference{Namespace: cm.Namespace, Name: cm.Name},
		}},
	}
	for _, obj := range []client.Object{cm, server} {
		if err := envtestClient.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	carotationWaitBundle(t, bundles, "ca-v1")
	cm.Data[corev1alpha1.DefaultCABundleKey] = "ca-v2"
	if err := envtestClient.Update(ctx, cm); err != nil {
		t.Fatal(err)
	}
	carotationWaitBundle(t, bundles, "ca-v2")
}

func carotationWaitBundle(t *testing.T, bundles <-chan string, want string) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case got := <-bundles:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("no probe with CA bundle %q", want)
		}
	}
}
