package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.obco.dev,resources=awssecretsroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=awssecretsroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.obco.dev,resources=awssecretsroles/finalizers,verbs=update

// SetupAWSSecretsRole registers the AWSSecretsRole controller with the manager.
func SetupAWSSecretsRole(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "awssecretsrole", AWSSecretsRoleAdapter{},
		func() *secretsv1alpha1.AWSSecretsRole { return &secretsv1alpha1.AWSSecretsRole{} })
}

// AWSSecretsRoleAdapter maps an AWSSecretsRole onto <mountPath>/roles/<name>.
type AWSSecretsRoleAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.AWSSecretsRole] = AWSSecretsRoleAdapter{}
	_ engine.Writer[*secretsv1alpha1.AWSSecretsRole]  = AWSSecretsRoleAdapter{}
)

// Path implements engine.Adapter.
func (AWSSecretsRoleAdapter) Path(r *secretsv1alpha1.AWSSecretsRole) string {
	return awsSecretsMount(r.Spec.MountPath) + "/roles/" + specName(r.Spec.Name, r.Name)
}

// Desired implements engine.Adapter. The policy document is sent compacted, which is how the plugin stores and
// returns it.
func (AWSSecretsRoleAdapter) Desired(_ context.Context, _ engine.Resolver, r *secretsv1alpha1.AWSSecretsRole) (engine.Payload, error) {
	s := r.Spec
	p := engine.Payload{"credential_type": string(s.CredentialType)}
	putPluginList(p, "role_arns", s.RoleARNs)
	putPluginList(p, "policy_arns", s.PolicyARNs)
	if s.PolicyDocument != "" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(s.PolicyDocument)); err != nil {
			return nil, errors.New("spec.policyDocument is not valid JSON")
		}
		p["policy_document"] = compact.String()
	}
	putPluginList(p, "iam_groups", s.IAMGroups)
	putCloudMap(p, "iam_tags", s.IAMTags)
	putCloudMap(p, "session_tags", s.SessionTags)
	optString(p, "external_id", s.ExternalID)
	optSeconds(p, "default_sts_ttl", s.DefaultSTSTTL)
	optSeconds(p, "max_sts_ttl", s.MaxSTSTTL)
	optString(p, "user_path", s.UserPath)
	optString(p, "permissions_boundary_arn", s.PermissionsBoundaryARN)
	optString(p, "mfa_serial_number", s.MFASerialNumber)
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSSecretsRoleAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		Sets:      []string{"role_arns", "policy_arns", "iam_groups"},
		Durations: []string{"default_sts_ttl", "max_sts_ttl"},
	}
}

// Write implements engine.Writer.
func (a AWSSecretsRoleAdapter) Write(ctx context.Context, c openbao.Client, r *secretsv1alpha1.AWSSecretsRole,
	desired engine.Payload, _ bool) (engine.Payload, error) {
	return writeCloud(ctx, c, a.Path(r), desired)
}
