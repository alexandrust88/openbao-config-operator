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

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=workflows,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=workflows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=workflows/finalizers,verbs=update

// SetupWorkflow registers the Workflow controller with the manager.
func SetupWorkflow(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.Workflow](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("workflow-controller"),
		conns,
		WorkflowAdapter{},
		func() *sysv1alpha1.Workflow { return &sysv1alpha1.Workflow{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// WorkflowAdapter maps a Workflow onto sys/workflows/manage/<path>.
type WorkflowAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.Workflow]  = WorkflowAdapter{}
	_ engine.Observer[*sysv1alpha1.Workflow] = WorkflowAdapter{}
	_ engine.Writer[*sysv1alpha1.Workflow]   = WorkflowAdapter{}
)

// Path implements engine.Adapter.
func (WorkflowAdapter) Path(w *sysv1alpha1.Workflow) string {
	return "sys/workflows/manage/" + nameOrDefault(w.Spec.Path, w.Name)
}

// Desired implements engine.Adapter. Every write replaces the whole workflow, so all fields are sent.
func (WorkflowAdapter) Desired(_ context.Context, _ engine.Resolver, w *sysv1alpha1.Workflow) (engine.Payload, error) {
	return engine.Payload{
		"workflow":              w.Spec.Workflow,
		"description":           w.Spec.Description,
		keyCASRequired:          w.Spec.CASRequired,
		"allow_unauthenticated": w.Spec.AllowUnauthenticated,
	}, nil
}

// Fields implements engine.Adapter.
func (WorkflowAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer. A stored definition that differs from the spec only in whitespace is
// reported as the spec text.
func (a WorkflowAdapter) Observe(ctx context.Context, c openbao.Client, w *sysv1alpha1.Workflow) (engine.Payload, bool, error) {
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
func (a WorkflowAdapter) Write(ctx context.Context, c openbao.Client, w *sysv1alpha1.Workflow, desired engine.Payload,
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
