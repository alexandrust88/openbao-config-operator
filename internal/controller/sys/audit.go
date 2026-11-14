package sys

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=audits,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=audits/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=audits/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// 400 messages of sys/audit/<path> (handleEnableAudit, handleDisableAudit).
const (
	auditAPICreationRefused = "cannot enable audit device via API"
	auditPrefixRefused      = "audit log prefixing is not allowed"
	auditConfigManaged      = "cannot disable configuration-managed audit device"
)

const (
	auditTablePath = "sys/audit"
	keyDescription = "description"
	keyLocal       = "local"
	keyOptions     = "options"
)

// SetupAudit registers the Audit controller with the manager.
func SetupAudit(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.Audit](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("audit-controller"),
		conns,
		AuditAdapter{},
		func() *sysv1alpha1.Audit { return &sysv1alpha1.Audit{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// AuditAdapter maps an Audit onto sys/audit/<path>. OpenBao has no read and no update for a single audit device:
// the Observer reads the whole table, and the Writer only enables.
type AuditAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.Audit]          = AuditAdapter{}
	_ engine.Observer[*sysv1alpha1.Audit]         = AuditAdapter{}
	_ engine.Writer[*sysv1alpha1.Audit]           = AuditAdapter{}
	_ engine.Deleter[*sysv1alpha1.Audit]          = AuditAdapter{}
	_ engine.SecretReferencer[*sysv1alpha1.Audit] = AuditAdapter{}
	_ engine.Destructive                          = AuditAdapter{}
	_ engine.DriftTier                            = AuditAdapter{}
)

// Path implements engine.Adapter.
func (AuditAdapter) Path(a *sysv1alpha1.Audit) string {
	return "sys/audit/" + nameOrDefault(a.Spec.Path, a.Name)
}

// Desired implements engine.Adapter. Options from optionsSecretRefs are merged into options: OpenBao returns
// every option in the audit table, so they are compared like the others and a changed Secret shows as drift.
func (AuditAdapter) Desired(ctx context.Context, r engine.Resolver, a *sysv1alpha1.Audit) (engine.Payload, error) {
	options := make(map[string]string, len(a.Spec.Options)+len(a.Spec.OptionsSecretRefs))
	for key, value := range a.Spec.Options {
		options[key] = string(value)
	}
	for _, key := range slices.Sorted(maps.Keys(a.Spec.OptionsSecretRefs)) {
		value, err := r.SecretValue(ctx, a.Namespace, a.Spec.OptionsSecretRefs[key])
		if err != nil {
			return nil, fmt.Errorf("optionsSecretRefs.%s: %w", key, err)
		}
		options[key] = string(value)
	}
	return engine.Payload{
		keyType:        string(a.Spec.Type),
		keyDescription: a.Spec.Description,
		keyLocal:       a.Spec.Local,
		keyOptions:     options,
	}, nil
}

// Fields implements engine.Adapter.
func (AuditAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{}
}

// Destructive implements engine.Destructive: disabling an audit device stops the audit trail.
func (AuditAdapter) Destructive() bool { return true }

// DriftTier implements engine.DriftTier.
func (AuditAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer.
func (AuditAdapter) SecretRefs(a *sysv1alpha1.Audit) []string {
	names := make([]string, 0, len(a.Spec.OptionsSecretRefs))
	for _, ref := range a.Spec.OptionsSecretRefs {
		names = append(names, ref.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// Observe implements engine.Observer: it picks <path>/ from the audit table.
func (a AuditAdapter) Observe(ctx context.Context, c openbao.Client, obj *sysv1alpha1.Audit) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "Audit"); err != nil {
		return nil, false, err
	}
	table, err := c.Read(ctx, auditTablePath)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	device, ok := table[nameOrDefault(obj.Spec.Path, obj.Name)+"/"].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	return engine.Payload{
		keyType:        device[keyType],
		keyDescription: device[keyDescription],
		keyLocal:       device[keyLocal],
		keyOptions:     device[keyOptions],
	}, true, nil
}

// Write implements engine.Writer. An audit device cannot be changed in place, and disabling it to enable it again
// would leave a gap in the audit trail, so a difference is reported instead of repaired.
func (a AuditAdapter) Write(ctx context.Context, c openbao.Client, obj *sysv1alpha1.Audit, desired engine.Payload, create bool) (engine.Payload, error) {
	path := a.Path(obj)
	if !create {
		return nil, fmt.Errorf("audit device %s differs from the spec and OpenBao cannot change an audit device in "+
			"place: make the spec match, or disable the device in OpenBao so that the operator enables it again: %w",
			path, engine.ErrUnsupported)
	}
	_, err := c.Write(ctx, path, desired)
	switch {
	case badRequestContains(err, auditAPICreationRefused):
		return nil, fmt.Errorf("OpenBao refuses to enable audit devices through the API: set "+
			"unsafe_allow_api_audit_creation = true in the server configuration, or declare the device there and "+
			"use creationPolicy Adopt: %w", engine.ErrUnsupported)
	case badRequestContains(err, auditPrefixRefused):
		return nil, fmt.Errorf("the prefix option needs allow_audit_log_prefixing = true in the server "+
			"configuration: %w", engine.ErrUnsupported)
	case err != nil:
		return nil, fmt.Errorf("enable %s: %w", path, err)
	}
	return nil, nil
}

// Delete implements engine.Deleter.
func (a AuditAdapter) Delete(ctx context.Context, c openbao.Client, obj *sysv1alpha1.Audit) error {
	path := a.Path(obj)
	err := c.Delete(ctx, path)
	if badRequestContains(err, auditConfigManaged) {
		return fmt.Errorf("audit device %s is declared in the server configuration and cannot be disabled through "+
			"the API; set deletionPolicy to Orphan to release it: %w", path, err)
	}
	return err
}
