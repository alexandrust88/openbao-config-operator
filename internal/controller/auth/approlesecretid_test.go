package auth

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	secretIDRole     = "auth/approle/role/demo"
	secretIDAccessor = "e9a9aec6-05a8-1c57-7c01-b2084a848da8"
)

// secretIDLookup is a `secret-id-accessor/lookup` response from OpenBao 2.7.2 for a secret ID with a one hour TTL.
const secretIDLookup = `{"cidr_list":["10.0.0.0/8"],"creation_time":"2026-11-14T00:24:03.823968+02:00",
"expiration_time":"2026-11-14T01:24:03.823968+02:00","last_updated_time":"2026-11-14T00:24:03.823968+02:00",
"metadata":{"team":"x"},"secret_id_accessor":"e9a9aec6-05a8-1c57-7c01-b2084a848da8","secret_id_num_uses":3,
"secret_id_ttl":3600,"token_bound_cidrs":[]}`

var secretIDCreated = time.Date(2026, 11, 14, 0, 24, 3, 0, time.FixedZone("", 2*3600))

func newSecretID() *authv1alpha1.AppRoleSecretID {
	return &authv1alpha1.AppRoleSecretID{
		ObjectMeta: metav1.ObjectMeta{Name: authTestDemoLogin, Namespace: pluginTestNamespace},
		Spec: authv1alpha1.AppRoleSecretIDSpec{
			ResourceSpec:   credConnection(),
			RoleName:       authTestDemo,
			Metadata:       map[string]string{pluginTestNamespace: "x"},
			CIDRList:       []string{cloudCIDR},
			TTL:            credDuration(time.Hour),
			NumUses:        new(int32(3)),
			WriteSecretRef: commonv1alpha1.LocalObjectReference{Name: authTestDemoLogin},
		},
	}
}

func delivered(accessor string) credResolver {
	return credResolver{secrets: map[string]map[string][]byte{authTestDemoLogin: {
		authLitRoleID: []byte(authLitRole), "secret_id": []byte("not-a-real-secret-id"), secretIDAccessorKey: []byte(accessor),
	}}}
}

// secretIDBao answers role reads, role-id reads and accessor lookups for the given live accessors.
func secretIDBao(t *testing.T, live ...string) *credBao {
	return &credBao{
		reads: map[string]map[string]any{
			secretIDRole:              {authKeyBindSecretID: true},
			secretIDRole + "/role-id": {authLitRoleID: authLitRole},
		},
		write: func(path string, data map[string]any) (map[string]any, error) {
			switch path {
			case secretIDRole + "/secret-id-accessor/lookup":
				if slices.Contains(live, data[secretIDAccessorKey].(string)) {
					return credJSON(t, secretIDLookup), nil
				}
				return nil, &openbao.ResponseError{StatusCode: 404}
			case secretIDRole + "/secret-id":
				return map[string]any{"secret_id": "new-secret", secretIDAccessorKey: authLitNewAccessor,
					"secret_id_ttl": 3600, "secret_id_num_uses": 3}, nil
			}
			return nil, nil
		},
	}
}

func TestSecretIDDesired(t *testing.T) {
	s := newSecretID()
	got, err := AppRoleSecretIDAdapter{}.Desired(context.Background(), credResolver{}, s)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		"metadata":          `{"team":"x"}`,
		"cidr_list":         []string{cloudCIDR},
		"ttl":               int64(3600),
		"num_uses":          int64(3),
		"role_path":         secretIDRole,
		secretIDAccessorKey: "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired =\n%v\nwant\n%v", got, want)
	}

	got, _ = AppRoleSecretIDAdapter{}.Desired(context.Background(), delivered(secretIDAccessor), s)
	if got[secretIDAccessorKey] != secretIDAccessor {
		t.Errorf("delivered accessor = %v", got[secretIDAccessorKey])
	}

	empty := delivered(secretIDAccessor)
	empty.secrets[authTestDemoLogin]["secret_id"] = nil
	got, _ = AppRoleSecretIDAdapter{}.Desired(context.Background(), empty, s)
	if got[secretIDAccessorKey] != "" {
		t.Error("a Secret with an empty secret_id must count as not delivered")
	}
}

func TestSecretIDDesiredRoleRef(t *testing.T) {
	s := newSecretID()
	s.Spec.RoleName = ""
	s.Spec.RoleRef = &commonv1alpha1.LocalObjectReference{Name: authTestDemo}
	role := newAppRole()
	role.Spec.MountPath = "machines"
	r := credResolver{appRoles: map[string]*authv1alpha1.AppRole{authTestDemo: role}}

	if _, err := (AppRoleSecretIDAdapter{}).Desired(context.Background(), r, s); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("not ready AppRole: err = %v", err)
	}
	if _, err := (AppRoleSecretIDAdapter{}).Desired(context.Background(), credResolver{}, s); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing AppRole: err = %v", err)
	}

	meta.SetStatusCondition(&role.Status.Conditions, metav1.Condition{Type: commonv1alpha1.ConditionReady,
		Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable})
	got, err := AppRoleSecretIDAdapter{}.Desired(context.Background(), r, s)
	if err != nil || got["role_path"] != "auth/machines/role/demo" {
		t.Errorf("role_path = %v, err %v", got["role_path"], err)
	}

	role.Spec.Namespace = "elsewhere"
	if _, err := (AppRoleSecretIDAdapter{}).Desired(context.Background(), r, s); err == nil || errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("AppRole in another OpenBao namespace: err = %v", err)
	}
}

func TestSecretIDObserve(t *testing.T) {
	ctx := context.Background()
	s := newSecretID()
	adapter := AppRoleSecretIDAdapter{Now: func() time.Time { return secretIDCreated.Add(10 * time.Minute) }}

	if _, found, err := adapter.Observe(ctx, secretIDBao(t, secretIDAccessor), s); found || err != nil {
		t.Fatalf("no recorded accessor: found=%v err=%v", found, err)
	}

	s.Status.ID, s.Status.RolePath = secretIDAccessor, secretIDRole
	observed, found, err := adapter.Observe(ctx, secretIDBao(t, secretIDAccessor), s)
	if err != nil || !found {
		t.Fatalf("live secret ID: found=%v err=%v", found, err)
	}
	inSync, _ := adapter.Desired(ctx, delivered(secretIDAccessor), s)
	credExpectDiff(t, inSync, observed, adapter.Fields())
	lost, _ := adapter.Desired(ctx, credResolver{}, s)
	credExpectDiff(t, lost, observed, adapter.Fields(), secretIDAccessorKey)
	stale, _ := adapter.Desired(ctx, delivered("old"), s)
	credExpectDiff(t, stale, observed, adapter.Fields(), secretIDAccessorKey)

	if _, found, err := adapter.Observe(ctx, secretIDBao(t), s); found || err != nil {
		t.Errorf("destroyed or tidied secret ID: found=%v err=%v", found, err)
	}
	missingRole := secretIDBao(t, secretIDAccessor)
	delete(missingRole.reads, secretIDRole)
	if _, found, err := adapter.Observe(ctx, missingRole, s); found || err != nil {
		t.Errorf("missing role: found=%v err=%v", found, err)
	}
	if len(missingRole.wrote(secretIDRole+"/secret-id-accessor/lookup")) != 0 {
		t.Error("lookup sent under a missing role")
	}
}

func TestSecretIDRotation(t *testing.T) {
	expiry := secretIDCreated.Add(time.Hour)
	tests := []struct {
		name     string
		rotation *authv1alpha1.SecretIDRotation
		now      time.Time
		due      bool
	}{
		{"default third before expiry, early", nil, expiry.Add(-21 * time.Minute), false},
		{"default third before expiry, due", nil, expiry.Add(-19 * time.Minute), true},
		{"percent", &authv1alpha1.SecretIDRotation{RenewBefore: "50%"}, expiry.Add(-29 * time.Minute), true},
		{"duration", &authv1alpha1.SecretIDRotation{RenewBefore: "5m"}, expiry.Add(-6 * time.Minute), false},
		{"window not shorter than ttl falls back", &authv1alpha1.SecretIDRotation{RenewBefore: "2h"}, secretIDCreated.Add(time.Minute), false},
		{"disabled", &authv1alpha1.SecretIDRotation{Enabled: new(false)}, expiry.Add(-time.Minute), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSecretID()
			s.Spec.Rotation = tt.rotation
			s.Status.ID, s.Status.RolePath = secretIDAccessor, secretIDRole
			adapter := AppRoleSecretIDAdapter{Now: func() time.Time { return tt.now }}
			_, found, err := adapter.Observe(context.Background(), secretIDBao(t, secretIDAccessor), s)
			if err != nil || found == tt.due {
				t.Errorf("found=%v err=%v, want due=%v", found, err, tt.due)
			}
		})
	}

	noTTL := credJSON(t, secretIDLookup)
	noTTL["secret_id_ttl"] = 0
	noTTL["expiration_time"] = "0001-01-01T00:00:00Z"
	if (AppRoleSecretIDAdapter{Now: time.Now}).rotationDue(newSecretID(), noTTL) {
		t.Error("a secret ID without TTL is never rotated")
	}
}

func TestSecretIDWriteRetiresAndOutputs(t *testing.T) {
	ctx := context.Background()
	s := newSecretID()
	s.Status.ID, s.Status.RolePath = secretIDAccessor, secretIDRole
	s.Status.RetiredAccessors = []string{"older"}
	adapter := AppRoleSecretIDAdapter{}
	bao := secretIDBao(t, secretIDAccessor, "older", "delivered")

	desired, _ := adapter.Desired(ctx, delivered("delivered"), s)
	resp, err := adapter.Write(ctx, bao, s, desired, false)
	if err != nil {
		t.Fatal(err)
	}
	sent := bao.wrote(secretIDRole + "/secret-id")
	if len(sent) != 1 || sent[0]["role_path"] != nil || sent[0][secretIDAccessorKey] != nil || sent[0]["ttl"] != int64(3600) {
		t.Errorf("create body = %v", sent)
	}
	if s.Status.ID != authLitNewAccessor || s.Status.RolePath != secretIDRole {
		t.Errorf("status = %+v", s.Status)
	}
	if want := []string{"older", secretIDAccessor, "delivered"}; !slices.Equal(s.Status.RetiredAccessors, want) {
		t.Errorf("retired = %v, want %v", s.Status.RetiredAccessors, want)
	}

	name, data, ok := adapter.Outputs(s, resp)
	if !ok || name != authTestDemoLogin || string(data["secret_id"]) != "new-secret" || string(data[authLitRoleID]) != authLitRole ||
		string(data[secretIDAccessorKey]) != authLitNewAccessor {
		t.Errorf("Outputs = %s %v %v", name, data, ok)
	}
	if _, _, ok := adapter.Outputs(s, nil); ok {
		t.Error("no Secret may be written without a secret_id")
	}

	if err := adapter.OutputsDelivered(ctx, bao, s); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteStatus(ctx, bao, s, engine.Payload{"expiration_time": "2026-11-14T01:24:03Z"}); err != nil {
		t.Fatal(err)
	}
	if len(s.Status.RetiredAccessors) != 0 || s.Status.ExpirationTime == nil {
		t.Errorf("after OutputsDelivered and WriteStatus: %+v", s.Status)
	}
	if got := len(bao.wrote(secretIDRole + "/secret-id-accessor/destroy")); got != 3 {
		t.Errorf("destroyed %d secret IDs, want 3", got)
	}
}

func TestSecretIDWriteCapsUndeliveredSecretIDs(t *testing.T) {
	ctx := context.Background()
	s := newSecretID()
	s.Status.ID, s.Status.RolePath = secretIDAccessor, secretIDRole
	s.Status.RetiredAccessors = []string{"delivered", "undelivered-1", "undelivered-2", "undelivered-3"}
	adapter := AppRoleSecretIDAdapter{}
	bao := secretIDBao(t, append([]string{secretIDAccessor}, s.Status.RetiredAccessors...)...)

	desired, _ := adapter.Desired(ctx, delivered("delivered"), s)
	if _, err := adapter.Write(ctx, bao, s, desired, false); err != nil {
		t.Fatal(err)
	}
	destroyed := bao.wrote(secretIDRole + "/secret-id-accessor/destroy")
	if len(destroyed) != 1 || destroyed[0][secretIDAccessorKey] != "undelivered-1" {
		t.Errorf("destroyed %v, want only the oldest undelivered secret ID", destroyed)
	}
	if want := []string{"delivered", "undelivered-2", "undelivered-3", secretIDAccessor}; !slices.Equal(s.Status.RetiredAccessors, want) {
		t.Errorf("retired = %v, want %v", s.Status.RetiredAccessors, want)
	}
}

func TestSecretIDOutputsDeliveredKeepsFailedDestroys(t *testing.T) {
	s := newSecretID()
	s.Status.ID, s.Status.RolePath = authLitNewAccessor, secretIDRole
	s.Status.RetiredAccessors = []string{secretIDAccessor}
	bao := secretIDBao(t, secretIDAccessor)
	lookup := bao.write
	bao.write = func(path string, data map[string]any) (map[string]any, error) {
		if path == secretIDRole+"/secret-id-accessor/destroy" {
			return nil, &openbao.ResponseError{StatusCode: 403}
		}
		return lookup(path, data)
	}
	if err := (AppRoleSecretIDAdapter{}).OutputsDelivered(context.Background(), bao, s); err == nil {
		t.Error("a failed destroy must be reported")
	}
	if !slices.Equal(s.Status.RetiredAccessors, []string{secretIDAccessor}) {
		t.Errorf("retired = %v, want the failed one kept", s.Status.RetiredAccessors)
	}
}

func TestSecretIDDelete(t *testing.T) {
	s := newSecretID()
	s.Status.ID, s.Status.RolePath = secretIDAccessor, secretIDRole
	s.Status.RetiredAccessors = []string{"gone"}
	bao := secretIDBao(t, secretIDAccessor)
	if err := (AppRoleSecretIDAdapter{}).Delete(context.Background(), bao, s); err != nil {
		t.Fatal(err)
	}
	destroyed := bao.wrote(secretIDRole + "/secret-id-accessor/destroy")
	if len(destroyed) != 1 || destroyed[0][secretIDAccessorKey] != secretIDAccessor {
		t.Errorf("destroyed %v, want only the live accessor", destroyed)
	}

	noRole := secretIDBao(t, secretIDAccessor)
	delete(noRole.reads, secretIDRole)
	if err := (AppRoleSecretIDAdapter{}).Delete(context.Background(), noRole, s); err != nil || len(noRole.writes) != 0 {
		t.Errorf("deleted role: err=%v writes=%v", err, noRole.writes)
	}
}

// TestSecretIDWriteRetiresOldRoleFirst: when the role path changes, the secret IDs recorded under the old role are
// destroyed there before a secret ID is generated under the new role, and a failed destroy generates nothing.
func TestSecretIDWriteRetiresOldRoleFirst(t *testing.T) {
	const oldRole = "auth/approle-old/role/demo"
	ctx := context.Background()
	newState := func() *authv1alpha1.AppRoleSecretID {
		s := newSecretID()
		s.Status.ID, s.Status.RolePath = secretIDAccessor, oldRole
		s.Status.RetiredAccessors = []string{"older"}
		return s
	}
	bao := func(destroyErr error) *credBao {
		b := secretIDBao(t)
		b.reads[oldRole] = map[string]any{authKeyBindSecretID: true}
		newRole := b.write
		b.write = func(path string, data map[string]any) (map[string]any, error) {
			switch path {
			case oldRole + "/secret-id-accessor/lookup":
				if slices.Contains([]string{secretIDAccessor, "older", "delivered"}, data[secretIDAccessorKey].(string)) {
					return credJSON(t, secretIDLookup), nil
				}
				return nil, &openbao.ResponseError{StatusCode: 404}
			case oldRole + "/secret-id-accessor/destroy":
				return nil, destroyErr
			}
			return newRole(path, data)
		}
		return b
	}
	adapter := AppRoleSecretIDAdapter{}

	s := newState()
	b := bao(nil)
	desired, _ := adapter.Desired(ctx, delivered("delivered"), s)
	if _, err := adapter.Write(ctx, b, s, desired, false); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OutputsDelivered(ctx, b, s); err != nil {
		t.Fatal(err)
	}
	destroys := b.wrote(oldRole + "/secret-id-accessor/destroy")
	destroyed := make([]string, 0, len(destroys))
	for _, d := range destroys {
		destroyed = append(destroyed, d[secretIDAccessorKey].(string))
	}
	if want := []string{"older", secretIDAccessor, "delivered"}; !slices.Equal(destroyed, want) {
		t.Errorf("destroyed under the old role %v, want %v", destroyed, want)
	}
	if s.Status.RolePath != secretIDRole || s.Status.ID != authLitNewAccessor || len(s.Status.RetiredAccessors) != 0 {
		t.Errorf("status = %+v", s.Status)
	}

	s = newState()
	b = bao(&openbao.ResponseError{StatusCode: 403})
	if _, err := adapter.Write(ctx, b, s, desired, false); err == nil {
		t.Error("a failed destroy under the old role must fail the write")
	}
	if len(b.wrote(secretIDRole+"/secret-id")) != 0 || s.Status.RolePath != oldRole || s.Status.ID != secretIDAccessor {
		t.Errorf("generated a secret ID or changed the status before the old ones were destroyed: %+v", s.Status)
	}
}
