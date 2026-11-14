package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// readCloud reads path and reports a 404 as absent.
func readCloud(ctx context.Context, c openbao.Client, path string) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if observed == nil {
		observed = engine.Payload{}
	}
	return observed, true, nil
}

// writeCloud writes payload to path. OpenBao answers 404 or "no handler for route" when the plugin or its mount is
// missing; that is reported as engine.ErrUnsupported so the resource waits instead of failing with backoff.
func writeCloud(ctx context.Context, c openbao.Client, path string, payload engine.Payload) (engine.Payload, error) {
	if _, err := c.Write(ctx, path, payload); err != nil {
		return nil, engine.UnsupportedIfMountMissing(fmt.Errorf("write %s: %w", path, err))
	}
	return nil, nil
}

func putCloudMap(p engine.Payload, key string, value map[string]string) {
	if value != nil {
		p[key] = value
	}
}
