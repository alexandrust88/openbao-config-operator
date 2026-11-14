package engine

import (
	"bytes"
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	hashKeyNamespace = "operator"
	hashKeyName      = "openbao-config-operator-hash-key"
)

func hashKeyClient(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) client.WithWatch {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(), funcs)
}

func TestLoadOrCreateHashKey(t *testing.T) {
	ctx := context.Background()
	key := client.ObjectKey{Namespace: hashKeyNamespace, Name: hashKeyName}

	t.Run("creates a random key once", func(t *testing.T) {
		c := hashKeyClient(t, interceptor.Funcs{})
		first, err := LoadOrCreateHashKey(ctx, c, c, hashKeyNamespace, hashKeyName)
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != hashKeySize || bytes.Equal(first, make([]byte, hashKeySize)) {
			t.Fatalf("key has %d bytes or is zero", len(first))
		}
		var secret corev1.Secret
		if err := c.Get(ctx, key, &secret); err != nil {
			t.Fatal(err)
		}
		if secret.Immutable == nil || !*secret.Immutable {
			t.Fatal("hash key secret is not immutable")
		}
		second, err := LoadOrCreateHashKey(ctx, c, c, hashKeyNamespace, hashKeyName)
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("second load returned a different key (err %v)", err)
		}
	})

	t.Run("reads back a concurrent create", func(t *testing.T) {
		winner := bytes.Repeat([]byte{7}, hashKeySize)
		c := hashKeyClient(t, interceptor.Funcs{
			Create: func(ctx context.Context, inner client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
				other := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), Namespace: obj.GetNamespace()},
					Data:       map[string][]byte{HashKeySecretKey: winner},
				}
				if err := inner.Create(ctx, other); err != nil {
					return err
				}
				return apierrors.NewAlreadyExists(corev1.Resource("secrets"), obj.GetName())
			},
		})
		got, err := LoadOrCreateHashKey(ctx, c, c, hashKeyNamespace, hashKeyName)
		if err != nil || !bytes.Equal(got, winner) {
			t.Fatalf("got %d bytes, err %v; want the concurrently created key", len(got), err)
		}
	})

	t.Run("rejects a short key", func(t *testing.T) {
		short := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: hashKeyName, Namespace: hashKeyNamespace},
			Data:       map[string][]byte{HashKeySecretKey: []byte("short")},
		}
		c := hashKeyClient(t, interceptor.Funcs{}, short)
		if _, err := LoadOrCreateHashKey(ctx, c, c, hashKeyNamespace, hashKeyName); err == nil {
			t.Fatal("short key accepted")
		}
	})
}
