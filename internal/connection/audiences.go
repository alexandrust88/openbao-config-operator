package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

const issuerDiscoveryPath = "/.well-known/openid-configuration"

// builtinAPIServerAudiences are audiences that API servers commonly accept without any flag: the in-cluster
// service names and the defaults of rke2 and k3s. They are denied even when discovery fails.
var builtinAPIServerAudiences = []string{
	"https://kubernetes.default.svc",
	"https://kubernetes.default.svc.cluster.local",
	"kubernetes.default.svc",
	"rke2",
	"k3s",
}

// APIServerAudiences returns the built-in API server audiences, configured, and the service account issuer the API
// server advertises, which is also its default audience. On a discovery error it returns the built-in and
// configured audiences together with the error, so the caller can log it and continue with a list that never
// shrinks.
func APIServerAudiences(ctx context.Context, cfg *rest.Config, configured []string) ([]string, error) {
	audiences := slices.Clone(builtinAPIServerAudiences)
	for _, audience := range configured {
		if !slices.Contains(audiences, audience) {
			audiences = append(audiences, audience)
		}
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return audiences, fmt.Errorf("discover service account issuer: %w", err)
	}
	body, err := dc.RESTClient().Get().AbsPath(issuerDiscoveryPath).DoRaw(ctx)
	if err != nil {
		return audiences, fmt.Errorf("discover service account issuer: %w", err)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return audiences, fmt.Errorf("decode %s: %w", issuerDiscoveryPath, err)
	}
	if doc.Issuer != "" && !slices.Contains(audiences, doc.Issuer) {
		audiences = append(audiences, doc.Issuer)
	}
	return audiences, nil
}
