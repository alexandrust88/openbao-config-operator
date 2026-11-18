package connection

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

func writeTokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), tokenKey)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// testJWT returns an unsigned ServiceAccount-shaped JWT with a dedicated audience; sub tells tokens apart.
func testJWT(sub string) string {
	return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"aud":["openbao-config-operator"],"sub":"`+sub+`"}`)) + ".sig"
}

func TestConfigClusterInjectedIdentity(t *testing.T) {
	ctx := context.Background()
	projectedJWT, rotatedJWT := testJWT("projected"), testJWT("rotated")
	projected := writeTokenFile(t, projectedJWT)
	for name, auth := range map[string]corev1alpha1.ClusterConnectionAuth{
		kubernetesMount: {Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
			Role: litOperator, InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: projected},
		}},
		litJwt: {JWT: &corev1alpha1.ClusterJWTAuth{
			Role: litOperator, InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: projected},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			conn := clusterConn(auth, nil)
			r, _ := newResolver(t, conn)
			r.TokenFile = writeTokenFile(t, testJWT("default"))

			cfg, err := r.Config(ctx, conn)
			if err != nil {
				t.Fatal(err)
			}
			var jwt openbao.CredentialFunc
			var key string
			switch a := innerAuth(t, cfg).(type) {
			case openbao.KubernetesAuth:
				jwt, key = a.JWT, a.CredentialKey
			case openbao.JWTAuth:
				jwt, key = a.JWT, a.CredentialKey
			default:
				t.Fatalf("auth is %T", a)
			}
			if key != "tokenfile:"+projected {
				t.Errorf("credential key = %q, want the token path", key)
			}
			if got, err := jwt(ctx); err != nil || got != projectedJWT {
				t.Errorf("JWT() = %q, %v; want the tokenPath file, not the default", got, err)
			}
			if err := os.WriteFile(projected, []byte(rotatedJWT), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := jwt(ctx); err != nil || got != rotatedJWT {
				t.Errorf("JWT() after rotation = %q, %v; want the file read at every login", got, err)
			}
			if err := os.WriteFile(projected, []byte(projectedJWT), 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigClusterInjectedIdentityMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for name, conn := range map[string]*corev1alpha1.ClusterBaoConnection{
		"explicit": clusterConn(corev1alpha1.ClusterConnectionAuth{Kubernetes: &corev1alpha1.ClusterKubernetesAuth{
			Role: litOperator, InjectedIdentity: &corev1alpha1.InjectedIdentity{TokenPath: missing},
		}}, nil),
		"implicit": clusterConn(corev1alpha1.ClusterConnectionAuth{JWT: &corev1alpha1.ClusterJWTAuth{
			Role: litOperator,
		}}, nil),
	} {
		r, _ := newResolver(t, conn)
		r.TokenFile = missing
		if _, err := r.Config(context.Background(), conn); !errors.Is(err, ErrReferenceNotFound) {
			t.Errorf("%s: err = %v, want ErrReferenceNotFound", name, err)
		}
	}
}

func TestConfigClusterFilesystemToken(t *testing.T) {
	ctx := context.Background()
	path := writeTokenFile(t, "s.sink")
	conn := clusterConn(corev1alpha1.ClusterConnectionAuth{Token: &corev1alpha1.ClusterTokenAuth{
		Filesystem: &corev1alpha1.FilesystemTokenSource{Path: path},
	}}, nil)
	r, _ := newResolver(t, conn)

	cfg, err := r.Config(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	auth, ok := innerAuth(t, cfg).(openbao.TokenAuth)
	if !ok {
		t.Fatalf("auth is %T, want openbao.TokenAuth", innerAuth(t, cfg))
	}
	if token, err := auth.Token(ctx); err != nil || token != "s.sink" {
		t.Errorf("Token() = %q, %v", token, err)
	}
	if strings.Contains(auth.CredentialKey, "s.sink") || !strings.HasPrefix(auth.CredentialKey, "file:"+path+"@") {
		t.Errorf("credential key %q must name the file and its version, not its value", auth.CredentialKey)
	}
	if refs := References(conn); len(refs) != 0 {
		t.Errorf("References = %v, want none for a file", refs)
	}

	if err := os.WriteFile(path, []byte("s.renewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	cfg, err = r.Config(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if innerAuth(t, cfg).CacheKey() == auth.CacheKey() {
		t.Error("cache key unchanged after the token file was rewritten")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Config(ctx, conn); !errors.Is(err, ErrReferenceNotFound) {
		t.Errorf("missing file: err = %v, want ErrReferenceNotFound", err)
	}
}

// TestNamespacedSpecHasNoOperatorSources keeps the tenant boundary structural: a field that lets a namespaced
// connection read the operator's token or files must never be added to BaoConnectionSpec.
func TestNamespacedSpecHasNoOperatorSources(t *testing.T) {
	if got := operatorSourceFields(reflect.TypeFor[corev1alpha1.BaoConnectionSpec]()); len(got) != 0 {
		t.Errorf("BaoConnectionSpec has operator credential source fields %v", got)
	}
	got := operatorSourceFields(reflect.TypeFor[corev1alpha1.ClusterBaoConnectionSpec]())
	if !slices.Contains(got, ".auth.kubernetes.injectedIdentity") || !slices.Contains(got, ".auth.token.filesystem") {
		t.Errorf("walker missed the cluster sources, found %v", got)
	}
}

func operatorSourceFields(root reflect.Type) []string {
	forbidden := map[string]bool{"injectedIdentity": true, "tokenPath": true, "filesystem": true,
		"environment": true, "env": true}
	var found []string
	var walk func(typ reflect.Type, at string, depth int)
	walk = func(typ reflect.Type, at string, depth int) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || depth > 8 {
			return
		}
		for f := range typ.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			path := at
			if name != "" {
				path += "." + name
			}
			if forbidden[name] {
				found = append(found, path)
			}
			walk(f.Type, path, depth+1)
		}
	}
	walk(root, "", 0)
	return found
}
