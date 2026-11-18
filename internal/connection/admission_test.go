package connection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const issuerAudience = "https://issuer.example"

func serviceAccountConn(jwt bool, audiences ...string) *corev1alpha1.BaoConnection {
	sa := &commonv1alpha1.LocalObjectReference{Name: litConfigurer}
	if jwt {
		return namespacedConn(corev1alpha1.ConnectionAuth{JWT: &corev1alpha1.JWTAuth{
			Role: teamA, ServiceAccountRef: sa, Audiences: audiences,
		}})
	}
	return namespacedConn(corev1alpha1.ConnectionAuth{Kubernetes: &corev1alpha1.KubernetesAuth{
		Role: teamA, ServiceAccountRef: sa, Audiences: audiences,
	}})
}

type admissionCase struct {
	name    string
	conn    *corev1alpha1.BaoConnection
	server  func(*corev1alpha1.BaoServer)
	ns      *corev1.Namespace
	wantErr string
}

// builtinAudienceCases lists one case per built-in API server audience that a server's allowedAudiences lists.
func builtinAudienceCases() []admissionCase {
	cases := make([]admissionCase, 0, len(builtinAPIServerAudiences))
	for _, audience := range builtinAPIServerAudiences {
		cases = append(cases, admissionCase{
			name: "built-in api server audience " + audience + " listed",
			conn: serviceAccountConn(false, audience),
			server: func(s *corev1alpha1.BaoServer) {
				s.Spec.AllowedAudiences = append(s.Spec.AllowedAudiences, audience)
			},
			wantErr: "is accepted by the Kubernetes API server",
		})
	}
	return cases
}

// TestTenantBoundary denies a namespaced connection the server does not admit before any TokenRequest is sent.
func TestTenantBoundary(t *testing.T) {
	cases := slices.Concat([]admissionCase{
		{name: "admitted", conn: serviceAccountConn(false, testAudience)},
		{name: "admitted jwt", conn: serviceAccountConn(true, testAudience)},
		{
			name:    "namespace not selected",
			conn:    serviceAccountConn(false, testAudience),
			ns:      namespace(teamA, nil),
			wantErr: "namespace team-a is not in allowedNamespaces",
		},
		{
			name:    "server allows no namespaces",
			conn:    serviceAccountConn(false, testAudience),
			server:  func(s *corev1alpha1.BaoServer) { s.Spec.AllowedNamespaces = nil },
			wantErr: "allows no namespaces",
		},
		{
			name: "invalid selector",
			conn: serviceAccountConn(false, testAudience),
			server: func(s *corev1alpha1.BaoServer) {
				s.Spec.AllowedNamespaces = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: tenantLabel, Operator: "Bogus"},
				}}
			},
			wantErr: "invalid allowedNamespaces",
		},
		{
			name:    "audience not allowed",
			conn:    serviceAccountConn(false, testAudience, "vault"),
			wantErr: `audience "vault" is not in the allowedAudiences of BaoServer openbao`,
		},
		{
			name:    "jwt audience not allowed",
			conn:    serviceAccountConn(true, "vault"),
			wantErr: `audience "vault" is not in the allowedAudiences`,
		},
		{
			name: "discovered issuer listed",
			conn: serviceAccountConn(true, issuerAudience),
			server: func(s *corev1alpha1.BaoServer) {
				s.Spec.AllowedAudiences = append(s.Spec.AllowedAudiences, issuerAudience)
			},
			wantErr: "is accepted by the Kubernetes API server",
		},
		{
			name:    "token auth from a namespace not selected",
			conn:    namespacedConn(tokenAuth),
			ns:      namespace(teamA, map[string]string{tenantLabel: "false"}),
			wantErr: "is not in allowedNamespaces",
		},
	}, builtinAudienceCases())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := tenantServer()
			if tc.server != nil {
				tc.server(server)
			}
			ns := tc.ns
			if ns == nil {
				ns = tenantNamespace()
			}
			sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: teamA, Name: litConfigurer}}
			tokenSecret := secret(teamA, "bao-token", map[string]string{tokenKey: litToken})
			r, pool, tokenRequests := newCountingResolver(t, server, ns, tc.conn, sa, tokenSecret)
			r.APIServerAudiences = []string{issuerAudience}
			ctx := context.Background()

			_, err := r.For(ctx, teamA, commonv1alpha1.ConnectionReference{Name: connName})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("For: %v", err)
				}
				var jwt openbao.CredentialFunc
				switch a := innerAuth(t, pool.cfg).(type) {
				case openbao.KubernetesAuth:
					jwt = a.JWT
				case openbao.JWTAuth:
					jwt = a.JWT
				default:
					t.Fatalf("auth is %T", a)
				}
				if _, err := jwt(ctx); err != nil || *tokenRequests != 1 {
					t.Errorf("JWT() err = %v after %d TokenRequests, want one TokenRequest", err, *tokenRequests)
				}
				return
			}
			if !errors.Is(err, engine.ErrConnectionDenied) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want ErrConnectionDenied containing %q", err, tc.wantErr)
			}
			if *tokenRequests != 0 {
				t.Errorf("%d TokenRequests sent for a denied connection", *tokenRequests)
			}
		})
	}
}

func TestClusterConnectionIgnoresServerTenantRules(t *testing.T) {
	server := tenantServer()
	server.Spec.AllowedNamespaces = nil
	server.Spec.AllowedAudiences = nil
	auth := corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
		Role:              litOperator,
		ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: litOpenBaoNS, Name: litOperator},
		Audiences:         []string{"platform"},
	}}
	r, _ := newResolver(t, server, clusterConn(auth, &metav1.LabelSelector{}))
	ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}
	if _, err := r.For(context.Background(), teamA, ref); err != nil {
		t.Errorf("For: %v", err)
	}
}

// TestAPIServerAudiencesDiscoveryFailureKeepsBuiltins proves a failed issuer discovery cannot shrink the denylist.
func TestAPIServerAudiencesDiscoveryFailureKeepsBuiltins(t *testing.T) {
	got, err := APIServerAudiences(context.Background(), &rest.Config{Host: "https://127.0.0.1:1"}, []string{"rke2", "custom"})
	if err == nil {
		t.Fatal("discovery against a closed port succeeded")
	}
	want := append(slices.Clone(builtinAPIServerAudiences), "custom")
	if !slices.Equal(got, want) {
		t.Errorf("audiences = %v, want %v", got, want)
	}
}

// TestClusterServiceAccountAudiences applies the API server audience rules to a cluster connection that requests a
// token for a named ServiceAccount, before any TokenRequest.
func TestClusterServiceAccountAudiences(t *testing.T) {
	for name, tc := range map[string]struct {
		audiences []string
		want      error
	}{
		"no audiences":       {want: ErrMissingAudiences},
		"built-in audience":  {audiences: []string{"rke2"}, want: engine.ErrConnectionDenied},
		"discovered issuer":  {audiences: []string{issuerAudience}, want: engine.ErrConnectionDenied},
		"dedicated audience": {audiences: []string{"platform"}},
	} {
		t.Run(name, func(t *testing.T) {
			auth := corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
				Role:              litOperator,
				ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: litOpenBaoNS, Name: litOperator},
				Audiences:         tc.audiences,
			}}
			r, _, tokenRequests := newCountingResolver(t, tenantServer(), clusterConn(auth, &metav1.LabelSelector{}))
			r.APIServerAudiences = []string{issuerAudience}
			ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}
			_, err := r.For(context.Background(), teamA, ref)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if *tokenRequests != 0 {
				t.Errorf("%d TokenRequests sent before login", *tokenRequests)
			}
		})
	}
}

// TestAutomountedTokenNeverSent refuses every way a cluster connection could send the operator's automounted
// ServiceAccount token, a Kubernetes API credential, to OpenBao.
func TestAutomountedTokenNeverSent(t *testing.T) {
	const automounted = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	for name, tc := range map[string]struct {
		auth      corev1alpha1.ClusterConnectionAuth
		tokenFile string
	}{
		"kubernetes tokenPath": {auth: corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
			Role: litOperator, InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: automounted},
		}}},
		"jwt tokenPath in the directory": {auth: corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
			Role: litOperator, InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: "/var/run/secrets/kubernetes.io/serviceaccount/../serviceaccount/token"},
		}}},
		"resolver default": {
			auth:      corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{Role: litOperator}},
			tokenFile: automounted,
		},
		"token file": {auth: corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
			Filesystem: &corev1alpha1.FilesystemTokenSource{Path: automounted},
		}}},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newResolver(t, tenantServer(), clusterConn(tc.auth, &metav1.LabelSelector{}))
			r.TokenFile = tc.tokenFile
			ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}
			if _, err := r.For(context.Background(), teamA, ref); !errors.Is(err, ErrAutomountedToken) {
				t.Fatalf("err = %v, want ErrAutomountedToken", err)
			}
		})
	}
}

func TestServerConfigRefusesHTTP(t *testing.T) {
	server := tenantServer()
	server.Spec.Address = "http://bao:8200"
	r, _ := newResolver(t)
	if _, err := r.ServerConfig(context.Background(), server); !errors.Is(err, ErrInsecureAddress) {
		t.Fatalf("err = %v, want ErrInsecureAddress", err)
	}
	server.Spec.AllowInsecureHTTP = true
	if _, err := r.ServerConfig(context.Background(), server); err != nil {
		t.Fatalf("allowInsecureHTTP: %v", err)
	}
}

// TestAutomountedTokenByFileIdentity refuses the automounted token under another name: a symlink, and on Linux the
// /proc/self/root form, which pass any check on the path text.
func TestAutomountedTokenByFileIdentity(t *testing.T) {
	automount := writeTokenFile(t, "kube-api-jwt")
	link := filepath.Join(t.TempDir(), "token")
	if err := os.Symlink(automount, link); err != nil {
		t.Fatal(err)
	}
	paths := []string{link}
	if _, err := os.Stat("/proc/self/root" + automount); err == nil {
		paths = append(paths, "/proc/self/root"+automount)
	}
	for _, path := range paths {
		for name, auth := range map[string]corev1alpha1.ClusterConnectionAuth{
			"injected identity": {Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
				Role: litOperator, InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: path},
			}},
			"token file": {Token: &corev1alpha1.ClusterTokenAuth{Filesystem: &corev1alpha1.FilesystemTokenSource{Path: path}}},
		} {
			t.Run(name+" "+path, func(t *testing.T) {
				r, _ := newResolver(t, tenantServer(), clusterConn(auth, &metav1.LabelSelector{}))
				r.automountFile = automount
				ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}
				if _, err := r.For(context.Background(), teamA, ref); !errors.Is(err, ErrAutomountedToken) {
					t.Fatalf("err = %v, want ErrAutomountedToken", err)
				}
			})
		}
	}
}

// TestInjectedTokenAudienceChecked refuses, at login, an injected identity token whose aud claim is an API server
// audience, whatever file it comes from.
func TestInjectedTokenAudienceChecked(t *testing.T) {
	jwt := func(aud any) string {
		payload, err := json.Marshal(map[string]any{"aud": aud, "sub": "system:serviceaccount:x:y"})
		if err != nil {
			t.Fatal(err)
		}
		return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	}
	for name, tc := range map[string]struct {
		token string
		want  error
	}{
		"dedicated audience":        {token: jwt([]string{"openbao-config-operator"})},
		"built-in audience":         {token: jwt([]string{"x", "https://kubernetes.default.svc.cluster.local"}), want: engine.ErrConnectionDenied},
		"discovered issuer, string": {token: jwt(issuerAudience), want: engine.ErrConnectionDenied},
		"no aud claim":              {token: jwt(nil), want: engine.ErrConnectionDenied},
		"empty aud claim":           {token: jwt([]string{}), want: engine.ErrConnectionDenied},
		"not a JWT":                 {token: "s.not-a-jwt", want: engine.ErrConnectionDenied},
	} {
		t.Run(name, func(t *testing.T) {
			auth := corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{Role: litOperator}}
			r, pool := newResolver(t, tenantServer(), clusterConn(auth, &metav1.LabelSelector{}))
			r.TokenFile, r.APIServerAudiences = writeTokenFile(t, tc.token), []string{issuerAudience}
			ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: clusterConnName}
			if _, err := r.For(context.Background(), teamA, ref); err != nil {
				t.Fatal(err)
			}
			login, ok := innerAuth(t, pool.cfg).(openbao.KubernetesAuth)
			if !ok {
				t.Fatalf("auth is %T", innerAuth(t, pool.cfg))
			}
			got, err := login.JWT(context.Background())
			if tc.want == nil && (err != nil || got != tc.token) || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("JWT() = %q, %v; want error %v", got, err, tc.want)
			}
		})
	}
}

// TestFilesystemTokenAudienceChecked refuses a cluster connection token file holding a JWT for an API server
// audience, while an OpenBao token passes unchanged.
func TestFilesystemTokenAudienceChecked(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"aud":["https://kubernetes.default.svc"]}`))
	for name, tc := range map[string]struct {
		token string
		want  error
	}{
		"openbao token":       {token: "s.sink"},
		"api server audience": {token: "e30." + payload + ".sig", want: engine.ErrConnectionDenied},
	} {
		t.Run(name, func(t *testing.T) {
			conn := clusterConn(corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
				Filesystem: &corev1alpha1.FilesystemTokenSource{Path: writeTokenFile(t, tc.token)},
			}}, nil)
			r, _ := newResolver(t, conn)
			cfg, err := r.Config(context.Background(), conn)
			if err != nil {
				t.Fatal(err)
			}
			auth, ok := innerAuth(t, cfg).(openbao.TokenAuth)
			if !ok {
				t.Fatalf("auth is %T", innerAuth(t, cfg))
			}
			got, err := auth.Token(context.Background())
			if tc.want == nil && (err != nil || got != tc.token) || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Token() = %q, %v; want error %v", got, err, tc.want)
			}
		})
	}
}

// TestJWTAudiencesExactKey reads aud the way the API server does: a differently cased "Aud" key must not hide an
// API server audience.
func TestJWTAudiencesExactKey(t *testing.T) {
	payload := `{"aud":"https://kubernetes.default.svc","Aud":"openbao"}`
	got, ok := jwtAudiences("e30." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig")
	if !ok || !slices.Equal(got, []string{"https://kubernetes.default.svc"}) {
		t.Fatalf("jwtAudiences(%s) = %q, %v; want the exact aud key", payload, got, ok)
	}
}
