// The fixture is an external plugin process used only by the live test harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
	"github.com/openbao/openbao/sdk/v2/plugin"
)

func main() {
	// The full example exercises catalog arguments; the SDK establishes mTLS itself.
	flag.Bool("tls-skip-verify", false, "retained for the catalog argument fixture")
	flag.Parse()
	if err := plugin.Serve(&plugin.ServeOpts{BackendFactoryFunc: factory}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func factory(ctx context.Context, config *logical.BackendConfig) (logical.Backend, error) {
	kind := logical.TypeLogical
	name := "secret"
	if strings.Contains(filepath.Base(os.Args[0]), "-auth-") {
		kind, name = logical.TypeCredential, "auth"
	}
	backend := &framework.Backend{
		BackendType: kind,
		Paths: []*framework.Path{{
			Pattern: "health",
			Callbacks: map[logical.Operation]framework.OperationFunc{
				logical.ReadOperation: func(context.Context, *logical.Request, *framework.FieldData) (*logical.Response, error) {
					return &logical.Response{Data: map[string]any{"fixture": "livekit", "type": name}}, nil
				},
			},
		}},
	}
	if err := backend.Setup(ctx, config); err != nil {
		return nil, err
	}
	return backend, nil
}
