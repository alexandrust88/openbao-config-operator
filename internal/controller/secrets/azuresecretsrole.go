package secrets

import (
	"context"
	"encoding/json"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=azuresecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=azuresecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=azuresecretsroles/finalizers,verbs=update

// SetupAzureSecretsRole registers the AzureSecretsRole controller with the manager.
func SetupAzureSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "azuresecretsrole", AzureSecretsRoleAdapter{},
		func() *secretsv1alpha1.AzureSecretsRole { return &secretsv1alpha1.AzureSecretsRole{} })
}

// AzureSecretsRoleAdapter maps an AzureSecretsRole onto <mountPath>/roles/<name>.
type AzureSecretsRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.AzureSecretsRole]  = AzureSecretsRoleAdapter{}
	_ engine.Observer[*secretsv1alpha1.AzureSecretsRole] = AzureSecretsRoleAdapter{}
	_ engine.Writer[*secretsv1alpha1.AzureSecretsRole]   = AzureSecretsRoleAdapter{}
)

// Path implements engine.Adapter.
func (AzureSecretsRoleAdapter) Path(r *secretsv1alpha1.AzureSecretsRole) string {
	return azureSecretsMount(r.Spec.MountPath) + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter. The plugin takes azure_roles and azure_groups as JSON strings.
func (AzureSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.AzureSecretsRole) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{}
	if s.AzureRoles != nil {
		encoded, err := json.Marshal(azureRoleEntries(s.AzureRoles))
		if err != nil {
			return nil, err
		}
		p["azure_roles"] = string(encoded)
	}
	if s.AzureGroups != nil {
		encoded, err := json.Marshal(azureGroupEntries(s.AzureGroups))
		if err != nil {
			return nil, err
		}
		p["azure_groups"] = string(encoded)
	}
	optString(p, "application_object_id", s.ApplicationObjectID)
	optBool(p, "persist_app", s.PersistApp)
	optSeconds(p, "ttl", s.TTL)
	optSeconds(p, "max_ttl", s.MaxTTL)
	optSeconds(p, "explicit_max_ttl", s.ExplicitMaxTTL)
	optBool(p, "permanently_delete", s.PermanentlyDelete)
	optString(p, "sign_in_audience", string(s.SignInAudience))
	putPluginList(p, "tags", s.Tags)
	return p, nil
}

// Fields implements engine.Adapter.
func (AzureSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"tags"},
		Durations: []string{"ttl", "max_ttl", "explicit_max_ttl"},
	}
}

// Observe implements engine.Observer. The read returns azure_roles and azure_groups as lists in which OpenBao has
// filled in the IDs or names it looked up; a list that matches the spec is reported as the desired JSON string.
func (a AzureSecretsRoleAdapter) Observe(ctx context.Context, c openbao.Client, r *secretsv1alpha1.AzureSecretsRole) (engine.Payload, bool, error) {
	observed, found, err := readCloud(ctx, c, a.Path(r))
	if !found || err != nil {
		return nil, false, err
	}
	desired, err := a.Desired(ctx, nil, r)
	if err != nil {
		return nil, false, err
	}
	if r.Spec.AzureRoles != nil && azureEntriesMatch(azureRoleEntries(r.Spec.AzureRoles), observed["azure_roles"]) {
		observed["azure_roles"] = desired["azure_roles"]
	}
	if r.Spec.AzureGroups != nil && azureEntriesMatch(azureGroupEntries(r.Spec.AzureGroups), observed["azure_groups"]) {
		observed["azure_groups"] = desired["azure_groups"]
	}
	return observed, true, nil
}

func azureRoleEntries(roles []secretsv1alpha1.AzureRoleAssignment) []map[string]string {
	entries := make([]map[string]string, 0, len(roles))
	for _, role := range roles {
		entry := map[string]string{"scope": role.Scope}
		if role.RoleName != "" {
			entry["role_name"] = role.RoleName
		}
		if role.RoleID != "" {
			entry["role_id"] = role.RoleID
		}
		entries = append(entries, entry)
	}
	return entries
}

func azureGroupEntries(groups []secretsv1alpha1.AzureGroupAssignment) []map[string]string {
	entries := make([]map[string]string, 0, len(groups))
	for _, group := range groups {
		entry := map[string]string{"group_name": group.GroupName}
		if group.ObjectID != "" {
			entry["object_id"] = group.ObjectID
		}
		entries = append(entries, entry)
	}
	return entries
}

// azureEntriesMatch pairs every wanted entry with a distinct observed entry that has the same value, ignoring case,
// for each key the wanted entry sets. Keys only OpenBao filled in are not compared; an absent list is empty.
func azureEntriesMatch(want []map[string]string, observed any) bool {
	got, ok := observed.([]any)
	if (!ok && observed != nil) || len(got) != len(want) {
		return false
	}
	used := make([]bool, len(got))
	for _, w := range want {
		matched := false
		for i, g := range got {
			entry, ok := g.(map[string]any)
			if used[i] || !ok || !azureEntryMatches(w, entry) {
				continue
			}
			used[i], matched = true, true
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func azureEntryMatches(want map[string]string, got map[string]any) bool {
	for key, value := range want {
		if s, ok := got[key].(string); !ok || !strings.EqualFold(s, value) {
			return false
		}
	}
	return true
}

// Write implements engine.Writer.
func (a AzureSecretsRoleAdapter) Write(ctx context.Context, c openbao.Client, r *secretsv1alpha1.AzureSecretsRole,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, a.Path(r), desired)
}
