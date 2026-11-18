package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// RootOnly is implemented by adapters of kinds whose OpenBao endpoints exist only in the root namespace (audit
// devices, the plugin catalog, quotas, sys configuration singletons, raft). The engine refuses such a resource whose
// effective OpenBao namespace is a child namespace with the reason RootOnlyKind, before any OpenBao call for it.
type RootOnly interface {
	RootOnly() bool
}

// ErrRootOnly wraps ErrInvalidSpec: a root-only kind can never work in a child namespace.
var ErrRootOnly = fmt.Errorf("the kind exists only in the root OpenBao namespace: %w", ErrInvalidSpec)

// RootOnlyError returns an error wrapping ErrRootOnly when adapter is RootOnly and namespace, an absolute OpenBao
// namespace, is not the root namespace.
func RootOnlyError(adapter any, kind, namespace string) error {
	ro, ok := adapter.(RootOnly)
	if !ok || !ro.RootOnly() {
		return nil
	}
	if ns := strings.Trim(namespace, "/"); ns != "" && ns != "root" {
		return fmt.Errorf("%s: OpenBao namespace %q is not the root namespace; use a connection based in the root "+
			"namespace and no spec.openbaoNamespace: %w", kind, ns, ErrRootOnly)
	}
	return nil
}

// refuseRootOnly stops the reconcile of a root-only kind whose effective namespace (p.bao) is not the root. It runs
// once the connection and spec.openbaoNamespace are resolved and before the first OpenBao call for the resource.
func (r *Reconciler[T]) refuseRootOnly(ctx context.Context, p *pass[T]) (ctrl.Result, bool, error) {
	cause := RootOnlyError(r.adapter, r.kind, p.bao.Namespace())
	if cause == nil {
		return ctrl.Result{}, false, nil
	}
	r.setConditions(p.obj, metav1.ConditionFalse, commonv1alpha1.ReasonRootOnlyKind,
		metav1.ConditionFalse, commonv1alpha1.ReasonRootOnlyKind, cause.Error())
	if err := r.patchStatus(ctx, p.obj, p.before); err != nil {
		return ctrl.Result{}, true, errors.Join(cause, err)
	}
	return ctrl.Result{}, true, reconcile.TerminalError(cause)
}
