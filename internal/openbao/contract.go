// Package openbao builds authenticated OpenBao clients and caches one per connection.
package openbao

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrNotFound is returned by Read and Delete when the path does not exist (HTTP 404).
var ErrNotFound = errors.New("openbao: not found")

// ErrNamespaceNotFound matches a 404 whose error is OpenBao's "namespace not found": the request's namespace
// does not exist (yet). It also matches ErrNotFound.
var ErrNamespaceNotFound = errors.New("openbao: namespace not found")

// ErrPermissionDenied wraps a 403 for a token that is still valid: the policy denies the request, so logging in
// again would not help.
var ErrPermissionDenied = errors.New("openbao: permission denied")

// ResponseError is a non-2xx response from OpenBao. Errors holds only OpenBao's error strings, never a raw
// body. A 404 matches ErrNotFound through errors.Is.
type ResponseError struct {
	StatusCode int
	Errors     []string
}

func (e *ResponseError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("openbao: status %d", e.StatusCode)
	}
	return fmt.Sprintf("openbao: status %d: %s", e.StatusCode, strings.Join(e.Errors, "; "))
}

// Is reports whether a 404 response is compared with ErrNotFound, or a missing namespace with
// ErrNamespaceNotFound.
func (e *ResponseError) Is(target error) bool {
	if e.StatusCode != 404 {
		return false
	}
	switch target {
	case ErrNotFound:
		return true
	case ErrNamespaceNotFound:
		return slices.Contains(e.Errors, "namespace not found")
	}
	return false
}

// Client is the narrow API the engine and adapters use. Implementations are safe for concurrent use.
type Client interface {
	// Read returns the "data" of the response, or ErrNotFound.
	Read(ctx context.Context, path string) (map[string]any, error)
	// Write sends data to path and returns the response "data", which may be nil.
	Write(ctx context.Context, path string, data map[string]any) (map[string]any, error)
	// Delete removes path. A missing path returns ErrNotFound.
	Delete(ctx context.Context, path string) error
	// List returns the "keys" of a LIST request, or ErrNotFound.
	List(ctx context.Context, path string) ([]string, error)
	// WithNamespace returns a client scoped to ns, relative to this client's namespace.
	WithNamespace(ns string) Client
	// Namespace returns the absolute namespace of this client.
	Namespace() string
}

// Config is the fully resolved, Kubernetes-free description of how to reach and log in to OpenBao.
type Config struct {
	Address   string
	Namespace string
	// AuthNamespace holds the auth mount; login, lookup-self and renew-self run there. Empty is the root.
	AuthNamespace string
	CABundle      []byte
	Timeout       time.Duration
	Auth          Auth
	// RateLimit is shared by every connection to the same address.
	RateLimit RateLimit
}

// RateLimit bounds the requests sent to one server. A zero QPS means no limit.
type RateLimit struct {
	QPS   float64
	Burst int
}

// Auth logs in and returns a token. Credential material is fetched inside Login so rotation needs no restart.
type Auth interface {
	// Login returns a client token and its TTL. A zero TTL means the token does not expire.
	Login(ctx context.Context, c LoginClient) (token string, ttl time.Duration, renewable bool, err error)
	// CacheKey identifies the credential inputs; a change invalidates the cached client.
	CacheKey() string
}

// LoginClient is the unauthenticated client handed to Auth.Login.
// Write returns the response "auth" block (client_token, accessor, lease_duration, renewable, policies)
// when the response carries one, and the "data" block otherwise.
type LoginClient interface {
	Write(ctx context.Context, path string, data map[string]any) (map[string]any, error)
}

// ServerInfo describes the server behind a connection.
type ServerInfo struct {
	Version     string
	Flavor      string // "OpenBao" or "Vault"
	Sealed      bool
	Standby     bool
	Initialized bool
}
