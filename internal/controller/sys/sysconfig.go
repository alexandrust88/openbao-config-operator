package sys

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// Payload keys used by several sys adapters.
const (
	keyCASRequired   = "cas_required"
	keyForceNoCache  = "force_no_cache"
	keyPolicy        = "policy"
	keyBlockInterval = "block_interval"
)

// requireRootNamespace fails with engine.ErrUnsupported when c is scoped to a child namespace.
func requireRootNamespace(c openbao.Client, kind string) error {
	if ns := strings.Trim(c.Namespace(), "/"); ns != "" && ns != "root" {
		return fmt.Errorf("%s exists only in the root namespace, the connection uses %q: %w", kind, ns, engine.ErrUnsupported)
	}
	return nil
}

// badRequestContains reports whether err is a 400 response carrying msg.
func badRequestContains(err error, msg string) bool {
	var re *openbao.ResponseError
	if !errors.As(err, &re) || re.StatusCode != 400 {
		return false
	}
	return slices.ContainsFunc(re.Errors, func(e string) bool { return strings.Contains(e, msg) })
}

// statusIs reports whether err is a response with the given HTTP status code.
func statusIs(err error, code int) bool {
	var re *openbao.ResponseError
	return errors.As(err, &re) && re.StatusCode == code
}

func nameOrDefault(specName, metaName string) string {
	if specName != "" {
		return specName
	}
	return metaName
}

func stringsOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// writeWithCAS writes desired with a check-and-set value: -1 (must not exist) on create, the stored version on
// update. A server that reports no version gets no cas.
func writeWithCAS(ctx context.Context, c openbao.Client, path string, desired engine.Payload, create bool) (engine.Payload, error) {
	payload := maps.Clone(desired)
	if create {
		payload["cas"] = -1
	} else {
		current, err := c.Read(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if version, ok := current["version"]; ok {
			payload["cas"] = version
		}
	}
	return c.Write(ctx, path, payload)
}
