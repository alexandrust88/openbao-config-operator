package identity

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func oidcDuration(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }

func TestOIDCConfig(t *testing.T) {
	a := OIDCConfigAdapter{}
	cfg := &identityv1alpha1.OIDCConfig{ObjectMeta: oidcMeta("cfg"),
		Spec: identityv1alpha1.OIDCConfigSpec{Issuer: litIssuerURL}}
	desired, _ := a.Desired(context.Background(), nil, cfg)
	if a.Path(cfg) != "identity/oidc/config" || !reflect.DeepEqual(desired, engine.Payload{litIssuer: litIssuerURL}) {
		t.Fatalf("path %q desired %v", a.Path(cfg), desired)
	}
	assertNoDiff(t, desired, map[string]any{litIssuer: litIssuerURL}, a.Fields())
	assertDiff(t, desired, map[string]any{litIssuer: ""}, a.Fields(), litIssuer)

	empty := &identityv1alpha1.OIDCConfig{ObjectMeta: oidcMeta("cfg")}
	desired, _ = a.Desired(context.Background(), nil, empty)
	assertNoDiff(t, desired, map[string]any{litIssuer: ""}, a.Fields())

	ctx := context.Background()
	if !a.Singleton() {
		t.Error("the OIDC configuration always exists and must be a singleton")
	}
	bao := newOIDCBao(nil)
	if err := a.Delete(ctx, bao, cfg); err != nil {
		t.Fatal(err)
	}
	if got := bao.written["identity/oidc/config"]; !reflect.DeepEqual(got, map[string]any{litIssuer: ""}) {
		t.Errorf("delete wrote %v, want the issuer reset", got)
	}
}

func TestOIDCKey(t *testing.T) {
	a := OIDCKeyAdapter{}
	key := &identityv1alpha1.OIDCKey{ObjectMeta: oidcMeta(litSigner), Spec: identityv1alpha1.OIDCKeySpec{
		RotationPeriod:   oidcDuration(12 * time.Hour),
		VerificationTTL:  oidcDuration(6 * time.Hour),
		Algorithm:        litES256,
		AllowedClientIDs: []string{"b", "*"},
	}}
	if got := a.Path(key); got != "identity/oidc/key/signer" {
		t.Errorf("path %q", got)
	}
	desired, _ := a.Desired(context.Background(), nil, key)
	want := engine.Payload{litRotationPeriod: int64(43200), "verification_ttl": int64(21600), litAlgorithm: litES256,
		litAllowedClientIDs: []string{"b", "*"}}
	if !reflect.DeepEqual(desired, want) {
		t.Fatalf("desired %v", desired)
	}
	read := map[string]any{litAlgorithm: litES256, litAllowedClientIDs: []any{"*", "b"}, litRotationPeriod: 43200,
		"verification_ttl": 21600}
	assertNoDiff(t, desired, read, a.Fields())
	assertDiff(t, desired, oidcCopy(read, map[string]any{litRotationPeriod: 3600}), a.Fields(), litRotationPeriod)

	unset, _ := a.Desired(context.Background(), nil, &identityv1alpha1.OIDCKey{ObjectMeta: oidcMeta("k")})
	// `bao read identity/oidc/key/k0` of a key written with no fields, OpenBao 2.7.2.
	emptyRead := map[string]any{litAlgorithm: "RS256", litAllowedClientIDs: []any{}, litRotationPeriod: 86400,
		"verification_ttl": 86400}
	if len(unset) != len(emptyRead) {
		t.Errorf("an empty spec must send every field with its default: %v", unset)
	}
	assertNoDiff(t, unset, emptyRead, a.Fields())

	bao := newOIDCBao(nil)
	builtin := &identityv1alpha1.OIDCKey{ObjectMeta: oidcMeta("x"), Spec: identityv1alpha1.OIDCKeySpec{Name: "default"}}
	if err := a.Delete(context.Background(), bao, builtin); err != nil || len(bao.deletes) != 0 {
		t.Errorf("default key deleted: %v %v", bao.deletes, err)
	}
	if err := a.Delete(context.Background(), bao, key); err != nil || !slices.Equal(bao.deletes, []string{"identity/oidc/key/signer"}) {
		t.Errorf("deletes %v err %v", bao.deletes, err)
	}
}

func TestOIDCRole(t *testing.T) {
	a := OIDCRoleAdapter{}
	role := &identityv1alpha1.OIDCRole{ObjectMeta: oidcMeta("app"), Spec: identityv1alpha1.OIDCRoleSpec{
		Name: "app-role", Key: litSigner, Template: litTemplateJSON, TTL: oidcDuration(time.Hour), ClientID: litAud1,
	}}
	if got := a.Path(role); got != "identity/oidc/role/app-role" {
		t.Errorf("path %q", got)
	}
	desired, _ := a.Desired(context.Background(), nil, role)
	want := engine.Payload{litKey: litSigner, litTemplate: litTemplateJSON, litTTL: int64(3600), litClientID: litAud1}
	if !reflect.DeepEqual(desired, want) {
		t.Fatalf("desired %v", desired)
	}
	read := map[string]any{litClientID: litAud1, litKey: litSigner, litTemplate: litTemplateJSON, litTTL: 3600}
	assertNoDiff(t, desired, read, a.Fields())
	assertDiff(t, desired, oidcCopy(read, map[string]any{litKey: "other"}), a.Fields(), litKey)

	generated := &identityv1alpha1.OIDCRole{ObjectMeta: oidcMeta("gen"), Spec: identityv1alpha1.OIDCRoleSpec{Key: litSigner}}
	desired, _ = a.Desired(context.Background(), nil, generated)
	if _, ok := desired[litClientID]; ok {
		t.Error("client_id sent although unset")
	}
	assertNoDiff(t, desired, map[string]any{litClientID: litGeneratedClientID, litKey: litSigner, litTemplate: "", litTTL: 86400}, a.Fields())
	if err := a.WriteStatus(context.Background(), nil, generated, map[string]any{litClientID: litGeneratedClientID}); err != nil {
		t.Fatal(err)
	}
	if generated.Status.ID != litGeneratedClientID {
		t.Errorf("status.id = %q", generated.Status.ID)
	}
}

func TestOIDCScope(t *testing.T) {
	a := OIDCScopeAdapter{}
	scope := &identityv1alpha1.OIDCScope{ObjectMeta: oidcMeta(litGroups), Spec: identityv1alpha1.OIDCScopeSpec{
		Template: `{litGroups:{{identity.entity.groups.names}}}`, Description: "group names",
	}}
	desired, _ := a.Desired(context.Background(), nil, scope)
	read := map[string]any{"description": "group names", litTemplate: `{litGroups:{{identity.entity.groups.names}}}`}
	assertNoDiff(t, desired, read, a.Fields())
	assertDiff(t, desired, oidcCopy(read, map[string]any{"description": ""}), a.Fields(), "description")
	if a.Path(scope) != "identity/oidc/scope/groups" {
		t.Errorf("path %q", a.Path(scope))
	}
}

func TestOIDCAssignment(t *testing.T) {
	entity := oidcReady(&identityv1alpha1.IdentityEntity{ObjectMeta: oidcMeta(litAlice)}, "11111111-aaaa-bbbb-cccc-000000000001")
	devA := oidcReady(&identityv1alpha1.IdentityGroup{ObjectMeta: metav1.ObjectMeta{Name: "dev-a", Namespace: litTeam,
		Labels: map[string]string{litTeam: litDev}}}, "22222222-aaaa-bbbb-cccc-000000000001")
	devB := oidcReady(&identityv1alpha1.IdentityGroup{ObjectMeta: metav1.ObjectMeta{Name: "dev-b", Namespace: litTeam,
		Labels: map[string]string{litTeam: litDev}}}, "22222222-aaaa-bbbb-cccc-000000000002")
	bao := newOIDCBao(map[string]map[string]any{
		"identity/entity/name/bob": {"id": litBobEntityID},
	})
	a := OIDCAssignmentAdapter{Reader: oidcReader(t, entity, devA, devB), Conns: oidcConns{client: bao}}
	assignment := &identityv1alpha1.OIDCAssignment{ObjectMeta: oidcMeta(litDevs), Spec: identityv1alpha1.OIDCAssignmentSpec{
		Entities: []identityv1alpha1.EntityMatch{
			{EntityID: "ABCDEF00-0000-0000-0000-000000000000"},
			{EntityName: "bob"},
			{EntityRef: &commonv1alpha1.LocalObjectReference{Name: litAlice}},
		},
		Groups: []identityv1alpha1.GroupMatch{{GroupSelector: &commonv1alpha1.LabelSelector{MatchLabels: map[string]string{litTeam: litDev}}}},
	}}
	desired, err := a.Desired(context.Background(), nil, assignment)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		litEntityIDs: []string{"11111111-aaaa-bbbb-cccc-000000000001", litBobEntityID,
			"abcdef00-0000-0000-0000-000000000000"},
		litGroupIDs: []string{"22222222-aaaa-bbbb-cccc-000000000001", "22222222-aaaa-bbbb-cccc-000000000002"},
	}
	if !reflect.DeepEqual(desired, want) {
		t.Fatalf("desired %v", desired)
	}
	read := map[string]any{
		litEntityIDs: []any{"abcdef00-0000-0000-0000-000000000000", litBobEntityID,
			"11111111-aaaa-bbbb-cccc-000000000001"},
		litGroupIDs: []any{"22222222-aaaa-bbbb-cccc-000000000002", "22222222-aaaa-bbbb-cccc-000000000001"},
	}
	assertNoDiff(t, desired, read, a.Fields())
	assertDiff(t, desired, oidcCopy(read, map[string]any{litGroupIDs: []any{}}), a.Fields(), litGroupIDs)

	empty, err := a.Desired(context.Background(), nil, &identityv1alpha1.OIDCAssignment{ObjectMeta: oidcMeta("none")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(empty, engine.Payload{litEntityIDs: []string{}, litGroupIDs: []string{}}) {
		t.Errorf("empty spec must clear both lists, got %v", empty)
	}

	missing := assignment.DeepCopy()
	missing.Spec.Entities = []identityv1alpha1.EntityMatch{{EntityRef: &commonv1alpha1.LocalObjectReference{Name: litNobody}}}
	if _, err := a.Desired(context.Background(), nil, missing); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing referent: got %v, want ErrReferenceNotReady", err)
	}
}

func oidcClientRead() map[string]any {
	return map[string]any{
		"access_token_ttl": 7200, "assignments": []any{"a1"}, "authorization_code": true, "client_credentials": false,
		litClientID: litAppClientID, "client_secret": "hvo_secret_fake", litClientType: litConfidential,
		litIDTokenTTL: 3600, litKey: "k1", "redirect_uris": []any{litRedirectURI, litRedirectURI2},
	}
}

func TestOIDCClientDesiredAndSteadyState(t *testing.T) {
	a := OIDCClientAdapter{}
	c := &identityv1alpha1.OIDCClient{ObjectMeta: oidcMeta("app"), Spec: identityv1alpha1.OIDCClientSpec{
		Key: "k1", ClientType: litConfidential, RedirectURIs: []string{litRedirectURI2, litRedirectURI},
		Assignments: []string{"a1"}, IDTokenTTL: oidcDuration(time.Hour), AccessTokenTTL: oidcDuration(2 * time.Hour),
		AuthorizationCode: new(true), ClientCredentials: new(false),
	}}
	desired, _ := a.Desired(context.Background(), nil, c)
	want := engine.Payload{litKey: "k1", litClientType: litConfidential, "redirect_uris": []string{litRedirectURI2, litRedirectURI},
		"assignments": []string{"a1"}, litIDTokenTTL: int64(3600), "access_token_ttl": int64(7200),
		"authorization_code": true, "client_credentials": false}
	if !reflect.DeepEqual(desired, want) {
		t.Fatalf("desired %v", desired)
	}
	assertNoDiff(t, desired, oidcClientRead(), a.Fields())
	assertDiff(t, desired, oidcCopy(oidcClientRead(), map[string]any{litIDTokenTTL: 60}), a.Fields(), litIDTokenTTL)

	defaults, _ := a.Desired(context.Background(), nil, &identityv1alpha1.OIDCClient{ObjectMeta: oidcMeta("d")})
	// `bao read identity/oidc/client/c0` of a client written with no fields, OpenBao 2.7.2.
	emptyRead := map[string]any{"access_token_ttl": 86400, "assignments": []any{}, "authorization_code": true,
		"client_credentials": false, litClientID: litAppClientID, "client_secret": "hvo_secret_fake",
		litClientType: litConfidential, litIDTokenTTL: 86400, litKey: "default", "redirect_uris": []any{}}
	if len(defaults) != len(emptyRead)-2 {
		t.Errorf("an empty spec must send every field with its default: %v", defaults)
	}
	assertNoDiff(t, defaults, emptyRead, a.Fields())
}

func TestOIDCClientOutputs(t *testing.T) {
	ref := &commonv1alpha1.LocalObjectReference{Name: litAppOIDC}
	c := &identityv1alpha1.OIDCClient{ObjectMeta: oidcMeta("app"), Spec: identityv1alpha1.OIDCClientSpec{WriteSecretRef: ref}}
	a := OIDCClientAdapter{}

	name, data, ok := a.Outputs(c, oidcClientRead())
	if !ok || name != litAppOIDC || string(data[OIDCClientIDKey]) != litAppClientID ||
		string(data[OIDCClientSecretKey]) != "hvo_secret_fake" {
		t.Errorf("confidential outputs: %s %v %v", name, ok, len(data))
	}
	public := oidcCopy(oidcClientRead(), map[string]any{litClientType: "public"})
	delete(public, "client_secret")
	if _, data, ok := a.Outputs(c, public); !ok || len(data) != 1 {
		t.Errorf("public outputs: ok=%v keys=%d, want client_id only", ok, len(data))
	}
	if _, _, ok := a.Outputs(c, nil); ok {
		t.Error("an empty response must not write the Secret")
	}
	if _, _, ok := a.Outputs(&identityv1alpha1.OIDCClient{}, oidcClientRead()); ok {
		t.Error("no writeSecretRef must not write a Secret")
	}

	if err := a.WriteStatus(context.Background(), nil, c, oidcClientRead()); err != nil || c.Status.ID != litAppClientID {
		t.Errorf("status.id = %q err %v", c.Status.ID, err)
	}
}

// TestOIDCClientRedeliversOutputWithoutWrite: a missing or stale output Secret of an unchanged client is written
// again from the client's read, without a write to OpenBao or a reported drift.
func TestOIDCClientRedeliversOutputWithoutWrite(t *testing.T) {
	ref := &commonv1alpha1.LocalObjectReference{Name: litAppOIDC}
	current := &corev1.Secret{ObjectMeta: oidcMeta(litAppOIDC), Data: map[string][]byte{
		OIDCClientIDKey: []byte(litAppClientID), OIDCClientSecretKey: []byte("hvo_secret_fake"),
	}}
	stale := current.DeepCopy()
	stale.Data[OIDCClientSecretKey] = []byte("old")

	for name, existing := range map[string][]client.Object{"missing secret": nil, "stale secret": {stale}} {
		t.Run(name, func(t *testing.T) {
			c := &identityv1alpha1.OIDCClient{ObjectMeta: oidcMeta("app"), Spec: identityv1alpha1.OIDCClientSpec{
				Key: "k1", ClientType: litConfidential, WriteSecretRef: ref, RedirectURIs: []string{litRedirectURI, litRedirectURI2},
				Assignments: []string{"a1"}, IDTokenTTL: oidcDuration(time.Hour), AccessTokenTTL: oidcDuration(2 * time.Hour),
			}}
			c.Generation = 1
			c.Status.Acquired, c.Status.Path = commonv1alpha1.AcquiredCreated, litAppClientPath
			c.Status.ServerID = engine.ConnectionKey(c.Namespace, c.Spec.ConnectionRef)
			c.Status.Conditions = []metav1.Condition{
				{Type: commonv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonAvailable,
					ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
				{Type: commonv1alpha1.ConditionSynced, Status: metav1.ConditionTrue, Reason: commonv1alpha1.ReasonReconcileSuccess,
					ObservedGeneration: 1, LastTransitionTime: metav1.Now()},
			}
			for _, s := range existing {
				s := s.(*corev1.Secret)
				s.OwnerReferences = []metav1.OwnerReference{{APIVersion: identityv1alpha1.GroupVersion.String(), Kind: "OIDCClient",
					Name: c.Name, UID: c.UID, Controller: new(true)}}
			}
			scheme := oidcScheme(t)
			k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(c).
				WithObjects(append(existing, c)...).WithIndex(c, engine.PathIndex, engine.PathIndexFunc).Build()
			bao := newOIDCBao(map[string]map[string]any{litAppClientPath: oidcClientRead()})
			r := engine.New(k8s, scheme, events.NewFakeRecorder(100), oidcConns{client: bao}, OIDCClientAdapter{},
				func() *identityv1alpha1.OIDCClient { return &identityv1alpha1.OIDCClient{} }, engine.Options{})

			if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if len(bao.writes) != 0 {
				t.Errorf("OpenBao writes = %v, want none for an output-only repair", bao.writes)
			}
			secret := &corev1.Secret{}
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(current), secret); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(secret.Data, current.Data) {
				t.Error("output Secret does not hold the client's current credentials")
			}
			got := &identityv1alpha1.OIDCClient{}
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(c), got); err != nil {
				t.Fatal(err)
			}
			if !meta.IsStatusConditionTrue(got.Status.Conditions, commonv1alpha1.ConditionSynced) {
				t.Errorf("conditions = %+v, want Synced", got.Status.Conditions)
			}
		})
	}
}

func TestOIDCClientWriteReadsBack(t *testing.T) {
	bao := newOIDCBao(map[string]map[string]any{litAppClientPath: oidcClientRead()})
	c := &identityv1alpha1.OIDCClient{ObjectMeta: oidcMeta("app")}
	response, err := OIDCClientAdapter{}.Write(context.Background(), bao, c, engine.Payload{litKey: "k1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if response[litClientID] != litAppClientID || !slices.Equal(bao.writes, []string{litAppClientPath}) {
		t.Errorf("response %v writes %v", response[litClientID], bao.writes)
	}
}

func TestOIDCProvider(t *testing.T) {
	app := oidcReady(&identityv1alpha1.OIDCClient{ObjectMeta: oidcMeta("app")}, "client-from-ref")
	bao := newOIDCBao(map[string]map[string]any{
		"identity/oidc/client/portal": {litClientID: litClientFromName},
	})
	a := OIDCProviderAdapter{Reader: oidcReader(t, app), Conns: oidcConns{client: bao}}
	provider := &identityv1alpha1.OIDCProvider{ObjectMeta: oidcMeta("main"), Spec: identityv1alpha1.OIDCProviderSpec{
		Issuer: litIssuerURL,
		AllowedClients: []identityv1alpha1.OIDCClientMatch{
			{ClientID: litLiteral},
			{ClientName: "portal"},
			{ClientRef: &commonv1alpha1.LocalObjectReference{Name: "app"}},
		},
		ScopesSupported: []string{litGroups},
	}}
	desired, err := a.Desired(context.Background(), nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{litIssuer: litIssuerURL,
		litAllowedClientIDs: []string{litClientFromName, "client-from-ref", litLiteral}, "scopes_supported": []string{litGroups}}
	if !reflect.DeepEqual(desired, want) {
		t.Fatalf("desired %v", desired)
	}

	bao.data["identity/oidc/provider/main"] = map[string]any{
		litAllowedClientIDs: []any{litLiteral, "client-from-ref", litClientFromName},
		litIssuer:           "https://bao.example.com:8200/v1/team/identity/oidc/provider/main",
		"scopes_supported":  []any{litGroups},
	}
	observed, found, err := a.Observe(context.Background(), bao, provider)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertNoDiff(t, desired, observed, a.Fields())
	bao.data["identity/oidc/provider/main"][litIssuer] = "https://other.example.com/v1/identity/oidc/provider/main"
	observed, _, _ = a.Observe(context.Background(), bao, provider)
	assertDiff(t, desired, observed, a.Fields(), litIssuer)

	bare := &identityv1alpha1.OIDCProvider{ObjectMeta: oidcMeta("p")}
	unset, _ := a.Desired(context.Background(), nil, bare)
	if !reflect.DeepEqual(unset, engine.Payload{litIssuer: "", litAllowedClientIDs: []string{}, "scopes_supported": []string{}}) {
		t.Errorf("an empty spec must send every field with its default: %v", unset)
	}
	// `bao read identity/oidc/provider/p0` of a provider written with no fields, OpenBao 2.7.2: the issuer derives
	// from api_addr.
	bao.data["identity/oidc/provider/p"] = map[string]any{litAllowedClientIDs: []any{},
		litIssuer: "http://127.0.0.1:8200/v1/identity/oidc/provider/p", "scopes_supported": []any{}}
	if observed, found, err := a.Observe(context.Background(), bao, bare); err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	} else {
		assertNoDiff(t, unset, observed, a.Fields())
	}

	notReady := provider.DeepCopy()
	notReady.Spec.AllowedClients = []identityv1alpha1.OIDCClientMatch{{ClientName: litMissing}}
	if _, err := a.Desired(context.Background(), nil, notReady); !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Errorf("missing client: got %v", err)
	}

	builtin := &identityv1alpha1.OIDCProvider{ObjectMeta: oidcMeta("default")}
	if err := a.Delete(context.Background(), bao, builtin); err != nil || len(bao.deletes) != 0 {
		t.Errorf("default provider deleted: %v %v", bao.deletes, err)
	}
}
