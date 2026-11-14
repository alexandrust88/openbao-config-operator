package sys

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=auditrequestheaders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=auditrequestheaders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=auditrequestheaders/finalizers,verbs=update

// auditHeaderMissing is the 400 message of a read of a header that is not audited.
const auditHeaderMissing = "Could not find header in config"

// SetupAuditRequestHeader registers the AuditRequestHeader controller with the manager.
func SetupAuditRequestHeader(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.AuditRequestHeader](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("auditrequestheader-controller"),
		conns,
		AuditRequestHeaderAdapter{},
		func() *sysv1alpha1.AuditRequestHeader { return &sysv1alpha1.AuditRequestHeader{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// AuditRequestHeaderAdapter maps an AuditRequestHeader onto sys/config/auditing/request-headers/<name>.
type AuditRequestHeaderAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.AuditRequestHeader]  = AuditRequestHeaderAdapter{}
	_ engine.Observer[*sysv1alpha1.AuditRequestHeader] = AuditRequestHeaderAdapter{}
)

// Path implements engine.Adapter.
func (AuditRequestHeaderAdapter) Path(h *sysv1alpha1.AuditRequestHeader) string {
	return "sys/config/auditing/request-headers/" + nameOrDefault(h.Spec.Name, h.Name)
}

// Desired implements engine.Adapter.
func (AuditRequestHeaderAdapter) Desired(_ context.Context, _ engine.Resolver, h *sysv1alpha1.AuditRequestHeader) (engine.Payload, error) {
	return engine.Payload{"hmac": h.Spec.HMAC}, nil
}

// Fields implements engine.Adapter.
func (AuditRequestHeaderAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer. A header that is not audited reads as a 400, and an audited one as
// {<name as requested>: {"hmac": bool}}.
func (a AuditRequestHeaderAdapter) Observe(ctx context.Context, c openbao.Client, h *sysv1alpha1.AuditRequestHeader) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "AuditRequestHeader"); err != nil {
		return nil, false, err
	}
	data, err := c.Read(ctx, a.Path(h))
	if errors.Is(err, openbao.ErrNotFound) || badRequestContains(err, auditHeaderMissing) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	settings, ok := data[nameOrDefault(h.Spec.Name, h.Name)].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	return engine.Payload{"hmac": settings["hmac"]}, true, nil
}
