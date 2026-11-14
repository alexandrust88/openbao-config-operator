package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// ErrAuthMountMissing is returned when the auth method an external-plugin kind configures is not enabled. It wraps
// engine.ErrUnsupported, so the engine reports the reason Unsupported and retries without error backoff.
var ErrAuthMountMissing = fmt.Errorf("auth method not enabled: %w", engine.ErrUnsupported)

// observeExternalAuth reads path and, when it is absent, checks that the auth method at auth/<mount> exists, so a
// missing mount or an unregistered plugin surfaces as a clear error instead of a failing create.
func observeExternalAuth(ctx context.Context, c openbao.Client, path, mount, pluginType string) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, checkAuthMount(ctx, c, mount, pluginType)
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return observed, true, nil
}

// checkAuthMount reports ErrAuthMountMissing when sys/auth lists no auth/<mount>. A token that cannot read
// sys/auth skips the check; the write then reports the problem.
func checkAuthMount(ctx context.Context, c openbao.Client, mount, pluginType string) error {
	mounts, err := c.Read(ctx, "sys/auth")
	if err != nil {
		return nil
	}
	if _, ok := mounts[mount+"/"]; ok {
		return nil
	}
	return fmt.Errorf("%w: no auth method at auth/%s; register the %s plugin and enable it there",
		ErrAuthMountMissing, mount, pluginType)
}

// putCloudSecret resolves ref into p[key]; a missing Secret wraps engine.ErrReferenceNotReady.
func putCloudSecret(ctx context.Context, r engine.Resolver, p engine.Payload, namespace, key string,
	ref *commonv1alpha1.SecretKeySelector) error {
	if ref == nil {
		return nil
	}
	value, err := authSecret(ctx, r, namespace, *ref)
	if err != nil {
		return err
	}
	p[key] = value
	return nil
}

func cloudSecretNames(refs ...*commonv1alpha1.SecretKeySelector) []string {
	var names []string
	for _, ref := range refs {
		if ref != nil {
			names = append(names, ref.Name)
		}
	}
	return names
}

// cloudNanosToSeconds rewrites the keys of observed that the server reads back as a time.Duration in nanoseconds
// into whole seconds, the unit they are written in.
func cloudNanosToSeconds(observed engine.Payload, keys ...string) {
	for _, key := range keys {
		var nanos float64
		switch v := observed[key].(type) {
		case json.Number:
			n, err := v.Float64()
			if err != nil {
				continue
			}
			nanos = n
		case float64:
			nanos = v
		case int64:
			nanos = float64(v)
		case int:
			nanos = float64(v)
		default:
			continue
		}
		observed[key] = int64(time.Duration(nanos) / time.Second)
	}
}
