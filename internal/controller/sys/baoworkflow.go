package sys

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=baoworkflows,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=baoworkflows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=baoworkflows/finalizers,verbs=update

// SetupBaoWorkflow registers the BaoWorkflow controller with the manager.
func SetupBaoWorkflow(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.BaoWorkflow](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("baoworkflow-controller"),
		conns,
		BaoWorkflowAdapter{},
		func() *sysv1alpha1.BaoWorkflow { return &sysv1alpha1.BaoWorkflow{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// BaoWorkflowAdapter maps a BaoWorkflow onto sys/workflows/manage/<path>.
type BaoWorkflowAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.BaoWorkflow]  = BaoWorkflowAdapter{}
	_ engine.Observer[*sysv1alpha1.BaoWorkflow] = BaoWorkflowAdapter{}
	_ engine.Writer[*sysv1alpha1.BaoWorkflow]   = BaoWorkflowAdapter{}
)

// Path implements engine.Adapter.
func (BaoWorkflowAdapter) Path(w *sysv1alpha1.BaoWorkflow) string {
	return "sys/workflows/manage/" + nameOrDefault(w.Spec.Path, w.Name)
}

// Desired implements engine.Adapter. Every write replaces the whole workflow, so all fields are sent.
func (BaoWorkflowAdapter) Desired(_ context.Context, _ engine.Resolver, w *sysv1alpha1.BaoWorkflow) (engine.Payload, error) {
	return engine.Payload{
		"workflow":              w.Spec.Workflow,
		"description":           w.Spec.Description,
		keyCASRequired:          w.Spec.CASRequired,
		"allow_unauthenticated": w.Spec.AllowUnauthenticated,
	}, nil
}

// Fields implements engine.Adapter.
func (BaoWorkflowAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer. A stored definition that differs from the spec only in whitespace is
// reported as the spec text.
func (a BaoWorkflowAdapter) Observe(ctx context.Context, c openbao.Client, w *sysv1alpha1.BaoWorkflow) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(w))
	if errors.Is(err, openbao.ErrNotFound) && !errors.Is(err, openbao.ErrNamespaceNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if stored, ok := observed["workflow"].(string); ok && normalizePolicy(stored) == normalizePolicy(w.Spec.Workflow) {
		observed["workflow"] = w.Spec.Workflow
	}
	return observed, true, nil
}

// Write implements engine.Writer. It always sends a check-and-set value, so workflows with casRequired can be
// updated: -1 (must not exist) on create, the current version on update. A 404 means the server has no
// workflows API, which is the case on HashiCorp Vault.
func (a BaoWorkflowAdapter) Write(ctx context.Context, c openbao.Client, w *sysv1alpha1.BaoWorkflow, desired engine.Payload,
	create bool) (engine.Payload, error) {
	path := a.Path(w)
	response, err := writeWithCAS(ctx, c, path, desired, create)
	if statusIs(err, http.StatusNotFound) && !errors.Is(err, openbao.ErrNamespaceNotFound) {
		return nil, fmt.Errorf("the server has no sys/workflows API (OpenBao only): %w: %w", engine.ErrUnsupported, err)
	}
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return response, nil
}
