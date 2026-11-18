package sys

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=uiheaders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=uiheaders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=uiheaders/finalizers,verbs=update

const uiHeaderValues = "values"

// SetupUIHeader registers the UIHeader controller with the manager.
func SetupUIHeader(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.UIHeader](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("uiheader-controller"),
		conns,
		UIHeaderAdapter{},
		func() *sysv1alpha1.UIHeader { return &sysv1alpha1.UIHeader{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// UIHeaderAdapter maps a UIHeader onto sys/config/ui/headers/<name>.
type UIHeaderAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.UIHeader]  = UIHeaderAdapter{}
	_ engine.Observer[*sysv1alpha1.UIHeader] = UIHeaderAdapter{}
)

// Path implements engine.Adapter.
func (UIHeaderAdapter) Path(h *sysv1alpha1.UIHeader) string {
	return "sys/config/ui/headers/" + nameOrDefault(h.Spec.Name, h.Name)
}

// Desired implements engine.Adapter.
func (UIHeaderAdapter) Desired(_ context.Context, _ engine.Resolver, h *sysv1alpha1.UIHeader) (engine.Payload, error) {
	return engine.Payload{uiHeaderValues: h.Spec.Values}, nil
}

// Fields implements engine.Adapter.
func (UIHeaderAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer. A plain read returns only the first value of the header (the
// multivalue query parameter is out of reach of openbao.Client), so a matching first value is reported as
// the spec's values.
func (a UIHeaderAdapter) Observe(ctx context.Context, c openbao.Client, h *sysv1alpha1.UIHeader) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "UIHeader"); err != nil {
		return nil, false, err
	}
	observed, err := c.Read(ctx, a.Path(h))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	first, _ := observed["value"].(string)
	if len(h.Spec.Values) > 0 && first == h.Spec.Values[0] {
		return engine.Payload{uiHeaderValues: h.Spec.Values}, true, nil
	}
	return engine.Payload{uiHeaderValues: []string{first}}, true, nil
}
