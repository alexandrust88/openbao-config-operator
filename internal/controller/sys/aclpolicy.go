// Package sys holds the adapters for the sys.obco.dev kinds.
package sys

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=aclpolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=aclpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=aclpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=sys.obco.dev,resources=authmounts,verbs=get;list;watch

// SetupACLPolicy registers the ACLPolicy controller with the manager.
func SetupACLPolicy(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.ACLPolicy](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("aclpolicy-controller"),
		conns,
		ACLPolicyAdapter{Reader: mgr.GetClient(), Conns: conns},
		func() *sysv1alpha1.ACLPolicy { return &sysv1alpha1.ACLPolicy{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// ACLPolicyAdapter maps an ACLPolicy onto sys/policies/acl/<name>. Reader and Conns resolve spec.accessorRefs; a
// zero ACLPolicyAdapter serves policies without them.
type ACLPolicyAdapter struct {
	Reader refs.Reader
	Conns  engine.Connections
}

var (
	_ engine.Adapter[*sysv1alpha1.ACLPolicy]          = ACLPolicyAdapter{}
	_ engine.Observer[*sysv1alpha1.ACLPolicy]         = ACLPolicyAdapter{}
	_ engine.Writer[*sysv1alpha1.ACLPolicy]           = ACLPolicyAdapter{}
	_ engine.StatusWriter[*sysv1alpha1.ACLPolicy]     = ACLPolicyAdapter{}
	_ engine.ReferenceWatcher[*sysv1alpha1.ACLPolicy] = ACLPolicyAdapter{}
	_ engine.DriftTier                                = ACLPolicyAdapter{}
)

// accessorPlaceholder matches ${accessor:<name>}; OpenBao's own {{...}} templates never match it.
var accessorPlaceholder = regexp.MustCompile(`\$\{accessor:([^}]*)\}`)

// Path implements engine.Adapter.
func (ACLPolicyAdapter) Path(p *sysv1alpha1.ACLPolicy) string {
	return "sys/policies/acl/" + policyName(p)
}

// Desired implements engine.Adapter. The spec text is written verbatim, with the accessor placeholders replaced,
// so that its formatting is kept. A write replaces the whole policy, so the flags are always sent.
func (a ACLPolicyAdapter) Desired(ctx context.Context, _ engine.Resolver, p *sysv1alpha1.ACLPolicy) (engine.Payload, error) {
	var text string
	var err error
	if a.Conns == nil {
		text, err = renderPolicy(ctx, refs.Resolver{Reader: a.Reader, Namespace: p.Namespace}, p)
	} else {
		text, err = renderPolicy(ctx, refs.For(a.Reader, a.Conns, p), p)
	}
	if err != nil {
		return nil, err
	}
	return engine.Payload{
		keyPolicy:                               text,
		keyCASRequired:                          p.Spec.CASRequired,
		"allow_wildcards_in_identity_templates": p.Spec.AllowWildcardsInIdentityTemplates,
		"allow_slashes_in_identity_templates":   p.Spec.AllowSlashesInIdentityTemplates,
	}, nil
}

// Fields implements engine.Adapter.
func (ACLPolicyAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer. A stored policy that differs from the rendered spec only in whitespace is
// reported as the rendered spec, so the engine does not rewrite it.
func (a ACLPolicyAdapter) Observe(ctx context.Context, c openbao.Client, p *sysv1alpha1.ACLPolicy) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, a.Path(p))
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	text, err := renderPolicy(ctx, refs.WithClient(a.Reader, c, p.Namespace), p)
	if err != nil {
		return nil, false, err
	}
	if stored, ok := observed[keyPolicy].(string); ok && normalizePolicy(stored) == normalizePolicy(text) {
		observed[keyPolicy] = text
	}
	return observed, true, nil
}

// WriteStatus implements engine.StatusWriter: status.id is the policy name as OpenBao stores it, so other kinds
// can reference the ACLPolicy through internal/refs.
func (ACLPolicyAdapter) WriteStatus(_ context.Context, _ openbao.Client, p *sysv1alpha1.ACLPolicy, _ engine.Payload) error {
	p.Status.ID = strings.ToLower(policyName(p))
	return nil
}

// ReferencedKinds implements engine.ReferenceWatcher.
func (ACLPolicyAdapter) ReferencedKinds() []client.Object {
	return []client.Object{&sysv1alpha1.AuthMount{}}
}

// References implements engine.ReferenceWatcher: the AuthMounts that accessorRefs names, so a recreated mount
// re-renders the policy at once.
func (ACLPolicyAdapter) References(p *sysv1alpha1.ACLPolicy) []engine.ObjectReference {
	var out []engine.ObjectReference
	for _, ref := range p.Spec.AccessorRefs {
		if ref.AuthMountRef != nil {
			out = append(out, engine.ObjectReference{Kind: refs.AuthMount.GVK.Kind, Name: ref.AuthMountRef.Name})
		}
	}
	return out
}

// renderPolicy replaces every ${accessor:<name>} in the policy with the accessor of the auth mount that
// spec.accessorRefs names. An unknown name can never resolve; a missing mount waits as ErrReferenceNotReady.
func renderPolicy(ctx context.Context, r refs.Resolver, p *sysv1alpha1.ACLPolicy) (string, error) {
	text := p.Spec.Policy
	accessors := map[string]string{}
	for _, match := range accessorPlaceholder.FindAllStringSubmatch(text, -1) {
		name := match[1]
		if _, done := accessors[name]; done {
			continue
		}
		ref, ok := p.Spec.AccessorRefs[name]
		if ok && r.Reader == nil {
			return "", fmt.Errorf("spec.accessorRefs[%s] cannot be resolved without a Kubernetes reader", name)
		}
		if !ok {
			return "", fmt.Errorf("spec.policy uses ${accessor:%s}, which spec.accessorRefs does not name: %w", name,
				engine.ErrInvalidSpec)
		}
		accessor, err := r.One(ctx, refs.AuthMount, refs.Ref{Name: ref.MountPath, ObjectName: authMountRefName(ref)})
		if err != nil {
			return "", fmt.Errorf("spec.accessorRefs[%s]: %w", name, err)
		}
		accessors[name] = accessor
	}
	if len(accessors) == 0 {
		return text, nil
	}
	return accessorPlaceholder.ReplaceAllStringFunc(text, func(m string) string {
		return accessors[accessorPlaceholder.FindStringSubmatch(m)[1]]
	}), nil
}

func authMountRefName(ref sysv1alpha1.ACLPolicyAccessorRef) string {
	if ref.AuthMountRef == nil {
		return ""
	}
	return ref.AuthMountRef.Name
}

// Write implements engine.Writer with a check-and-set value, so a policy with casRequired can be written.
func (a ACLPolicyAdapter) Write(ctx context.Context, c openbao.Client, p *sysv1alpha1.ACLPolicy, desired engine.Payload,
	create bool) (engine.Payload, error) {
	return writeWithCAS(ctx, c, a.Path(p), desired, create)
}

func policyName(p *sysv1alpha1.ACLPolicy) string {
	if p.Spec.Name != "" {
		return p.Spec.Name
	}
	return p.Name
}

// normalizePolicy drops line endings, indentation, trailing spaces and blank lines.
func normalizePolicy(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// DriftTier implements engine.DriftTier: it grants access, so drift is checked on the short interval.
func (ACLPolicyAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }
