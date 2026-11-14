package secrets

import (
	"context"
	"fmt"
	"maps"

	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// engineStubResolver serves Secret values from a map keyed by "<secret>/<key>".
type engineStubResolver map[string]string

func (r engineStubResolver) SecretValue(_ context.Context, _ string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	value, ok := r[ref.Name+"/"+ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s: %w", ref.Name, engine.ErrReferenceNotReady)
	}
	return []byte(value), nil
}

func (engineStubResolver) Object(context.Context, client.ObjectKey, client.Object) error { return nil }

type engineStubCall struct {
	op   string
	path string
	data map[string]any
}

// engineStubClient serves reads from data or errs and records every write and delete.
type engineStubClient struct {
	openbao.Client
	data      map[string]map[string]any
	errs      map[string]error
	responses map[string]map[string]any
	calls     []engineStubCall
}

func (c *engineStubClient) Read(_ context.Context, path string) (map[string]any, error) {
	if err := c.errs[path]; err != nil {
		return nil, err
	}
	data, ok := c.data[path]
	if !ok {
		return nil, openbao.ErrNotFound
	}
	return maps.Clone(data), nil
}

func (c *engineStubClient) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, engineStubCall{op: "write", path: path, data: data})
	return c.responses[path], nil
}

func (c *engineStubClient) Delete(_ context.Context, path string) error {
	c.calls = append(c.calls, engineStubCall{op: "delete", path: path})
	return nil
}
