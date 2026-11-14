package identity

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcclients,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcclients/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.openbaoconfig.io,resources=oidcclients/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

// Keys of the Secret named by an OIDCClient's writeSecretRef.
const (
	OIDCClientIDKey     = "client_id"
	OIDCClientSecretKey = "client_secret"
)

// SetupOIDCClient registers the OIDCClient controller with the manager.
func SetupOIDCClient(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*identityv1alpha1.OIDCClient](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("oidcclient-controller"),
		conns,
		OIDCClientAdapter{},
		func() *identityv1alpha1.OIDCClient { return &identityv1alpha1.OIDCClient{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// OIDCClientAdapter maps an OIDCClient onto identity/oidc/client/<name>.
type OIDCClientAdapter struct{}

var (
	_ engine.Adapter[*identityv1alpha1.OIDCClient]      = OIDCClientAdapter{}
	_ engine.Writer[*identityv1alpha1.OIDCClient]       = OIDCClientAdapter{}
	_ engine.OutputWriter[*identityv1alpha1.OIDCClient] = OIDCClientAdapter{}
	_ engine.StatusWriter[*identityv1alpha1.OIDCClient] = OIDCClientAdapter{}

	_ engine.OutputSecretNamer[*identityv1alpha1.OIDCClient] = OIDCClientAdapter{}
	_ engine.OutputRefresher[*identityv1alpha1.OIDCClient]   = OIDCClientAdapter{}
)

// Path implements engine.Adapter.
func (OIDCClientAdapter) Path(o *identityv1alpha1.OIDCClient) string {
	return "identity/oidc/client/" + oidcName(o.Spec.Name, o.Name)
}

// Desired implements engine.Adapter. key and client_type are always sent; the server accepts them unchanged on
// update and rejects a change.
func (OIDCClientAdapter) Desired(_ context.Context, _ engine.Resolver, o *identityv1alpha1.OIDCClient) (engine.Payload, error) {
	return engine.Payload{
		"key":                oidcClientKey(o),
		"client_type":        string(oidcClientType(o)),
		"redirect_uris":      listOrEmpty(o.Spec.RedirectURIs),
		"assignments":        listOrEmpty(o.Spec.Assignments),
		"id_token_ttl":       secondsOr(o.Spec.IDTokenTTL, oidcDefaultTTL),
		"access_token_ttl":   secondsOr(o.Spec.AccessTokenTTL, oidcDefaultTTL),
		"authorization_code": boolOr(o.Spec.AuthorizationCode, true),
		"client_credentials": boolOr(o.Spec.ClientCredentials, false),
	}, nil
}

// Fields implements engine.Adapter.
func (OIDCClientAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"redirect_uris", "assignments"},
		Durations: []string{"id_token_ttl", "access_token_ttl"},
	}
}

// Write implements engine.Writer. The write returns no data, so the client is read back for its generated
// credentials.
func (a OIDCClientAdapter) Write(ctx context.Context, c openbao.Client, o *identityv1alpha1.OIDCClient, desired engine.Payload, _ bool) (engine.Payload, error) {
	path := a.Path(o)
	if _, err := c.Write(ctx, path, desired); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	response, err := c.Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return response, nil
}

// Outputs implements engine.OutputWriter.
func (OIDCClientAdapter) Outputs(o *identityv1alpha1.OIDCClient, response engine.Payload) (string, map[string][]byte, bool) {
	if o.Spec.WriteSecretRef == nil {
		return "", nil, false
	}
	id, _ := response["client_id"].(string)
	if id == "" {
		return "", nil, false
	}
	data := map[string][]byte{OIDCClientIDKey: []byte(id)}
	if secret, _ := response["client_secret"].(string); secret != "" {
		data[OIDCClientSecretKey] = []byte(secret)
	}
	return o.Spec.WriteSecretRef.Name, data, true
}

// RefreshOutputs implements engine.OutputRefresher: a read returns the client's credentials, so a missing or stale
// output Secret is written again from it.
func (OIDCClientAdapter) RefreshOutputs(_ context.Context, _ openbao.Client, _ *identityv1alpha1.OIDCClient,
	observed engine.Payload) (engine.Payload, error) {
	return observed, nil
}

// OutputSecretName implements engine.OutputSecretNamer.
func (OIDCClientAdapter) OutputSecretName(o *identityv1alpha1.OIDCClient) string {
	if o.Spec.WriteSecretRef == nil {
		return ""
	}
	return o.Spec.WriteSecretRef.Name
}

// WriteStatus implements engine.StatusWriter: status.id is the client ID.
func (OIDCClientAdapter) WriteStatus(_ context.Context, _ openbao.Client, o *identityv1alpha1.OIDCClient, observed engine.Payload) error {
	if id, ok := observed["client_id"].(string); ok {
		o.Status.ID = id
	}
	return nil
}

func oidcClientKey(o *identityv1alpha1.OIDCClient) string {
	if o.Spec.Key != "" {
		return o.Spec.Key
	}
	return builtinOIDCName
}

func oidcClientType(o *identityv1alpha1.OIDCClient) identityv1alpha1.OIDCClientType {
	if o.Spec.ClientType != "" {
		return o.Spec.ClientType
	}
	return identityv1alpha1.OIDCClientTypeConfidential
}
