package auth

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// JWT auth keys shared by the role and CEL role adapters.
const (
	keyExpirationLeeway = "expiration_leeway"
	keyNotBeforeLeeway  = "not_before_leeway"
	keyClockSkewLeeway  = "clock_skew_leeway"
	keyBoundAudiences   = "bound_audiences"
)

// builtinList converts values for an OpenBao comma-string-slice field, which trims each element on write;
// sending them trimmed keeps a PEM block that ends in a newline from reading back as drift.
func builtinList[S ~string](values []S) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.TrimSpace(string(v))
	}
	return out
}

func builtinBool(v *bool) bool {
	return v != nil && *v
}

// leewaySeconds converts a leeway (integer seconds, a duration string, or -1) to signed seconds.
func leewaySeconds(v intstr.IntOrString) (int64, error) {
	if v.Type == intstr.Int {
		return int64(v.IntVal), nil
	}
	if n, err := strconv.ParseInt(v.StrVal, 10, 64); err == nil {
		return n, nil
	}
	d, err := time.ParseDuration(v.StrVal)
	if err != nil {
		return 0, fmt.Errorf("invalid leeway %q: %w", v.StrVal, err)
	}
	return int64(d / time.Second), nil
}

// putLeeways adds each set leeway, or the given default when def is non-nil and the leeway is unset.
func putLeeways(p engine.Payload, l authv1alpha1.JWTLeeways, def map[string]int64) error {
	for key, v := range map[string]*intstr.IntOrString{
		keyExpirationLeeway: l.ExpirationLeeway,
		keyNotBeforeLeeway:  l.NotBeforeLeeway,
		keyClockSkewLeeway:  l.ClockSkewLeeway,
	} {
		if v == nil {
			if d, ok := def[key]; ok {
				p[key] = d
			}
			continue
		}
		seconds, err := leewaySeconds(*v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		p[key] = seconds
	}
	return nil
}

// readWithDefaults reads path and fills each key of defaults that the read omits, for fields OpenBao returns only
// when they differ from their default.
func readWithDefaults(ctx context.Context, c openbao.Client, path string, defaults engine.Payload) (engine.Payload, bool, error) {
	observed, err := c.Read(ctx, path)
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	putDefaults(observed, defaults)
	return observed, true, nil
}

// writeWithout writes desired to path without the compare-only keys, which OpenBao reads but does not accept.
func writeWithout(ctx context.Context, c openbao.Client, path string, desired engine.Payload, compareOnly ...string) (engine.Payload, error) {
	payload := maps.Clone(desired)
	for _, k := range compareOnly {
		delete(payload, k)
	}
	return c.Write(ctx, path, payload)
}
