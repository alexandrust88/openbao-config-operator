package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/connection"
)

func TestOpenBaoServerValidation(t *testing.T) {
	requireEnvtest(t)
	caRef := &corev1alpha1.NamespacedCABundleReference{Namespace: openbaoName, Name: "ca"}
	tooMany := make([]string, 17)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("aud-%d", i)
	}
	cases := []struct {
		name    string
		mutate  func(*corev1alpha1.OpenBaoServerSpec)
		wantErr string
	}{
		{name: "minimal"},
		{
			name: "full",
			mutate: func(s *corev1alpha1.OpenBaoServerSpec) {
				s.CABundle = &corev1alpha1.CABundleSource{SecretRef: caRef}
				s.AllowedNamespaces = &metav1.LabelSelector{}
				s.AllowedAudiences = []string{openbaoName}
				s.RateLimit = &corev1alpha1.ServerRateLimit{QPS: 50, Burst: 100}
				s.RequestTimeout = &metav1.Duration{Duration: 7e9}
			},
		},
		{
			name:    "address with v1",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.Address = "https://bao:8200/v1" },
			wantErr: "address must not end with /v1",
		},
		{
			name:    "address not a url",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.Address = "bao:8200" },
			wantErr: "spec.address",
		},
		{
			name:    "http address",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.Address = "http://bao:8200" },
			wantErr: "address must use https unless allowInsecureHTTP is true",
		},
		{
			name: "http address allowed",
			mutate: func(s *corev1alpha1.OpenBaoServerSpec) {
				s.Address, s.AllowInsecureHTTP = "http://bao:8200", true
			},
		},
		{
			name: "two ca bundles",
			mutate: func(s *corev1alpha1.OpenBaoServerSpec) {
				s.CABundle = &corev1alpha1.CABundleSource{SecretRef: caRef, ConfigMapRef: caRef}
			},
			wantErr: "exactly one of the fields in [secretRef configMapRef] must be set",
		},
		{
			name:    "empty ca bundle",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.CABundle = &corev1alpha1.CABundleSource{} },
			wantErr: "exactly one of the fields in [secretRef configMapRef] must be set",
		},
		{
			name: "ca bundle without namespace",
			mutate: func(s *corev1alpha1.OpenBaoServerSpec) {
				s.CABundle = &corev1alpha1.CABundleSource{ConfigMapRef: &corev1alpha1.NamespacedCABundleReference{Name: "ca"}}
			},
			wantErr: "spec.caBundle.configMapRef.namespace",
		},
		{
			name:    "zero qps",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.RateLimit = &corev1alpha1.ServerRateLimit{} },
			wantErr: "spec.rateLimit.qps",
		},
		{
			name:    "too many audiences",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.AllowedAudiences = tooMany },
			wantErr: "spec.allowedAudiences",
		},
		{
			name:    "empty audience",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.AllowedAudiences = []string{""} },
			wantErr: "spec.allowedAudiences[0]",
		},
		{
			name:    "duplicate audience",
			mutate:  func(s *corev1alpha1.OpenBaoServerSpec) { s.AllowedAudiences = []string{openbaoName, openbaoName} },
			wantErr: "Duplicate value",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &corev1alpha1.OpenBaoServer{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("server-%d", i)},
				Spec:       corev1alpha1.OpenBaoServerSpec{Address: testAddress},
			}
			if tc.mutate != nil {
				tc.mutate(&server.Spec)
			}
			assertConnectionCreate(t, server, tc.wantErr)
		})
	}
}

func TestOpenBaoServerCABundleKeyDefault(t *testing.T) {
	requireEnvtest(t)
	server := &corev1alpha1.OpenBaoServer{
		ObjectMeta: metav1.ObjectMeta{Name: "ca-default"},
		Spec: corev1alpha1.OpenBaoServerSpec{
			Address: testAddress,
			CABundle: &corev1alpha1.CABundleSource{
				ConfigMapRef: &corev1alpha1.NamespacedCABundleReference{Namespace: openbaoName, Name: "ca"},
			},
		},
	}
	if err := envtestClient.Create(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	if got := server.Spec.CABundle.ConfigMapRef.Key; got != corev1alpha1.DefaultCABundleKey {
		t.Errorf("key = %q, want %q", got, corev1alpha1.DefaultCABundleKey)
	}
}

// TestConnectionsHaveNoAddress proves a tenant cannot pick the URL the operator calls: the connection kinds have
// no address, CA or timeout fields, and serverRef is required.
func TestConnectionsHaveNoAddress(t *testing.T) {
	requireEnvtest(t)
	ensureValidationNamespace(t)
	token := map[string]any{tokenKey: map[string]any{"secretRef": map[string]any{fieldName: "t", "key": tokenKey}}}
	cases := []struct {
		name    string
		kind    string
		spec    map[string]any
		strict  bool
		wantErr string
	}{
		{
			name:    "address strict",
			kind:    kindConnection,
			spec:    map[string]any{fieldServerRef: map[string]any{fieldName: openbaoName}, "address": testAddress, litAuth: token},
			strict:  true,
			wantErr: `unknown field "spec.address"`,
		},
		{
			name: "ca bundle strict",
			kind: kindConnection,
			spec: map[string]any{
				fieldServerRef: map[string]any{fieldName: openbaoName}, litAuth: token,
				"caBundleConfigMapRef": map[string]any{fieldName: "ca"},
			},
			strict:  true,
			wantErr: `unknown field "spec.caBundleConfigMapRef"`,
		},
		{
			name:    "address instead of serverRef",
			kind:    kindConnection,
			spec:    map[string]any{"address": testAddress, litAuth: token},
			wantErr: "spec.serverRef: Required value",
		},
		{
			name: "cluster address strict",
			kind: "ClusterOpenBaoConnection",
			spec: map[string]any{
				fieldServerRef: map[string]any{fieldName: openbaoName}, "requestTimeout": "5s",
				litAuth: map[string]any{tokenKey: map[string]any{litFilesystem: map[string]any{fieldPath: sinkTokenPath}}},
			},
			strict:  true,
			wantErr: `unknown field "spec.requestTimeout"`,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := map[string]any{"name": fmt.Sprintf("no-address-%d", i)}
			if tc.kind == kindConnection {
				metadata["namespace"] = validationNamespace
			}
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": corev1alpha1.GroupVersion.String(),
				"kind":       tc.kind,
				"metadata":   metadata,
				"spec":       tc.spec,
			}}
			var opts []client.CreateOption
			if tc.strict {
				opts = append(opts, client.FieldValidation("Strict"))
			}
			err := envtestClient.Create(context.Background(), obj, opts...)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestAPIServerAudiencesDiscoversIssuer reads the service account issuer the envtest API server advertises.
func TestAPIServerAudiencesDiscoversIssuer(t *testing.T) {
	requireEnvtest(t)
	got, err := connection.APIServerAudiences(context.Background(), envtestConfig, []string{"https://configured"})
	if err != nil {
		t.Fatal(err)
	}
	issuer := envtestEnv.ControlPlane.APIServer.SecureServing.URL("https", "/").String()
	for _, want := range []string{"https://kubernetes.default.svc", "rke2", "k3s", "https://configured", issuer} {
		if !slices.Contains(got, want) {
			t.Errorf("audiences = %v, missing %s", got, want)
		}
	}
}

// TestTenantBoundaryEnvtest runs the connection controller against a real API server: CEL cannot see the
// OpenBaoServer, so the resolver denies a namespace or audience the server does not allow, before any
// TokenRequest.
const boundaryServer = "boundary"

func TestTenantBoundaryEnvtest(t *testing.T) {
	requireEnvtest(t)
	ctx := context.Background()
	issuer := envtestEnv.ControlPlane.APIServer.SecureServing.URL("https", "/").String()
	audiences, err := connection.APIServerAudiences(ctx, envtestConfig, nil)
	if err != nil {
		t.Fatal(err)
	}

	server := &corev1alpha1.OpenBaoServer{
		ObjectMeta: metav1.ObjectMeta{Name: boundaryServer},
		Spec: corev1alpha1.OpenBaoServerSpec{
			Address:           testAddress,
			AllowedNamespaces: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: boundaryServer}},
			AllowedAudiences:  []string{openbaoName, issuer},
		},
	}
	admitted := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "boundary-tenant",
		Labels: map[string]string{tenantLabel: boundaryServer}}}
	outsider := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "boundary-outsider"}}
	for _, obj := range []client.Object{server, admitted, outsider} {
		if err := envtestClient.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}

	tokenRequests := 0
	c, err := client.NewWithWatch(envtestConfig, client.Options{Scheme: connectionTestScheme})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := interceptor.NewClient(c, interceptor.Funcs{SubResourceCreate: func(ctx context.Context,
		c client.Client, subResource string, obj, sub client.Object, opts ...client.SubResourceCreateOption) error {
		if subResource == tokenKey {
			tokenRequests++
		}
		return c.SubResource(subResource).Create(ctx, obj, sub, opts...)
	}})
	pool := &recordingPool{client: lookupSelfClient{}}
	r := &connectionReconciler{
		Client:    wrapped,
		resolver:  &connection.Resolver{Client: wrapped, Pool: pool, APIServerAudiences: audiences},
		pool:      pool,
		newObject: func() connectionObject { return &corev1alpha1.OpenBaoConnection{} },
		newList:   func() client.ObjectList { return &corev1alpha1.OpenBaoConnectionList{} },
	}

	cases := []struct {
		name, namespace, audience, wantReason, wantMessage string
	}{
		{"namespace not selected", outsider.Name, openbaoName, commonv1alpha1.ReasonConnectionDenied,
			"namespace boundary-outsider is not in allowedNamespaces"},
		{"audience not allowed", admitted.Name, "vault", commonv1alpha1.ReasonConnectionDenied,
			`audience "vault" is not in the allowedAudiences of OpenBaoServer boundary`},
		{"api server issuer listed", admitted.Name, issuer, commonv1alpha1.ReasonConnectionDenied,
			"is accepted by the Kubernetes API server"},
		{"admitted", admitted.Name, openbaoName, corev1alpha1.ReasonServerNotReady, "OpenBaoServer boundary is not ready"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &corev1alpha1.OpenBaoConnection{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("boundary-%d", i), Namespace: tc.namespace},
				Spec: corev1alpha1.OpenBaoConnectionSpec{
					ConnectionSpec: corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: server.Name}},
					Auth: corev1alpha1.ConnectionAuth{Kubernetes: &corev1alpha1.KubernetesAuth{
						Role: "r", ServiceAccountRef: saRef(), Audiences: []string{tc.audience},
					}},
				},
			}
			if err := envtestClient.Create(ctx, conn); err != nil {
				t.Fatalf("create: %v (CEL must accept any audience; only the resolver sees the server)", err)
			}
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(conn)}); err != nil {
				t.Fatal(err)
			}
			got := &corev1alpha1.OpenBaoConnection{}
			if err := envtestClient.Get(ctx, client.ObjectKeyFromObject(conn), got); err != nil {
				t.Fatal(err)
			}
			cond := meta.FindStatusCondition(got.Status.Conditions, commonv1alpha1.ConditionReady)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != tc.wantReason ||
				!strings.Contains(cond.Message, tc.wantMessage) {
				t.Errorf("Ready = %+v, want False/%s containing %q", cond, tc.wantReason, tc.wantMessage)
			}
			if tokenRequests != 0 {
				t.Errorf("%d TokenRequests sent", tokenRequests)
			}
		})
	}
	if err := envtestClient.Delete(ctx, server); err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}
