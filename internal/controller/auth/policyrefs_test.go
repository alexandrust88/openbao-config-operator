package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// policyRefsResolver serves ACLPolicy resources by name.
type policyRefsResolver map[string]*sysv1alpha1.ACLPolicy

func (policyRefsResolver) SecretValue(context.Context, string, commonv1alpha1.SecretKeySelector) ([]byte, error) {
	return nil, engine.ErrReferenceNotReady
}

func (r policyRefsResolver) Object(_ context.Context, key client.ObjectKey, obj client.Object) error {
	p, ok := r[key.Name]
	if !ok {
		return fmt.Errorf("%w: policy %s not found", engine.ErrReferenceNotReady, key.Name)
	}
	p.DeepCopyInto(obj.(*sysv1alpha1.ACLPolicy))
	return nil
}

func policyRefsPolicy(name, openbaoName string, ready bool) *sysv1alpha1.ACLPolicy {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	p := &sysv1alpha1.ACLPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: pluginTestNamespace}}
	p.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: cloudLitOpenbao}
	p.Spec.Name = openbaoName
	p.Status.Conditions = []metav1.Condition{{Type: commonv1alpha1.ConditionReady, Status: status}}
	return p
}

func policyRefsRole(refs ...string) *authv1alpha1.KubernetesAuthRole {
	k := &authv1alpha1.KubernetesAuthRole{ObjectMeta: metav1.ObjectMeta{Name: authTestApp, Namespace: pluginTestNamespace}}
	k.Spec.ConnectionRef = commonv1alpha1.ConnectionReference{Name: cloudLitOpenbao}
	k.Spec.BoundServiceAccountNames = []string{authTestApp}
	k.Spec.BoundServiceAccountNamespaces = []string{pluginTestNamespace}
	k.Spec.TokenPolicies = []string{authLitReader, authTestAudit}
	for _, name := range refs {
		k.Spec.PolicyRefs = append(k.Spec.PolicyRefs, commonv1alpha1.LocalObjectReference{Name: name})
	}
	return k
}

func TestPolicyRefsMergeIntoTokenPolicies(t *testing.T) {
	r := policyRefsResolver{
		"app-read":  policyRefsPolicy("app-read", "", true),
		"app-write": policyRefsPolicy("app-write", "team-writer", true),
		"duplicate": policyRefsPolicy("duplicate", authLitReader, true),
	}
	got, err := KubernetesAuthRoleAdapter{}.Desired(context.Background(), r, policyRefsRole("app-read", "app-write", "duplicate"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app-read", authTestAudit, authLitReader, "team-writer"}; !reflect.DeepEqual(got[keyTokenPolicies], want) {
		t.Errorf("token_policies = %v, want %v", got[keyTokenPolicies], want)
	}

	got, err = KubernetesAuthRoleAdapter{}.Desired(context.Background(), r, policyRefsRole())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{authLitReader, authTestAudit}; !reflect.DeepEqual(got[keyTokenPolicies], want) {
		t.Errorf("without refs token_policies = %v, want the literals unchanged", got[keyTokenPolicies])
	}
}

func TestPolicyRefsWaitForReadyPolicies(t *testing.T) {
	r := policyRefsResolver{"pending": policyRefsPolicy("pending", "", false)}
	for _, ref := range []string{"pending", "missing"} {
		_, err := KubernetesAuthRoleAdapter{}.Desired(context.Background(), r, policyRefsRole(ref))
		if !errors.Is(err, engine.ErrReferenceNotReady) {
			t.Errorf("%s: err = %v, want ErrReferenceNotReady", ref, err)
		}
	}
}

func TestPolicyRefsRejectOtherConnectionOrNamespace(t *testing.T) {
	other := policyRefsPolicy("other", "", true)
	other.Spec.ConnectionRef.Name = "elsewhere"
	child := policyRefsPolicy("child", "", true)
	child.Spec.Namespace = "team-a"
	r := policyRefsResolver{"other": other, "child": child}
	for _, ref := range []string{"other", "child"} {
		_, err := KubernetesAuthRoleAdapter{}.Desired(context.Background(), r, policyRefsRole(ref))
		if err == nil || errors.Is(err, engine.ErrReferenceNotReady) {
			t.Errorf("%s: err = %v, want a configuration error", ref, err)
		}
	}
}

// TestPolicyRefsEveryTokenFieldsKind guards that each kind with token policies resolves policyRefs.
func TestPolicyRefsEveryTokenFieldsKind(t *testing.T) {
	r := policyRefsResolver{}
	refs := authv1alpha1.TokenFields{PolicyRefs: []commonv1alpha1.LocalObjectReference{{Name: "missing"}}}
	meta := metav1.ObjectMeta{Name: "x", Namespace: pluginTestNamespace}
	ctx := context.Background()
	desired := map[string]func() error{
		"AppRole": func() error {
			_, err := AppRoleAdapter{}.Desired(ctx, r, &authv1alpha1.AppRole{ObjectMeta: meta, Spec: authv1alpha1.AppRoleSpec{TokenFields: refs}})
			return err
		},
		"CertAuthRole": func() error {
			_, err := CertAuthRoleAdapter{}.Desired(ctx, r, &authv1alpha1.CertAuthRole{ObjectMeta: meta, Spec: authv1alpha1.CertAuthRoleSpec{TokenFields: refs}})
			return err
		},
		"JWTAuthRole": func() error {
			_, err := JWTAuthRoleAdapter{}.Desired(ctx, r, &authv1alpha1.JWTAuthRole{ObjectMeta: meta, Spec: authv1alpha1.JWTAuthRoleSpec{TokenFields: refs}})
			return err
		},
		"UserpassUser": func() error {
			_, err := UserpassUserAdapter{}.Desired(ctx, r, &authv1alpha1.UserpassUser{ObjectMeta: meta, Spec: authv1alpha1.UserpassUserSpec{TokenFields: refs}})
			return err
		},
		"AWSAuthRole": func() error {
			_, err := AWSAuthRoleAdapter{}.Desired(ctx, r, &authv1alpha1.AWSAuthRole{ObjectMeta: meta, Spec: authv1alpha1.AWSAuthRoleSpec{TokenFields: refs}})
			return err
		},
		"AzureAuthRole": func() error {
			_, err := AzureAuthRoleAdapter{}.Desired(ctx, r, &authv1alpha1.AzureAuthRole{ObjectMeta: meta, Spec: authv1alpha1.AzureAuthRoleSpec{TokenFields: refs}})
			return err
		},
		"CFAuthRole": func() error {
			_, err := CFAuthRoleAdapter{}.Desired(ctx, r, &authv1alpha1.CFAuthRole{ObjectMeta: meta, Spec: authv1alpha1.CFAuthRoleSpec{TokenFields: refs}})
			return err
		},
		"GCPAuthRole": func() error {
			_, err := GCPAuthRoleAdapter{}.Desired(ctx, r, &authv1alpha1.GCPAuthRole{ObjectMeta: meta, Spec: authv1alpha1.GCPAuthRoleSpec{TokenFields: refs}})
			return err
		},
		"OCIAuthRole": func() error {
			_, err := OCIAuthRoleAdapter{}.Desired(ctx, r, &authv1alpha1.OCIAuthRole{ObjectMeta: meta, Spec: authv1alpha1.OCIAuthRoleSpec{TokenFields: refs}})
			return err
		},
	}
	for kind, run := range desired {
		if err := run(); !errors.Is(err, engine.ErrReferenceNotReady) {
			t.Errorf("%s: err = %v, want ErrReferenceNotReady for a missing ACLPolicy", kind, err)
		}
	}
}
