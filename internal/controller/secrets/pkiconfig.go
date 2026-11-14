package secrets

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=secrets.openbaoconfig.io,resources=pkiissuers,verbs=get;list;watch

// SetupPKIConfig registers the PKIConfig controller with the manager.
func SetupPKIConfig(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*secretsv1alpha1.PKIConfig](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("pkiconfig-controller"),
		conns,
		PKIConfigAdapter{},
		func() *secretsv1alpha1.PKIConfig { return &secretsv1alpha1.PKIConfig{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// PKIConfigAdapter maps a PKIConfig onto the <mount>/config/{cluster,urls,crl,issuers} singletons. The payload
// is flat with keys "<section>.<key>", so the engine compares every section in one pass and the duration keys
// can be listed in the FieldSpec.
type PKIConfigAdapter struct{}

var (
	_ engine.Adapter[*secretsv1alpha1.PKIConfig]  = PKIConfigAdapter{}
	_ engine.Observer[*secretsv1alpha1.PKIConfig] = PKIConfigAdapter{}
	_ engine.Writer[*secretsv1alpha1.PKIConfig]   = PKIConfigAdapter{}
	_ engine.Singleton                            = PKIConfigAdapter{}
)

// pkiConfigSections is the write order: templated URLs and ACME need the cluster path first.
var pkiConfigSections = []string{"cluster", "urls", "crl", "issuers"}

const pkiDefaultIssuerKey = "issuers.default"

// Path implements engine.Adapter.
func (PKIConfigAdapter) Path(c *secretsv1alpha1.PKIConfig) string {
	return pkiMount(c.Spec.MountPath) + "/config"
}

// Desired implements engine.Adapter. The urls and crl sections are always sent, every unset field at its OpenBao
// default. The cluster fields and issuers are sent only when set: OpenBao rejects an empty cluster path and the
// default issuer has no default to return to.
func (PKIConfigAdapter) Desired(ctx context.Context, r engine.Resolver, c *secretsv1alpha1.PKIConfig) (engine.Payload, error) {
	s := &c.Spec
	p := engine.Payload{}
	u := ptr.Deref(s.URLs, secretsv1alpha1.PKIURLsConfig{})
	orList(p, "urls.issuing_certificates", u.IssuingCertificates)
	orList(p, "urls.crl_distribution_points", u.CRLDistributionPoints)
	orList(p, "urls.delta_crl_distribution_points", u.DeltaCRLDistributionPoints)
	orList(p, "urls.ocsp_servers", u.OCSPServers)
	orFalse(p, "urls.enable_templating", u.EnableTemplating)
	crl := ptr.Deref(s.CRL, secretsv1alpha1.PKICRLConfig{})
	pkiDurationString(p, "crl.expiry", crl.Expiry, "72h")
	orFalse(p, "crl.disable", crl.Disable)
	orFalse(p, "crl.ocsp_disable", crl.OCSPDisable)
	pkiDurationString(p, "crl.ocsp_expiry", crl.OCSPExpiry, "12h")
	orFalse(p, "crl.auto_rebuild", crl.AutoRebuild)
	pkiDurationString(p, "crl.auto_rebuild_grace_period", crl.AutoRebuildGracePeriod, "12h")
	orFalse(p, "crl.enable_delta", crl.EnableDelta)
	pkiDurationString(p, "crl.delta_rebuild_interval", crl.DeltaRebuildInterval, "15m")
	orFalse(p, "crl.allow_expired_cert_revocation", crl.AllowExpiredCertRevocation)
	if cluster := s.Cluster; cluster != nil {
		optString(p, "cluster.path", cluster.Path)
		optString(p, "cluster.aia_path", cluster.AIAPath)
	}
	if issuers := s.Issuers; issuers != nil {
		ref, err := pkiIssuerRef(ctx, r, c, &s.ResourceSpec, pkiMount(s.MountPath), issuers.Default)
		if err != nil {
			return nil, err
		}
		p[pkiDefaultIssuerKey] = ref
		orFalse(p, "issuers.default_follows_latest_issuer", issuers.DefaultFollowsLatestIssuer)
	}
	return p, nil
}

// pkiDurationString sets a duration that OpenBao stores as a string to the spec value or, when unset, to def.
func pkiDurationString(p engine.Payload, key string, d *metav1.Duration, def string) {
	p[key] = def
	pkiOptDurationString(p, key, d)
}

// Fields implements engine.Adapter.
func (PKIConfigAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{
		"crl.expiry", "crl.ocsp_expiry", "crl.auto_rebuild_grace_period", "crl.delta_rebuild_interval",
	}}
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (PKIConfigAdapter) Singleton() bool { return true }

// Observe implements engine.Observer. It reads issuers only when the spec sets it. The default issuer is read
// as an ID; when the spec names it, the name is reported if it resolves to that ID.
func (PKIConfigAdapter) Observe(ctx context.Context, c openbao.Client, cfg *secretsv1alpha1.PKIConfig) (engine.Payload, bool, error) {
	mount := pkiMount(cfg.Spec.MountPath)
	observed := engine.Payload{}
	for _, section := range pkiConfigSectionsOf(cfg) {
		path := mount + "/config/" + section
		data, err := c.Read(ctx, path)
		if err != nil {
			return nil, false, fmt.Errorf("read %s: %w", path, err)
		}
		for key, value := range data {
			observed[section+"."+key] = value
		}
	}
	if issuers := cfg.Spec.Issuers; issuers != nil && issuers.Default.IssuerName != "" {
		name := issuers.Default.IssuerName
		if current := pkiString(observed[pkiDefaultIssuerKey]); current != "" && current != name {
			issuer, found, err := pkiReadRef(ctx, c, mount, "issuer", name)
			if err != nil {
				return nil, false, err
			}
			if found && pkiString(issuer["issuer_id"]) == current {
				observed[pkiDefaultIssuerKey] = name
			}
		}
	}
	return observed, true, nil
}

// Write implements engine.Writer: one write per section that differs from the server. A default issuer that does
// not exist yet is reported before any write, so that the retries until it appears rewrite nothing.
func (a PKIConfigAdapter) Write(ctx context.Context, c openbao.Client, cfg *secretsv1alpha1.PKIConfig, desired engine.Payload,
	_ bool) (engine.Payload, error) {
	mount := pkiMount(cfg.Spec.MountPath)
	if ref := pkiString(desired[pkiDefaultIssuerKey]); ref != "" {
		_, found, err := pkiReadRef(ctx, c, mount, "issuer", ref)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("default issuer %q does not exist in mount %s: %w", ref, mount, engine.ErrReferenceNotReady)
		}
	}
	observed, _, err := a.Observe(ctx, c, cfg)
	if err != nil {
		return nil, err
	}
	wrote := false
	for _, section := range pkiConfigSections {
		body, wanted := engine.Payload{}, engine.Payload{}
		for key, value := range desired {
			if name, ok := strings.CutPrefix(key, section+"."); ok {
				body[name], wanted[key] = value, value
			}
		}
		if len(engine.Diff(wanted, observed, a.Fields())) == 0 {
			continue
		}
		path := mount + "/config/" + section
		if _, err := c.Write(ctx, path, body); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		wrote = true
	}
	if !wrote {
		return nil, engine.ErrNothingWritten
	}
	return nil, nil
}

func pkiConfigSectionsOf(cfg *secretsv1alpha1.PKIConfig) []string {
	if cfg.Spec.Issuers != nil {
		return pkiConfigSections
	}
	return pkiConfigSections[:3]
}
