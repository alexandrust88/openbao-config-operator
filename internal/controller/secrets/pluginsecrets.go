package secrets

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// setupPlugin registers the engine controller of one external-plugin kind.
func setupPlugin[T engine.Object](mgr ctrl.Manager, conns engine.Connections, opts engine.Options, name string,
	adapter engine.Adapter[T], newObj func() T) error {
	r := engine.New[T](mgr.GetClient(), mgr.GetScheme(), mgr.GetEventRecorder(name+"-controller"), conns, adapter, newObj, opts)
	return r.SetupWithManager(mgr)
}

// pluginMountPath falls back to the CRD default when the object was not defaulted by the API server.
func pluginMountPath(mountPath, fallback string) string {
	if mountPath == "" {
		return fallback
	}
	return mountPath
}

func putPluginList[E any](p engine.Payload, key string, value []E) {
	if value != nil {
		p[key] = value
	}
}

// putPluginSecret resolves ref and stores its value under key; a missing Secret wraps engine.ErrReferenceNotReady.
func putPluginSecret(ctx context.Context, r engine.Resolver, namespace string, p engine.Payload, key string,
	ref *commonv1alpha1.SecretKeySelector) error {
	if ref == nil {
		return nil
	}
	value, err := r.SecretValue(ctx, namespace, *ref)
	if err != nil {
		return err
	}
	p[key] = string(value)
	return nil
}

func pluginSecretNames(refs ...*commonv1alpha1.SecretKeySelector) []string {
	var names []string
	for _, ref := range refs {
		if ref != nil {
			names = append(names, ref.Name)
		}
	}
	return names
}
