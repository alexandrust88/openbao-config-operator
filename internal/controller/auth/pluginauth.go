package auth

import (
	"context"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const pluginPoliciesKey = "policies"

func setupPluginKind[T engine.Object](mgr ctrl.Manager, conns engine.Connections, opts engine.Options,
	controllerName string, adapter engine.Adapter[T], newObj func() T) error {
	r := engine.New[T](mgr.GetClient(), mgr.GetScheme(), mgr.GetEventRecorder(controllerName), conns, adapter, newObj, opts)
	return r.SetupWithManager(mgr)
}

func pluginPut[V any](p engine.Payload, key string, v *V) {
	if v != nil {
		p[key] = *v
	}
}

// ldapConnectionFieldSpec covers LDAPConnectionFields: the read omits bindpass and both client TLS values.
var ldapConnectionFieldSpec = engine.FieldSpec{
	WriteOnly: []string{"bindpass", "client_tls_cert", "client_tls_key"},
	Durations: []string{"request_timeout", "connection_timeout"},
}

// putLDAPConnection adds the LDAP connection keys to p. url and userattr are sent lower case, as OpenBao stores them.
func putLDAPConnection(ctx context.Context, r engine.Resolver, namespace string, p engine.Payload, f authv1alpha1.LDAPConnectionFields) error {
	p["url"] = strings.ToLower(f.URL)
	putString(p, "binddn", f.BindDN)
	putString(p, "userdn", f.UserDN)
	putString(p, "userattr", strings.ToLower(f.UserAttr))
	putString(p, "userfilter", f.UserFilter)
	putString(p, "upndomain", f.UPNDomain)
	pluginPut(p, "discoverdn", f.DiscoverDN)
	pluginPut(p, "deny_null_bind", f.DenyNullBind)
	pluginPut(p, "username_as_alias", f.UsernameAsAlias)
	putString(p, "groupdn", f.GroupDN)
	putString(p, "groupfilter", f.GroupFilter)
	putString(p, "groupattr", f.GroupAttr)
	pluginPut(p, "use_token_groups", f.UseTokenGroups)
	pluginPut(p, "anonymous_group_search", f.AnonymousGroupSearch)
	pluginPut(p, "case_sensitive_names", f.CaseSensitiveNames)
	putString(p, "certificate", f.Certificate)
	putString(p, "client_tls_cert", f.ClientTLSCert)
	pluginPut(p, "starttls", f.StartTLS)
	pluginPut(p, "insecure_tls", f.InsecureTLS)
	putString(p, "tls_min_version", f.TLSMinVersion)
	putString(p, "tls_max_version", f.TLSMaxVersion)
	putSeconds(p, "request_timeout", f.RequestTimeout)
	putSeconds(p, "connection_timeout", f.ConnectionTimeout)
	putString(p, "dereference_aliases", f.DereferenceAliases)
	pluginPut(p, "max_page_size", f.MaxPageSize)
	if f.BindPassSecretRef != nil {
		v, err := authSecret(ctx, r, namespace, *f.BindPassSecretRef)
		if err != nil {
			return err
		}
		p["bindpass"] = v
	}
	if f.ClientTLSKeySecretRef != nil {
		v, err := authSecret(ctx, r, namespace, *f.ClientTLSKeySecretRef)
		if err != nil {
			return err
		}
		p["client_tls_key"] = v
	}
	return nil
}

func ldapConnectionSecretRefs(f authv1alpha1.LDAPConnectionFields) []string {
	var refs []string
	if f.BindPassSecretRef != nil {
		refs = append(refs, f.BindPassSecretRef.Name)
	}
	if f.ClientTLSKeySecretRef != nil {
		refs = append(refs, f.ClientTLSKeySecretRef.Name)
	}
	return refs
}

// pluginSplitComma splits a comma-separated value the way OpenBao's comma string slices do: trimmed, no empties.
func pluginSplitComma(s string) []string {
	var out []string
	for item := range strings.SplitSeq(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// pluginCommaKeyToList turns key of observed from a comma-separated string into a list, so that it compares as a set.
func pluginCommaKeyToList(observed engine.Payload, key string) {
	if s, ok := observed[key].(string); ok {
		observed[key] = pluginSplitComma(s)
	}
}

// normaliseGitHubMap reports the "value" of a GitHub team or user mapping, the comma-joined policy list as stored,
// as the desired string when it holds the same policies in another order or spacing.
func normaliseGitHubMap(observed engine.Payload, policies []string) {
	if stored, ok := observed["value"].(string); ok && pluginSameSet(pluginSplitComma(stored), policies) {
		observed["value"] = strings.Join(policies, ",")
	}
}

func pluginLower(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.ToLower(v)
	}
	return out
}

func pluginSameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}
