// Package auth holds the adapters for the auth.obco.dev kinds.
package auth

import (
	"context"
	"fmt"
	"net/netip"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// token_* keys of auth method roles.
const (
	keyTokenBoundCIDRs      = "token_bound_cidrs"
	keyTokenExplicitMaxTTL  = "token_explicit_max_ttl"
	keyTokenMaxTTL          = "token_max_ttl"
	keyTokenNoDefaultPolicy = "token_no_default_policy"
	keyTokenNumUses         = "token_num_uses"
	keyTokenPeriod          = "token_period"
	keyTokenPolicies        = "token_policies"
	keyTokenStrictlyBindIP  = "token_strictly_bind_ip"
	keyTokenTTL             = "token_ttl"
	keyTokenType            = "token_type"
)

// TokenBaseFieldSpec is the comparison contribution of TokenBaseFields; merge it into an adapter's FieldSpec.
var TokenBaseFieldSpec = engine.FieldSpec{
	Sets:      []string{keyTokenBoundCIDRs},
	Durations: []string{keyTokenExplicitMaxTTL, keyTokenPeriod},
}

// TokenFieldSpec is the comparison contribution of TokenFields; merge it into an adapter's FieldSpec.
var TokenFieldSpec = engine.FieldSpec{
	Sets:      []string{keyTokenBoundCIDRs, keyTokenPolicies},
	Durations: []string{keyTokenExplicitMaxTTL, keyTokenPeriod, keyTokenTTL, keyTokenMaxTTL},
}

// PutTokenBaseFields adds the set token_* keys of t to p; unset fields are left out so the server keeps its value.
func PutTokenBaseFields(p engine.Payload, t authv1alpha1.TokenBaseFields) {
	putSeconds(p, keyTokenExplicitMaxTTL, t.TokenExplicitMaxTTL)
	putSeconds(p, keyTokenPeriod, t.TokenPeriod)
	if t.TokenBoundCIDRs != nil {
		p[keyTokenBoundCIDRs] = hostCIDRsAsIPs(t.TokenBoundCIDRs)
	}
	if t.TokenNumUses != nil {
		p[keyTokenNumUses] = int64(*t.TokenNumUses)
	}
	if t.TokenType != "" {
		p[keyTokenType] = string(t.TokenType)
	}
	if t.TokenNoDefaultPolicy != nil {
		p[keyTokenNoDefaultPolicy] = *t.TokenNoDefaultPolicy
	}
}

// PutTokenFields adds the set token_* keys of t, including the base fields, to p.
func PutTokenFields(p engine.Payload, t authv1alpha1.TokenFields) {
	PutTokenBaseFields(p, t.TokenBaseFields)
	putSeconds(p, keyTokenTTL, t.TokenTTL)
	putSeconds(p, keyTokenMaxTTL, t.TokenMaxTTL)
	if t.TokenPolicies != nil {
		p[keyTokenPolicies] = t.TokenPolicies
	}
	if t.TokenStrictlyBindIP != nil {
		p[keyTokenStrictlyBindIP] = *t.TokenStrictlyBindIP
	}
}

// putTokenFieldDefaults adds the value a built-in auth method stores for each unset token_* field of an auth
// role, so a field removed from the spec is reset. External plugins keep PutTokenFields alone: an older plugin SDK
// does not return every token_* key.
func putTokenFieldDefaults(p engine.Payload) {
	putDefaults(p, engine.Payload{
		keyTokenExplicitMaxTTL:  int64(0),
		keyTokenPeriod:          int64(0),
		keyTokenBoundCIDRs:      []string{},
		keyTokenNumUses:         int64(0),
		keyTokenType:            string(authv1alpha1.TokenTypeDefault),
		keyTokenNoDefaultPolicy: false,
		keyTokenTTL:             int64(0),
		keyTokenMaxTTL:          int64(0),
		keyTokenPolicies:        []string{},
		keyTokenStrictlyBindIP:  false,
	})
}

// putDefaults adds each key of defaults that p does not set.
func putDefaults(p, defaults engine.Payload) {
	for key, value := range defaults {
		if _, ok := p[key]; !ok {
			p[key] = value
		}
	}
}

// +kubebuilder:rbac:groups=sys.obco.dev,resources=aclpolicies,verbs=get;list;watch

// putTokenFields adds the set token_* keys of t to p, with the OpenBao names of the ACLPolicy resources that t
// references merged into token_policies.
func putTokenFields(ctx context.Context, r engine.Resolver, obj engine.Object, p engine.Payload, t authv1alpha1.TokenFields) error {
	PutTokenFields(p, t)
	if len(t.PolicyRefs) == 0 {
		return nil
	}
	names := slices.Clone(t.TokenPolicies)
	spec := obj.GetResourceSpec()
	for _, ref := range t.PolicyRefs {
		var policy sysv1alpha1.ACLPolicy
		if err := r.Object(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: ref.Name}, &policy); err != nil {
			return fmt.Errorf("policy %s: %w", ref.Name, err)
		}
		if policy.Spec.ConnectionRef != spec.ConnectionRef || policy.Spec.Namespace != spec.Namespace {
			return fmt.Errorf("policy %s uses another connectionRef or OpenBao namespace than the role", ref.Name)
		}
		if !meta.IsStatusConditionTrue(policy.Status.Conditions, commonv1alpha1.ConditionReady) {
			return fmt.Errorf("policy %s is not ready: %w", ref.Name, engine.ErrReferenceNotReady)
		}
		names = append(names, authName(policy.Spec.Name, policy.Name))
	}
	slices.Sort(names)
	p[keyTokenPolicies] = slices.Compact(names)
	return nil
}

// MergeFieldSpecs concatenates the lists and maps of the given specs.
func MergeFieldSpecs(specs ...engine.FieldSpec) engine.FieldSpec {
	var out engine.FieldSpec
	for _, s := range specs {
		out.WriteOnly = append(out.WriteOnly, s.WriteOnly...)
		out.CreateOnly = append(out.CreateOnly, s.CreateOnly...)
		out.Sets = append(out.Sets, s.Sets...)
		out.Durations = append(out.Durations, s.Durations...)
		for k, v := range s.Aliases {
			if out.Aliases == nil {
				out.Aliases = map[string]string{}
			}
			out.Aliases[k] = v
		}
	}
	return out
}

// hostCIDRsAsIPs writes single-host blocks as bare addresses, because OpenBao reads 10.0.0.1/32 back as 10.0.0.1.
func hostCIDRsAsIPs(cidrs []string) []string {
	out := make([]string, len(cidrs))
	for i, c := range cidrs {
		out[i] = c
		if prefix, err := netip.ParsePrefix(c); err == nil && prefix.IsSingleIP() {
			out[i] = prefix.Addr().String()
		}
	}
	return out
}

func putSeconds(p engine.Payload, key string, d *metav1.Duration) {
	if d != nil {
		p[key] = int64(d.Seconds())
	}
}
