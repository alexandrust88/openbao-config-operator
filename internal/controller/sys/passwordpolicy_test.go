package sys

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func TestPasswordPolicy(t *testing.T) {
	text := "length = 20\nrule \"charset\" {\n  charset = \"abc\"\n}\n"
	p := &sysv1alpha1.PasswordPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "db-passwords", Namespace: testNamespace},
		Spec:       sysv1alpha1.PasswordPolicySpec{Policy: text},
	}
	a := PasswordPolicyAdapter{}
	if got := a.Path(p); got != "sys/policies/password/db-passwords" {
		t.Errorf("default path = %q", got)
	}
	p.Spec.Name = "pp"
	if got := a.Path(p); got != "sys/policies/password/pp" {
		t.Errorf("spec path = %q", got)
	}
	desired, _ := a.Desired(context.Background(), nil, p)
	if !reflect.DeepEqual(desired, engine.Payload{keyPolicy: text}) {
		t.Errorf("desired = %v", desired)
	}

	// GET sys/policies/password/pp from bao server -dev 2.7.2 returns the stored text verbatim.
	golden := decodeRead(t, `{"policy":"length = 20\nrule \"charset\" {\n  charset = \"abc\"\n}\n"}`)
	if got := engine.Diff(desired, golden, a.Fields()); len(got) != 0 {
		t.Errorf("differing = %v, want none", got)
	}
	golden[keyPolicy] = "length = 21\n"
	if got := engine.Diff(desired, golden, a.Fields()); len(got) != 1 {
		t.Errorf("changed policy not reported: %v", got)
	}
}
