package secrets

import (
	"context"
	"slices"
	"strconv"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=gcprolesets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=gcprolesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=gcprolesets/finalizers,verbs=update

// SetupGCPRoleset registers the GCPRoleset controller with the manager.
func SetupGCPRoleset(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "gcproleset", GCPRolesetAdapter{},
		func() *secretsv1alpha1.GCPRoleset { return &secretsv1alpha1.GCPRoleset{} })
}

// GCPRolesetAdapter maps a GCPRoleset onto <mountPath>/roleset/<name>.
type GCPRolesetAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.GCPRoleset]      = GCPRolesetAdapter{}
	_ engine.Observer[*secretsv1alpha1.GCPRoleset]     = GCPRolesetAdapter{}
	_ engine.StatusWriter[*secretsv1alpha1.GCPRoleset] = GCPRolesetAdapter{}
	_ engine.Writer[*secretsv1alpha1.GCPRoleset]       = GCPRolesetAdapter{}
)

// Path implements engine.Adapter.
func (GCPRolesetAdapter) Path(r *secretsv1alpha1.GCPRoleset) string {
	return gcpSecretsMount(r.Spec.MountPath) + "/roleset/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter.
func (GCPRolesetAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.GCPRoleset) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{"project": s.Project, "bindings": gcpBindingsHCL(s.Bindings)}
	optString(p, "secret_type", string(s.SecretType))
	putPluginList(p, "token_scopes", s.TokenScopes)
	return p, nil
}

// Fields implements engine.Adapter.
func (GCPRolesetAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Sets: []string{"token_scopes"}}
}

// Observe implements engine.Observer.
func (a GCPRolesetAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.GCPRoleset) (engine.Payload, bool, error) {
	return observeGCPBindings(ctx, c, a.Path(r), r.Spec.Bindings)
}

// WriteStatus implements engine.StatusWriter: status.id is the email of the service account OpenBao created.
func (GCPRolesetAdapter) WriteStatus(_ context.Context, _ openbao.Client, r *secretsv1alpha1.GCPRoleset, observed engine.Payload) error {
	if email, ok := observed["service_account_email"].(string); ok {
		r.Status.ID = email
	}
	return nil
}

// observeGCPBindings reads path; the read returns bindings as a map of resource to roles, which is reported as the
// desired HCL text when it grants the same roles.
func observeGCPBindings(ctx context.Context, c openbao.Client, path string, bindings []secretsv1alpha1.GCPBinding) (engine.Payload, bool, error) {
	observed, found, err := readCloud(ctx, c, path)
	if !found || err != nil {
		return nil, false, err
	}
	if bindings != nil && gcpBindingsMatch(bindings, observed["bindings"]) {
		observed["bindings"] = gcpBindingsHCL(bindings)
	}
	return observed, true, nil
}

// gcpBindingsHCL renders bindings in the HCL form the plugin parses: one resource block per binding.
func gcpBindingsHCL(bindings []secretsv1alpha1.GCPBinding) string {
	blocks := make([]string, 0, len(bindings))
	for _, b := range bindings {
		roles := make([]string, 0, len(b.Roles))
		for _, role := range b.Roles {
			roles = append(roles, strconv.Quote(role))
		}
		blocks = append(blocks, "resource "+strconv.Quote(b.Resource)+" {\n  roles = ["+strings.Join(roles, ", ")+"]\n}\n")
	}
	return strings.Join(blocks, "\n")
}

func gcpBindingsMatch(bindings []secretsv1alpha1.GCPBinding, observed any) bool {
	got, ok := observed.(map[string]any)
	if !ok || len(got) != len(bindings) {
		return false
	}
	for _, b := range bindings {
		list, ok := got[b.Resource].([]any)
		if !ok {
			return false
		}
		roles := make([]string, 0, len(list))
		for _, role := range list {
			s, ok := role.(string)
			if !ok {
				return false
			}
			roles = append(roles, s)
		}
		want := slices.Clone(b.Roles)
		slices.Sort(want)
		slices.Sort(roles)
		if !slices.Equal(slices.Compact(want), slices.Compact(roles)) {
			return false
		}
	}
	return true
}

// Write implements engine.Writer.
func (a GCPRolesetAdapter) Write(ctx context.Context, c openbao.Client, r *secretsv1alpha1.GCPRoleset,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, a.Path(r), desired)
}
