package importer

import (
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// kubernetesRoleRead is the read of auth/kubernetes/role/app from OpenBao 2.7.
var kubernetesRoleRead = engine.Payload{
	"alias_name_source":                        "serviceaccount_uid",
	"audience":                                 "",
	"bound_service_account_names":              []any{litApp},
	"bound_service_account_namespace_selector": `{"matchLabels":{"team":"a"}}`,
	"bound_service_account_namespaces":         []any{"apps"},
	"token_bound_cidrs":                        []any{},
	"token_explicit_max_ttl":                   float64(0),
	"token_max_ttl":                            float64(0),
	"token_no_default_policy":                  false,
	"token_num_uses":                           float64(0),
	"token_period":                             float64(0),
	litTokenPolicies:                           []any{litApp},
	"token_strictly_bind_ip":                   false,
	"token_ttl":                                float64(3600),
	"token_type":                               "default",
}

func TestFillMapsReadShapes(t *testing.T) {
	var spec authv1alpha1.KubernetesAuthRoleSpec
	spec.Name, spec.MountPath = litApp, litKubernetes
	read := engine.Payload{litName: "other"}
	maps.Copy(read, kubernetesRoleRead)
	set, unmapped := fill(reflect.ValueOf(&spec).Elem(), read, mountIdentity)

	if len(unmapped) != 0 {
		t.Fatalf("unmapped = %v", unmapped)
	}
	if spec.Name != litApp || spec.MountPath != litKubernetes {
		t.Errorf("identity fields were overwritten: %q %q", spec.Name, spec.MountPath)
	}
	if spec.Audience != "" {
		t.Errorf("empty audience was set: %q", spec.Audience)
	}
	if !slices.Equal(spec.BoundServiceAccountNames, []string{litApp}) || !slices.Equal(spec.TokenPolicies, []string{litApp}) {
		t.Errorf("lists = %v %v", spec.BoundServiceAccountNames, spec.TokenPolicies)
	}
	if spec.TokenTTL == nil || spec.TokenTTL.Duration != time.Hour {
		t.Errorf("tokenTTL = %v, want 1h", spec.TokenTTL)
	}
	if spec.BoundServiceAccountNamespaceSelector == nil || spec.BoundServiceAccountNamespaceSelector.MatchLabels["team"] != "a" {
		t.Errorf("selector = %v", spec.BoundServiceAccountNamespaceSelector)
	}
	if spec.TokenNoDefaultPolicy == nil || *spec.TokenNoDefaultPolicy {
		t.Errorf("tokenNoDefaultPolicy = %v, want false", spec.TokenNoDefaultPolicy)
	}
	paths := make([]string, 0, len(set))
	for _, f := range set {
		paths = append(paths, f.path)
	}
	if slices.Contains(paths, "tokenBoundCIDRs") || !slices.Contains(paths, "tokenTTL") {
		t.Errorf("set paths = %v", paths)
	}
}

func TestFillNestedPrefixAndMaps(t *testing.T) {
	var spec sysv1alpha1.AuthMountSpec
	read := engine.Payload{
		litDescription:           "users",
		"default_lease_ttl":      float64(60),
		"options":                map[string]any{"version": "2"},
		"user_lockout_threshold": float64(5),
		"user_lockout_duration":  "15m",
	}
	_, unmapped := fill(reflect.ValueOf(&spec).Elem(), read, []string{"path", litType})
	if len(unmapped) != 0 {
		t.Fatalf("unmapped = %v", unmapped)
	}
	if spec.Description == nil || *spec.Description != "users" || spec.DefaultLeaseTTL.Duration != time.Minute {
		t.Errorf("mount fields = %v %v", spec.Description, spec.DefaultLeaseTTL)
	}
	if spec.Options["version"] != "2" {
		t.Errorf("options = %v", spec.Options)
	}
	if spec.UserLockout == nil || *spec.UserLockout.Threshold != 5 || spec.UserLockout.Duration.Duration != 15*time.Minute {
		t.Errorf("userLockout = %+v", spec.UserLockout)
	}
}

func TestFillReportsUnconvertibleValues(t *testing.T) {
	var spec authv1alpha1.KubernetesAuthRoleSpec
	_, unmapped := fill(reflect.ValueOf(&spec).Elem(), engine.Payload{litTokenPolicies: map[string]any{"a": 1.0}}, nil)
	if !slices.Equal(unmapped, []string{"tokenPolicies"}) {
		t.Errorf("unmapped = %v", unmapped)
	}
}

func TestConvert(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		typ  reflect.Type
		want any
	}{
		{"seconds", float64(90), reflect.TypeFor[*metav1.Duration](), &metav1.Duration{Duration: 90 * time.Second}},
		{"duration string", "72h", reflect.TypeFor[*metav1.Duration](), &metav1.Duration{Duration: 72 * time.Hour}},
		{"numeric string", "30", reflect.TypeFor[metav1.Duration](), metav1.Duration{Duration: 30 * time.Second}},
		{"comma list", "a, b", reflect.TypeFor[[]string](), []string{"a", "b"}},
		{"number as string", float64(100), reflect.TypeFor[string](), "100"},
		{"int pointer", float64(3), reflect.TypeFor[*int32](), new(int32(3))},
		{"json map", `{"a":"b"}`, reflect.TypeFor[map[string]string](), map[string]string{"a": "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := convert(tt.raw, tt.typ)
			if !ok {
				t.Fatal("not converted")
			}
			if !reflect.DeepEqual(got.Interface(), tt.want) {
				t.Errorf("got %#v, want %#v", got.Interface(), tt.want)
			}
		})
	}
	for _, raw := range []any{nil, "", []any{}, map[string]any{}} {
		if _, ok := convert(raw, reflect.TypeFor[string]()); ok {
			t.Errorf("empty value %#v was converted", raw)
		}
	}
	if _, ok := convert("not json", reflect.TypeFor[map[string]string]()); ok {
		t.Error("invalid JSON string was converted to a map")
	}
	if _, ok := convert(1.5, reflect.TypeFor[int32]()); ok {
		t.Error("fraction was converted to an integer")
	}
}

func TestIsZeroAndDropEmptyStructs(t *testing.T) {
	for _, v := range []any{new(false), new(int32(0)), &metav1.Duration{}, []string{}, ""} {
		if !isZero(reflect.ValueOf(v)) {
			t.Errorf("isZero(%#v) = false", v)
		}
	}
	for _, v := range []any{new(true), &metav1.Duration{Duration: time.Second}, []string{"a"}, "x"} {
		if isZero(reflect.ValueOf(v)) {
			t.Errorf("isZero(%#v) = true", v)
		}
	}
	spec := sysv1alpha1.AuthMountSpec{UserLockout: &sysv1alpha1.UserLockoutConfig{}}
	dropEmptyStructs(reflect.ValueOf(&spec).Elem())
	if spec.UserLockout != nil {
		t.Error("empty userLockout was kept")
	}
	spec.UserLockout = &sysv1alpha1.UserLockoutConfig{Disable: new(true)}
	dropEmptyStructs(reflect.ValueOf(&spec).Elem())
	if spec.UserLockout == nil {
		t.Error("set userLockout was dropped")
	}
}
