package access

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/internal/refs"
)

// accessorPlaceholder stands in Desired for the Kubernetes auth accessor, which only a read can resolve.
const accessorPlaceholder = "<kubernetes-auth-accessor>"

// defaultGrantTokenTTL matches the CRD default of spec.tokenTTL.
const defaultGrantTokenTTL = 15 * time.Minute

// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accessgrants,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accessgrants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accessgrants/finalizers,verbs=update
// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accesslevels,verbs=get;list;watch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=authmounts,verbs=get;list;watch

// SetupAccessGrant registers the AccessGrant controller with the manager.
func SetupAccessGrant(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*accessv1alpha1.AccessGrant](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("accessgrant-controller"),
		conns,
		AccessGrantAdapter{Reader: mgr.GetClient()},
		func() *accessv1alpha1.AccessGrant { return &accessv1alpha1.AccessGrant{} },
		opts,
	)
	return r.SetupWithManager(mgr, func(b *builder.Builder) {
		b.Watches(&accessv1alpha1.AccessLevel{}, enqueueCiters(mgr.GetClient(), grantsCiting))
	})
}

// AccessGrantAdapter maps an AccessGrant onto one ACL policy and one Kubernetes auth role per subject.
type AccessGrantAdapter struct {
	// Reader reads the AuthMount named by spec.authMountRef.
	Reader refs.Reader
}

var (
	_ engine.Adapter[*accessv1alpha1.AccessGrant]      = AccessGrantAdapter{}
	_ engine.Observer[*accessv1alpha1.AccessGrant]     = AccessGrantAdapter{}
	_ engine.Writer[*accessv1alpha1.AccessGrant]       = AccessGrantAdapter{}
	_ engine.Deleter[*accessv1alpha1.AccessGrant]      = AccessGrantAdapter{}
	_ engine.StatusWriter[*accessv1alpha1.AccessGrant] = AccessGrantAdapter{}
	_ engine.DriftTier                                 = AccessGrantAdapter{}
)

// Path implements engine.Adapter.
func (AccessGrantAdapter) Path(g *accessv1alpha1.AccessGrant) string {
	return policyChild("", grantPolicyName(g)).Path
}

// Desired implements engine.Adapter. The policy text carries a placeholder for the auth accessor.
func (AccessGrantAdapter) Desired(ctx context.Context, r engine.Resolver, g *accessv1alpha1.AccessGrant) (engine.Payload, error) {
	levels, err := resolveLevels(ctx, r.Object, g.Namespace, grantLevels(g))
	if err != nil {
		return nil, err
	}
	var rules []policyRule
	for _, rule := range g.Spec.Rules {
		rules = append(rules, expandRule(rule.AccessPath, levels[rule.Level], rule.Organizations)...)
	}
	policy := grantPolicyName(g)
	desired := engine.Payload{
		childKey(policyChild("", policy)): renderPolicy(grantOwner(g), rules, accessorPlaceholder),
		staleKey:                          []any{},
	}
	for i, subject := range g.Spec.Subjects {
		role, err := grantRolePayload(subject, policy, g.Spec.TokenTTL)
		if err != nil {
			return nil, err
		}
		desired[childKey(grantRoleChild("", grantRoleName(g, i)))] = role
	}
	return desired, nil
}

// Fields implements engine.Adapter.
func (AccessGrantAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Partial: []string{"role:*"}}
}

// DriftTier implements engine.DriftTier: the grant decides who can read secrets.
func (AccessGrantAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer. The stored policy is reported with the accessor replaced by the
// placeholder, so it compares with Desired.
func (a AccessGrantAdapter) Observe(ctx context.Context, c openbao.Client, g *accessv1alpha1.AccessGrant) (engine.Payload, bool, error) {
	mountPath, accessor, err := a.mount(ctx, c, g)
	if err != nil {
		return nil, false, err
	}
	planned := grantChildren(g, mountPath)
	observed := engine.Payload{}
	var existing []accessv1alpha1.AccessChild
	for _, child := range planned {
		data, ok, err := readIfExists(ctx, c, child.Path)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		existing = append(existing, child)
		if child.Kind == accessv1alpha1.AccessChildPolicy {
			text, _ := data["policy"].(string)
			observed[childKey(child)] = strings.ReplaceAll(text, accessor, accessorPlaceholder)
			continue
		}
		observed[childKey(child)] = data
	}
	if err := checkChildren(g, g.Status.Children, existing); err != nil {
		return nil, false, err
	}
	observed[staleKey] = childPaths(staleChildren(g.Status.Children, planned))
	return observed, len(g.Status.Children) > 0 || len(existing) > 0, nil
}

// Write implements engine.Writer. It writes the policy before the roles, writes only the children that
// differ, deletes the children the spec dropped, and records every child it creates in the status as it goes.
func (a AccessGrantAdapter) Write(ctx context.Context, c openbao.Client, g *accessv1alpha1.AccessGrant, desired engine.Payload,
	_ bool) (engine.Payload, error) {
	mountPath, accessor, err := a.mount(ctx, c, g)
	if err != nil {
		return nil, err
	}
	current, _, err := a.Observe(ctx, c, g)
	if err != nil {
		return nil, err
	}
	planned := grantChildren(g, mountPath)
	for _, child := range planned {
		key := childKey(child)
		_, exists := current[key]
		if len(engine.Diff(engine.Payload{key: desired[key]}, current, a.Fields())) > 0 {
			payload, ok := desired[key].(map[string]any)
			if child.Kind == accessv1alpha1.AccessChildPolicy {
				text, _ := desired[key].(string)
				payload, ok = map[string]any{"policy": strings.ReplaceAll(text, accessorPlaceholder, accessor)}, true
			}
			if !ok {
				return nil, fmt.Errorf("desired %s has an unexpected shape", key)
			}
			if _, err := c.Write(ctx, child.Path, payload); err != nil {
				return nil, fmt.Errorf("write %s: %w", child.Path, err)
			}
		}
		if !exists {
			recordChild(&g.Status.Children, child)
		}
	}
	stale := staleChildren(g.Status.Children, planned)
	return nil, deleteChildren(ctx, c, stale, &g.Status.Children)
}

// Delete implements engine.Deleter. It deletes exactly the children recorded in the status.
func (AccessGrantAdapter) Delete(ctx context.Context, c openbao.Client, g *accessv1alpha1.AccessGrant) error {
	return deleteChildren(ctx, c, g.Status.Children, &g.Status.Children)
}

// WriteStatus implements engine.StatusWriter. The children ledger is kept by Write, which knows what it created.
func (AccessGrantAdapter) WriteStatus(_ context.Context, _ openbao.Client, g *accessv1alpha1.AccessGrant, _ engine.Payload) error {
	g.Status.Roles = make([]string, 0, len(g.Spec.Subjects))
	for i := range g.Spec.Subjects {
		g.Status.Roles = append(g.Status.Roles, grantRoleName(g, i))
	}
	return nil
}

// mount resolves the Kubernetes auth method of the grant to its path and accessor.
func (a AccessGrantAdapter) mount(ctx context.Context, c openbao.Client, g *accessv1alpha1.AccessGrant) (string, string, error) {
	accessor := g.Spec.MountAccessor
	if ref := g.Spec.AuthMountRef; ref != nil {
		if a.Reader == nil {
			return "", "", fmt.Errorf("authMountRef needs a reader")
		}
		var err error
		accessor, err = refs.WithClient(a.Reader, c, g.Namespace).One(ctx, refs.AuthMount, refs.Ref{ObjectName: ref.Name})
		if err != nil {
			return "", "", err
		}
	}
	return lookupAuthMount(ctx, c, g.Spec.MountPath, accessor, "kubernetes")
}

func grantPolicyName(g *accessv1alpha1.AccessGrant) string {
	return "grant_" + g.Namespace + "_" + g.Name
}

func grantRoleName(g *accessv1alpha1.AccessGrant, index int) string {
	return grantPolicyName(g) + "_" + strconv.Itoa(index)
}

func grantOwner(g *accessv1alpha1.AccessGrant) string {
	return "AccessGrant/" + g.Namespace + "/" + g.Name
}

func grantRoleChild(mountPath, role string) accessv1alpha1.AccessChild {
	return accessv1alpha1.AccessChild{Kind: accessv1alpha1.AccessChildKubernetesAuthRole,
		Path: "auth/" + mountPath + "/role/" + role}
}

// grantChildren lists the objects the spec plans, policy first.
func grantChildren(g *accessv1alpha1.AccessGrant, mountPath string) []accessv1alpha1.AccessChild {
	children := make([]accessv1alpha1.AccessChild, 0, 1+len(g.Spec.Subjects))
	children = append(children, policyChild("", grantPolicyName(g)))
	for i := range g.Spec.Subjects {
		children = append(children, grantRoleChild(mountPath, grantRoleName(g, i)))
	}
	return children
}

func grantLevels(g *accessv1alpha1.AccessGrant) []string {
	levels := make([]string, 0, len(g.Spec.Rules))
	for _, rule := range g.Spec.Rules {
		levels = append(levels, rule.Level)
	}
	return levels
}

// grantRolePayload sends both namespace keys, so switching between a list and a selector clears the other. The
// role write only changes the keys it is sent, so every token setting that could widen a token is pinned to its
// default; audience is left out because the read omits it when empty and setting it only narrows logins.
func grantRolePayload(s accessv1alpha1.AccessSubject, policy string, ttl *metav1.Duration) (engine.Payload, error) {
	role := engine.Payload{
		"bound_service_account_names":              []any{s.Name},
		"bound_service_account_namespaces":         []any{},
		"bound_service_account_namespace_selector": "",
		"token_policies":                           []any{policy},
		"token_ttl":                                int64(defaultGrantTokenTTL.Seconds()),
		"token_max_ttl":                            int64(0),
		"token_explicit_max_ttl":                   int64(0),
		"token_period":                             int64(0),
		"token_num_uses":                           int64(0),
		"token_bound_cidrs":                        []any{},
		"token_strictly_bind_ip":                   false,
		"token_no_default_policy":                  false,
		"token_type":                               "default",
		"alias_name_source":                        "serviceaccount_uid",
	}
	if s.NamespaceSelector != nil {
		selector, err := json.Marshal(map[string]any{"matchLabels": s.NamespaceSelector.MatchLabels})
		if err != nil {
			return nil, fmt.Errorf("encode namespaceSelector: %w", err)
		}
		role["bound_service_account_namespace_selector"] = string(selector)
	} else {
		role["bound_service_account_namespaces"] = []any{s.Namespace}
	}
	if ttl != nil {
		role["token_ttl"] = int64(ttl.Seconds())
	}
	return role, nil
}
