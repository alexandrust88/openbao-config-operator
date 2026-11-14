package access

import (
	"context"
	"fmt"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accesslayouts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accesslayouts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accesslayouts/finalizers,verbs=update
// +kubebuilder:rbac:groups=access.openbaoconfig.io,resources=accesslevels,verbs=get;list;watch

// SetupAccessLayout registers the AccessLayout controller with the manager.
func SetupAccessLayout(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*accessv1alpha1.AccessLayout](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("accesslayout-controller"),
		conns,
		AccessLayoutAdapter{},
		func() *accessv1alpha1.AccessLayout { return &accessv1alpha1.AccessLayout{} },
		opts,
	)
	return r.SetupWithManager(mgr, func(b *builder.Builder) {
		b.Watches(&accessv1alpha1.AccessLevel{}, enqueueCiters(mgr.GetClient(), layoutsCiting))
		// The generation predicate of the engine hides annotation changes, which unblock removals.
		b.Watches(&accessv1alpha1.AccessLayout{}, &handler.EnqueueRequestForObject{},
			builder.WithPredicates(predicate.AnnotationChangedPredicate{}))
	})
}

// AccessLayoutAdapter maps an AccessLayout onto ACL policies and external groups with group aliases.
type AccessLayoutAdapter struct{}

var (
	_ engine.Adapter[*accessv1alpha1.AccessLayout]      = AccessLayoutAdapter{}
	_ engine.Observer[*accessv1alpha1.AccessLayout]     = AccessLayoutAdapter{}
	_ engine.Writer[*accessv1alpha1.AccessLayout]       = AccessLayoutAdapter{}
	_ engine.Deleter[*accessv1alpha1.AccessLayout]      = AccessLayoutAdapter{}
	_ engine.StatusWriter[*accessv1alpha1.AccessLayout] = AccessLayoutAdapter{}
	_ engine.DriftTier                                  = AccessLayoutAdapter{}
)

// layoutCell is one role, OpenBao namespace and organization of the matrix.
type layoutCell struct {
	namespace string
	role      string
	org       string
	level     string
	policy    string
}

// Path implements engine.Adapter. It is the name prefix of every policy of the layout.
func (AccessLayoutAdapter) Path(l *accessv1alpha1.AccessLayout) string {
	return policyChild("", layoutBase(l)).Path
}

// Desired implements engine.Adapter. It refuses to plan away access the layout granted before unless the
// layout carries the allow-disruptive-update annotation.
func (AccessLayoutAdapter) Desired(ctx context.Context, r engine.Resolver, l *accessv1alpha1.AccessLayout) (engine.Payload, error) {
	levels, err := resolveLevels(ctx, r.Object, l.Namespace, layoutLevels(l))
	if err != nil {
		return nil, err
	}
	stale := staleChildren(l.Status.Children, layoutChildren(l))
	if len(stale) > 0 && l.Annotations[accessv1alpha1.AllowDisruptiveUpdateAnnotation] != "true" {
		return nil, fmt.Errorf("%w: removing access requires the annotation %s: \"true\"; it would delete %s",
			engine.ErrInvalidSpec, accessv1alpha1.AllowDisruptiveUpdateAnnotation, strings.Join(childPaths(stale), ", "))
	}

	owner := layoutOwner(l)
	desired := engine.Payload{staleKey: []any{}}
	for _, cell := range layoutCells(l) {
		var orgs []string
		if cell.org != "" {
			orgs = []string{cell.org}
		}
		var rules []policyRule
		for _, p := range l.Spec.Paths {
			rules = append(rules, expandRule(p, levels[cell.level], orgs)...)
		}
		desired[childKey(policyChild(cell.namespace, cell.policy))] = renderPolicy(owner, rules, "")
		for _, mount := range l.Spec.AuthMounts {
			desired[childKey(groupChild(cell.namespace, cell.policy, mount))] = map[string]any{
				"type":     "external",
				"policies": []any{cell.policy},
				"metadata": map[string]any{"managed-by": managedBy, "owner": owner},
				"alias":    map[string]any{"name": layoutAlias(l, cell), "mount": mount},
			}
		}
	}
	return desired, nil
}

// Fields implements engine.Adapter.
func (AccessLayoutAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// DriftTier implements engine.DriftTier: the layout decides who can read secrets.
func (AccessLayoutAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// Observe implements engine.Observer.
func (AccessLayoutAdapter) Observe(ctx context.Context, c openbao.Client, l *accessv1alpha1.AccessLayout) (engine.Payload, bool, error) {
	obs, err := observeLayout(ctx, c, l)
	if err != nil {
		return nil, false, err
	}
	return obs.payload, obs.found, nil
}

// Write implements engine.Writer. It resolves every auth accessor first, writes policies before groups
// and groups before aliases, deletes the children the spec dropped, and records children as it goes.
func (AccessLayoutAdapter) Write(ctx context.Context, c openbao.Client, l *accessv1alpha1.AccessLayout, desired engine.Payload,
	_ bool) (engine.Payload, error) {
	obs, err := observeLayout(ctx, c, l)
	if err != nil {
		return nil, err
	}
	accessors := map[string]string{}
	for _, cell := range layoutCells(l) {
		for _, mount := range l.Spec.AuthMounts {
			key := cell.namespace + "\x00" + mount
			if _, ok := accessors[key]; ok {
				continue
			}
			_, accessor, err := lookupAuthMount(ctx, inNamespace(c, cell.namespace), mount, "", "jwt", "oidc")
			if err != nil {
				return nil, err
			}
			accessors[key] = accessor
		}
	}

	planned := layoutChildren(l)
	for _, child := range planned {
		key := childKey(child)
		if child.Kind != accessv1alpha1.AccessChildPolicy {
			continue
		}
		if len(engine.Diff(engine.Payload{key: desired[key]}, obs.payload, engine.FieldSpec{})) > 0 {
			if _, err := inNamespace(c, child.Namespace).Write(ctx, child.Path, map[string]any{"policy": desired[key]}); err != nil {
				return nil, fmt.Errorf("write %s: %w", childKey(child), err)
			}
		}
		if _, exists := obs.payload[key]; !exists {
			recordChild(&l.Status.Children, child)
		}
	}
	for _, child := range planned {
		if child.Kind != accessv1alpha1.AccessChildGroup {
			continue
		}
		want, ok := desired[childKey(child)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("desired %s has an unexpected shape", childKey(child))
		}
		alias, _ := want["alias"].(map[string]any)
		mount, _ := alias["mount"].(string)
		if err := writeLayoutGroup(ctx, inNamespace(c, child.Namespace), child, want, obs,
			accessors[child.Namespace+"\x00"+mount], &l.Status.Children); err != nil {
			return nil, err
		}
	}
	return nil, deleteChildren(ctx, c, staleChildren(l.Status.Children, planned), &l.Status.Children)
}

// Delete implements engine.Deleter. It deletes exactly the children recorded in the status; deleting a
// group also deletes its alias.
func (AccessLayoutAdapter) Delete(ctx context.Context, c openbao.Client, l *accessv1alpha1.AccessLayout) error {
	return deleteChildren(ctx, c, l.Status.Children, &l.Status.Children)
}

// WriteStatus implements engine.StatusWriter. The children ledger is kept by Write, which knows what it created.
func (AccessLayoutAdapter) WriteStatus(context.Context, openbao.Client, *accessv1alpha1.AccessLayout, engine.Payload) error {
	return nil
}

// writeLayoutGroup writes the group when it is missing or differs, then its alias when that differs.
func writeLayoutGroup(ctx context.Context, c openbao.Client, child accessv1alpha1.AccessChild, want map[string]any,
	obs layoutObservation, accessor string, recorded *[]accessv1alpha1.AccessChild) error {
	key := childKey(child)
	got, exists := obs.payload[key].(map[string]any)
	groupID := obs.groupIDs[key]
	group := engine.Payload{"type": want["type"], "policies": want["policies"], "metadata": want["metadata"]}
	if groupID == "" || len(engine.Diff(group, got, engine.FieldSpec{})) > 0 {
		response, err := c.Write(ctx, child.Path, group)
		if err != nil {
			return fmt.Errorf("write %s: %w", key, err)
		}
		if id, _ := response["id"].(string); id != "" {
			groupID = id
		}
	}
	if !exists {
		recordChild(recorded, child)
	}
	if groupID == "" {
		data, _, err := readIfExists(ctx, c, child.Path)
		if err != nil {
			return err
		}
		groupID, _ = data["id"].(string)
	}
	if groupID == "" {
		return fmt.Errorf("%s has no id after write", key)
	}

	alias, _ := want["alias"].(map[string]any)
	if len(engine.Diff(engine.Payload{"alias": alias}, got, engine.FieldSpec{Partial: []string{"alias"}})) == 0 {
		return nil
	}
	body := map[string]any{"name": alias["name"], "mount_accessor": accessor, "canonical_id": groupID}
	aliasPath := "identity/group-alias"
	if aliasID := obs.aliasIDs[key]; aliasID != "" {
		aliasPath += "/id/" + aliasID
	}
	if _, err := c.Write(ctx, aliasPath, body); err != nil {
		return fmt.Errorf("write alias of %s: %w", key, err)
	}
	return nil
}

// layoutObservation is the comparable read of a layout plus the server ids a write needs.
type layoutObservation struct {
	payload  engine.Payload
	found    bool
	groupIDs map[string]string
	aliasIDs map[string]string
}

func observeLayout(ctx context.Context, c openbao.Client, l *accessv1alpha1.AccessLayout) (layoutObservation, error) {
	obs := layoutObservation{
		payload:  engine.Payload{},
		found:    len(l.Status.Children) > 0,
		groupIDs: map[string]string{},
		aliasIDs: map[string]string{},
	}
	planned := layoutChildren(l)
	var existing []accessv1alpha1.AccessChild
	for _, child := range planned {
		data, ok, err := readIfExists(ctx, inNamespace(c, child.Namespace), child.Path)
		if err != nil {
			return obs, err
		}
		if !ok {
			continue
		}
		obs.found = true
		existing = append(existing, child)
		key := childKey(child)
		if child.Kind == accessv1alpha1.AccessChildPolicy {
			obs.payload[key] = data["policy"]
			continue
		}
		view := map[string]any{"type": data["type"], "policies": data["policies"], "metadata": data["metadata"]}
		obs.groupIDs[key], _ = data["id"].(string)
		if alias, ok := data["alias"].(map[string]any); ok {
			if id, _ := alias["id"].(string); id != "" {
				obs.aliasIDs[key] = id
				mountPath, _ := alias["mount_path"].(string)
				view["alias"] = map[string]any{
					"name":  alias["name"],
					"mount": strings.TrimSuffix(strings.TrimPrefix(mountPath, "auth/"), "/"),
				}
			}
		}
		obs.payload[key] = view
	}
	if err := checkChildren(l, l.Status.Children, existing); err != nil {
		return obs, err
	}
	obs.payload[staleKey] = childPaths(staleChildren(l.Status.Children, planned))
	return obs, nil
}

func layoutBase(l *accessv1alpha1.AccessLayout) string {
	return "layout_" + l.Namespace + "_" + l.Name
}

func layoutOwner(l *accessv1alpha1.AccessLayout) string {
	return "AccessLayout/" + l.Namespace + "/" + l.Name
}

func groupChild(namespace, policy, mount string) accessv1alpha1.AccessChild {
	return accessv1alpha1.AccessChild{Kind: accessv1alpha1.AccessChildGroup, Namespace: namespace,
		Path: "identity/group/name/" + policy + "_" + mount}
}

func layoutCells(l *accessv1alpha1.AccessLayout) []layoutCell {
	orgs := l.Spec.Organizations
	if len(orgs) == 0 {
		orgs = []string{""}
	}
	var cells []layoutCell
	for _, role := range l.Spec.Roles {
		for _, level := range role.Levels {
			for _, org := range orgs {
				policy := layoutBase(l) + "_" + role.Name
				if org != "" {
					policy += "_" + org
				}
				cells = append(cells, layoutCell{namespace: strings.Trim(level.Namespace, "/"), role: role.Name,
					org: org, level: level.Level, policy: policy})
			}
		}
	}
	return cells
}

// layoutChildren lists the objects the spec plans: per cell its policy, then one group per auth method.
func layoutChildren(l *accessv1alpha1.AccessLayout) []accessv1alpha1.AccessChild {
	cells := layoutCells(l)
	children := make([]accessv1alpha1.AccessChild, 0, len(cells)*(1+len(l.Spec.AuthMounts)))
	for _, cell := range cells {
		children = append(children, policyChild(cell.namespace, cell.policy))
		for _, mount := range l.Spec.AuthMounts {
			children = append(children, groupChild(cell.namespace, cell.policy, mount))
		}
	}
	return children
}

func layoutAlias(l *accessv1alpha1.AccessLayout, cell layoutCell) string {
	template := l.Spec.GroupAliasTemplate
	if template == "" {
		template = "{role}"
		if len(l.Spec.Organizations) > 0 {
			template = "{org}_{role}"
		}
	}
	return strings.ReplaceAll(strings.ReplaceAll(template, "{role}", cell.role), "{org}", cell.org)
}

func layoutLevels(l *accessv1alpha1.AccessLayout) []string {
	var levels []string
	for _, role := range l.Spec.Roles {
		for _, level := range role.Levels {
			levels = append(levels, level.Level)
		}
	}
	return levels
}
