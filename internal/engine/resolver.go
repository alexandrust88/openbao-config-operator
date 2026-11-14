package engine

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// NewResolver returns the default Resolver, which reads Secrets and referenced objects from reader.
func NewResolver(reader client.Reader) Resolver {
	return &readerResolver{reader: reader}
}

type readerResolver struct {
	reader client.Reader
}

// SecretValue wraps ErrReferenceNotReady when the Secret or the key is missing, so the resource waits for it.
func (r *readerResolver) SecretValue(ctx context.Context, namespace string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	var secret corev1.Secret
	if err := r.reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("secret %s/%s not found: %w", namespace, ref.Name, ErrReferenceNotReady)
		}
		return nil, fmt.Errorf("read secret %s/%s: %w", namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no key %q: %w", namespace, ref.Name, ref.Key, ErrReferenceNotReady)
	}
	return value, nil
}

// observeResolver serves managementPolicy Observe: a missing Secret or key resolves to an empty value instead of
// ErrReferenceNotReady, because write-only inputs are never compared and imported resources often name Secrets
// that do not exist yet.
type observeResolver struct {
	Resolver
}

func (r observeResolver) SecretValue(ctx context.Context, namespace string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	value, err := r.Resolver.SecretValue(ctx, namespace, ref)
	if errors.Is(err, ErrReferenceNotReady) {
		return []byte{}, nil
	}
	return value, err
}

// Object wraps ErrReferenceNotReady, alongside the API error, when the referent does not exist.
func (r *readerResolver) Object(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	err := r.reader.Get(ctx, key, obj)
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: %w", ErrReferenceNotReady, err)
	}
	return err
}
