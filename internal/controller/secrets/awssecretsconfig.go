package secrets

import (
	"context"
	"maps"

	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=awssecretsconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=awssecretsconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=awssecretsconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupAWSSecretsConfig registers the AWSSecretsConfig controller with the manager.
func SetupAWSSecretsConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	return setupPlugin(mgr, conns, opts, "awssecretsconfig", AWSSecretsConfigAdapter{},
		func() *secretsv1alpha1.AWSSecretsConfig { return &secretsv1alpha1.AWSSecretsConfig{} })
}

// AWSSecretsConfigAdapter maps an AWSSecretsConfig onto <mountPath>/config/root and <mountPath>/config/lease.
// Desired and Observe use one flat payload; lease and lease_max live on the lease path.
type AWSSecretsConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.AWSSecretsConfig]          = AWSSecretsConfigAdapter{}
	_ engine.Observer[*secretsv1alpha1.AWSSecretsConfig]         = AWSSecretsConfigAdapter{}
	_ engine.Writer[*secretsv1alpha1.AWSSecretsConfig]           = AWSSecretsConfigAdapter{}
	_ engine.SecretReferencer[*secretsv1alpha1.AWSSecretsConfig] = AWSSecretsConfigAdapter{}
)

var awsLeaseKeys = []string{"lease", "lease_max"}

// Path implements engine.Adapter.
func (AWSSecretsConfigAdapter) Path(c *secretsv1alpha1.AWSSecretsConfig) string {
	return awsSecretsMount(c.Spec.MountPath) + "/config/root"
}

// Desired implements engine.Adapter. lease and lease_max are Go duration strings because the plugin parses them
// with time.ParseDuration.
func (AWSSecretsConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.AWSSecretsConfig) (engine.Payload, error) {
	s := c.Spec
	p := engine.Payload{}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "access_key", s.AccessKeySecretRef); err != nil {
		return nil, err
	}
	if err := putPluginSecret(ctx, r, c.Namespace, p, "secret_key", s.SecretKeySecretRef); err != nil {
		return nil, err
	}
	optString(p, "region", s.Region)
	optString(p, "iam_endpoint", s.IAMEndpoint)
	optString(p, "sts_endpoint", s.STSEndpoint)
	optString(p, "username_template", s.UsernameTemplate)
	if s.MaxRetries != nil {
		p["max_retries"] = int64(*s.MaxRetries)
	}
	if s.Lease != nil {
		p["lease"] = s.Lease.Duration.String()
	}
	if s.LeaseMax != nil {
		p["lease_max"] = s.LeaseMax.Duration.String()
	}
	return p, nil
}

// Fields implements engine.Adapter.
func (AWSSecretsConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{
		WriteOnly: []string{"access_key", "secret_key"},
		Durations: awsLeaseKeys,
	}
}

// Observe implements engine.Observer: config/root decides existence, config/lease adds lease and lease_max.
func (a AWSSecretsConfigAdapter) Observe(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.AWSSecretsConfig) (engine.Payload, bool, error) {
	observed, found, err := readCloud(ctx, c, a.Path(obj))
	if !found || err != nil {
		return nil, false, err
	}
	lease, leaseFound, err := readCloud(ctx, c, awsSecretsMount(obj.Spec.MountPath)+"/config/lease")
	if err != nil {
		return nil, false, err
	}
	if leaseFound {
		for _, key := range awsLeaseKeys {
			if v, ok := lease[key]; ok {
				observed[key] = v
			}
		}
	}
	return observed, true, nil
}

// Write implements engine.Writer. config/root is replaced as a whole, so it is always written with every key.
func (a AWSSecretsConfigAdapter) Write(ctx context.Context, c openbao.Client, obj *secretsv1alpha1.AWSSecretsConfig, desired engine.Payload, _ bool) (engine.Payload, error) {
	root := maps.Clone(desired)
	lease := engine.Payload{}
	for _, key := range awsLeaseKeys {
		if v, ok := root[key]; ok {
			lease[key] = v
			delete(root, key)
		}
	}
	if _, err := writeCloud(ctx, c, a.Path(obj), root); err != nil || len(lease) == 0 {
		return nil, err
	}
	return writeCloud(ctx, c, awsSecretsMount(obj.Spec.MountPath)+"/config/lease", lease)
}

// SecretRefs implements engine.SecretReferencer.
func (AWSSecretsConfigAdapter) SecretRefs(c *secretsv1alpha1.AWSSecretsConfig) []string {
	return pluginSecretNames(c.Spec.AccessKeySecretRef, c.Spec.SecretKeySecretRef)
}

func awsSecretsMount(mountPath string) string { return pluginMountPath(mountPath, "aws") }
