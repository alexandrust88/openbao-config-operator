package auth

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// authMount returns the auth mount path without slashes, or fallback when it is unset.
func authMount(mountPath, fallback string) string {
	if mountPath = strings.Trim(mountPath, "/"); mountPath != "" {
		return mountPath
	}
	return fallback
}

func authName(specName, metaName string) string {
	if specName != "" {
		return specName
	}
	return metaName
}

func authSecret(ctx context.Context, r engine.Resolver, namespace string, ref commonv1alpha1.SecretKeySelector) (string, error) {
	value, err := r.SecretValue(ctx, namespace, ref)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func putString(p engine.Payload, key, v string) {
	if v != "" {
		p[key] = v
	}
}

func putList(p engine.Payload, key string, v []string) {
	if v != nil {
		p[key] = v
	}
}

func credPutInt32(p engine.Payload, key string, v *int32) {
	if v != nil {
		p[key] = int64(*v)
	}
}

// credInt64 reads a JSON number as decoded by the OpenBao client.
func credInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	}
	return 0, false
}
