package openbao

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/openbao/openbao/api/v2"
)

// Server flavors reported in ServerInfo.Flavor.
const (
	FlavorOpenBao = "OpenBao"
	FlavorVault   = "Vault"
)

// Probe reads sys/seal-status and sys/health in the root namespace without a token and reports what kind of
// server is behind cfg. cfg.Auth and cfg.Namespace are not used.
func Probe(ctx context.Context, cfg Config) (ServerInfo, error) {
	ac, httpc, err := newAPIClient(cfg, "")
	if err != nil {
		return ServerInfo{}, err
	}
	defer httpc.CloseIdleConnections()

	seal, err := getJSON(ctx, ac, "sys/seal-status", nil)
	if err != nil {
		return ServerInfo{}, err
	}
	health, err := getJSON(ctx, ac, "sys/health", url.Values{
		"standbyok":     {queryTrue},
		"perfstandbyok": {queryTrue},
		"sealedcode":    {"200"},
		"uninitcode":    {"200"},
	})
	if err != nil {
		return ServerInfo{}, err
	}

	version, _ := seal["version"].(string)
	sealed, _ := seal["sealed"].(bool)
	initialized, _ := seal["initialized"].(bool)
	standby, _ := health["standby"].(bool)
	return ServerInfo{
		Version:     version,
		Flavor:      flavor(seal, health),
		Sealed:      sealed,
		Standby:     standby,
		Initialized: initialized,
	}, nil
}

// flavor relies on fields only one server emits: OpenBao 2.6+ renamed build_date to commit_date in
// seal-status, and OpenBao dropped the performance_standby and enterprise fields from sys/health.
func flavor(seal, health map[string]any) string {
	if _, ok := seal["commit_date"]; ok {
		return FlavorOpenBao
	}
	_, perfStandby := health["performance_standby"]
	_, enterprise := health["enterprise"]
	if perfStandby || enterprise {
		return FlavorVault
	}
	return FlavorOpenBao
}

func getJSON(ctx context.Context, ac *api.Client, path string, params url.Values) (map[string]any, error) {
	r := ac.NewRequest(http.MethodGet, "/v1/"+path)
	if params != nil {
		r.Params = params
	}
	resp, err := ac.RawRequestWithContext(ctx, r)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", path, convertErr(err))
	}
	var out map[string]any
	if err := resp.DecodeJSON(&out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return out, nil
}
