package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=core.openbaoconfig.io,resources=openbaoendpoints,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.openbaoconfig.io,resources=openbaoendpoints/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.openbaoconfig.io,resources=openbaoendpoints/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupOpenBaoEndpoint registers the OpenBaoEndpoint controller with the manager.
func SetupOpenBaoEndpoint(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*corev1alpha1.OpenBaoEndpoint](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("openbaoendpoint-controller"),
		conns,
		OpenBaoEndpointAdapter{Secrets: mgr.GetClient()},
		func() *corev1alpha1.OpenBaoEndpoint { return &corev1alpha1.OpenBaoEndpoint{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OpenBaoEndpointAdapter maps an OpenBaoEndpoint onto its path. Secrets reads dataSecretRef again during Observe,
// because the write-only keys of an OpenBaoEndpoint depend on the Secret's content.
type OpenBaoEndpointAdapter struct {
	Secrets client.Reader
}

var (
	_ engine.Adapter[*corev1alpha1.OpenBaoEndpoint]  = OpenBaoEndpointAdapter{}
	_ engine.Observer[*corev1alpha1.OpenBaoEndpoint] = OpenBaoEndpointAdapter{}
	_ engine.Deleter[*corev1alpha1.OpenBaoEndpoint]  = OpenBaoEndpointAdapter{}
	_ engine.Destructive                             = OpenBaoEndpointAdapter{}

	_ engine.ReadBackSkipper[*corev1alpha1.OpenBaoEndpoint] = OpenBaoEndpointAdapter{}

	_ engine.SecretReferencer[*corev1alpha1.OpenBaoEndpoint] = OpenBaoEndpointAdapter{}
)

// Destructive implements engine.Destructive: path and deletePath are free-form, so a delete can unmount a secrets
// engine or remove a namespace.
func (OpenBaoEndpointAdapter) Destructive() bool { return true }

// Path implements engine.Adapter.
func (OpenBaoEndpointAdapter) Path(e *corev1alpha1.OpenBaoEndpoint) string { return e.Spec.Path }

// Desired implements engine.Adapter: spec.data with the dataSecretRef object merged over it.
func (OpenBaoEndpointAdapter) Desired(ctx context.Context, r engine.Resolver, e *corev1alpha1.OpenBaoEndpoint) (engine.Payload, error) {
	desired, _, err := endpointPayload(ctx, r, e)
	return desired, err
}

// Fields implements engine.Adapter. Write-only keys are per object; Observe handles them.
func (OpenBaoEndpointAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// FieldsFor returns the per-object comparison rules: writeOnlyFields plus every key of dataSecretRef.
func (a OpenBaoEndpointAdapter) FieldsFor(ctx context.Context, r engine.Resolver, e *corev1alpha1.OpenBaoEndpoint) (engine.FieldSpec, error) {
	_, writeOnly, err := endpointPayload(ctx, r, e)
	return engine.FieldSpec{WriteOnly: writeOnly}, err
}

// Observe implements engine.Observer.
//
// The engine compares every desired key, so keys a read cannot be expected to return (absent keys under
// ignoreAbsentFields, everything under disableRead) are reported with their desired value. Under disableRead a
// spec change still forces a write: the desired value is reported only once the current generation has been
// reconciled.
func (a OpenBaoEndpointAdapter) Observe(ctx context.Context, c openbao.Client, e *corev1alpha1.OpenBaoEndpoint) (engine.Payload, bool, error) {
	desired, _, err := endpointPayload(ctx, engine.NewResolver(a.Secrets), e)
	if err != nil {
		return nil, false, err
	}

	if e.Spec.DisableRead {
		if e.Status.Acquired == "" {
			return nil, false, nil
		}
		if !engine.Reconciled(e) {
			return engine.Payload{}, true, nil
		}
		return desired, true, nil
	}

	readPath := e.Spec.ReadPath
	if readPath == "" {
		readPath = e.Spec.Path
	}
	observed, err := c.Read(ctx, readPath)
	if errors.Is(err, openbao.ErrNotFound) || mountAbsent(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	if e.Spec.IgnoreAbsentFields {
		for key, value := range desired {
			if _, present := observed[key]; !present {
				observed[key] = value
			}
		}
	}
	return observed, true, nil
}

// mountAbsent reports the 400 that a read of sys/mounts/<path> or sys/auth/<path> returns for a mount that does not
// exist; every other 400 stays an error.
func mountAbsent(err error) bool {
	var re *openbao.ResponseError
	if !errors.As(err, &re) || re.StatusCode != http.StatusBadRequest || len(re.Errors) != 1 {
		return false
	}
	return strings.HasPrefix(re.Errors[0], "No secret engine mount at ") || strings.HasPrefix(re.Errors[0], "No auth engine at ")
}

// SkipReadBack implements engine.ReadBackSkipper: a disableRead object cannot be read to confirm a write.
func (OpenBaoEndpointAdapter) SkipReadBack(e *corev1alpha1.OpenBaoEndpoint) bool {
	return e.Spec.DisableRead
}

// SecretRefs implements engine.SecretReferencer.
func (OpenBaoEndpointAdapter) SecretRefs(e *corev1alpha1.OpenBaoEndpoint) []string {
	if e.Spec.DataSecretRef == nil {
		return nil
	}
	return []string{e.Spec.DataSecretRef.Name}
}

// Delete implements engine.Deleter.
func (OpenBaoEndpointAdapter) Delete(ctx context.Context, c openbao.Client, e *corev1alpha1.OpenBaoEndpoint) error {
	if e.Spec.DisableDelete {
		return nil
	}
	path := e.Spec.DeletePath
	if path == "" {
		path = e.Spec.Path
	}
	if err := c.Delete(ctx, path); err != nil && !errors.Is(err, openbao.ErrNotFound) {
		return err
	}
	return nil
}

// endpointPayload merges the dataSecretRef object over spec.data and returns the sorted write-only keys.
func endpointPayload(ctx context.Context, r engine.Resolver, e *corev1alpha1.OpenBaoEndpoint) (engine.Payload, []string, error) {
	payload := engine.Payload{}
	if e.Spec.Data != nil && len(e.Spec.Data.Raw) > 0 {
		if err := decodeObject(e.Spec.Data.Raw, &payload); err != nil {
			return nil, nil, fmt.Errorf("spec.data: %w: %w", engine.ErrInvalidSpec, err)
		}
	}
	writeOnly := slices.Clone(e.Spec.WriteOnlyFields)
	if ref := e.Spec.DataSecretRef; ref != nil {
		raw, err := r.SecretValue(ctx, e.Namespace, *ref)
		if err != nil {
			return nil, nil, err
		}
		secretData := engine.Payload{}
		if err := decodeObject(raw, &secretData); err != nil {
			return nil, nil, fmt.Errorf("secret %s key %s: %w: %w", ref.Name, ref.Key, engine.ErrInvalidSpec, err)
		}
		maps.Copy(payload, secretData)
		writeOnly = append(writeOnly, slices.Collect(maps.Keys(secretData))...)
	}
	slices.Sort(writeOnly)
	return payload, slices.Compact(writeOnly), nil
}

// decodeObject decodes a JSON object; the error never includes the input, which may be secret.
func decodeObject(raw []byte, into *engine.Payload) error {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return errors.New("must be a JSON object")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return errors.New("invalid JSON object")
	}
	return nil
}
