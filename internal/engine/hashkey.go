package engine

import (
	"context"
	"crypto/rand"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HashKeySecretKey is the data key of the Secret that holds the write-only hash key.
const HashKeySecretKey = "key"

const hashKeySize = 32

// LoadOrCreateHashKey returns the write-only hash key from the Secret namespace/name. When the Secret does not
// exist it is created with a random key; a concurrent create by another replica is read back.
func LoadOrCreateHashKey(ctx context.Context, reader client.Reader, writer client.Writer, namespace,
	name string) ([]byte, error) {
	objKey := client.ObjectKey{Namespace: namespace, Name: name}
	key, err := readHashKey(ctx, reader, objKey)
	if !apierrors.IsNotFound(err) {
		return key, err
	}
	key = make([]byte, hashKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate hash key: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{managedByLabel: DefaultFieldOwner},
		},
		Type:      corev1.SecretTypeOpaque,
		Immutable: new(true),
		Data:      map[string][]byte{HashKeySecretKey: key},
	}
	err = writer.Create(ctx, secret)
	if apierrors.IsAlreadyExists(err) {
		return readHashKey(ctx, reader, objKey)
	}
	if err != nil {
		return nil, fmt.Errorf("create hash key secret %s: %w", objKey, err)
	}
	return key, nil
}

func readHashKey(ctx context.Context, reader client.Reader, objKey client.ObjectKey) ([]byte, error) {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, objKey, secret); err != nil {
		return nil, fmt.Errorf("get hash key secret %s: %w", objKey, err)
	}
	key := secret.Data[HashKeySecretKey]
	if len(key) < hashKeySize {
		return nil, fmt.Errorf("hash key secret %s: key %q must hold at least %d bytes", objKey, HashKeySecretKey,
			hashKeySize)
	}
	return key, nil
}
