//go:build integration

package livekit

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func verifyOutputSecrets(ctx context.Context, t testing.TB, k8s client.Client, r *run) {
	t.Helper()
	checked := 0
	for _, obj := range r.objs {
		name, keys := outputKeys(obj)
		if name == "" {
			continue
		}
		cur := obj.DeepCopy()
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
			t.Errorf("%s: read output owner %s/%s: %v", r.overlay, obj.GetKind(), obj.GetName(), err)
			continue
		}
		if !stateOf(cur).ready {
			continue
		}
		var secret corev1.Secret
		if err := k8s.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: name}, &secret); err != nil {
			t.Errorf("%s: Ready %s/%s has no readable output Secret %s: %v", r.overlay, obj.GetKind(), obj.GetName(), name, err)
			continue
		}
		if !metav1.IsControlledBy(&secret, cur) {
			t.Errorf("%s: output Secret %s is not controlled by %s/%s", r.overlay, name, obj.GetKind(), obj.GetName())
		}
		for _, key := range keys {
			if len(secret.Data[key]) == 0 {
				t.Errorf("%s: output Secret %s is missing nonempty key %s", r.overlay, name, key)
			}
		}
		checked++
	}
	t.Logf("%s: verified ownership and required keys of %d output Secrets", r.overlay, checked)
}

func outputKeys(obj *unstructured.Unstructured) (string, []string) {
	name, _, _ := unstructured.NestedString(obj.Object, keySpec, "writeSecretRef", keyName)
	switch obj.GetKind() {
	case "AppRoleSecretID":
		return name, []string{"role_id", "secret_id", "secret_id_accessor"}
	case "PKIKey", "PKIIssuer":
		return name, []string{"private_key", "key_id"}
	case "RandomSecret":
		key, _, _ := unstructured.NestedString(obj.Object, keySpec, "key")
		if key == "" {
			key = "password"
		}
		return name, []string{key}
	case "TOTPKey":
		name, _, _ = unstructured.NestedString(obj.Object, keySpec, "generate", "writeSecretRef", keyName)
		return name, []string{"url", "fingerprint"}
	case "OIDCClient":
		keys := []string{"client_id"}
		typ, _, _ := unstructured.NestedString(obj.Object, keySpec, "clientType")
		if typ != "public" {
			keys = append(keys, "client_secret")
		}
		return name, keys
	}
	return "", nil
}
