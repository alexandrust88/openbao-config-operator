package openbao

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"slices"
	"strings"
	"time"

	"github.com/openbao/openbao/api/v2"
)

const (
	maxRetries   = 2
	minRetryWait = 250 * time.Millisecond
	maxRetryWait = 2 * time.Second
	queryTrue    = "true"
)

type client struct {
	s  *session
	ns string
}

var _ Client = (*client)(nil)

func (c *client) Read(ctx context.Context, path string) (map[string]any, error) {
	if err := ValidatePath(path); err != nil {
		return nil, wrapErr("read", path, err)
	}
	path = CanonicalPath(path)
	var data map[string]any
	err := c.s.do(ctx, c.ns, func(ac *api.Client) error {
		secret, err := rawSecret(ctx, ac, ac.NewRequest(http.MethodGet, "/v1/"+path))
		if err != nil {
			return err
		}
		data = secret.Data
		return nil
	})
	if err != nil {
		return nil, wrapErr("read", path, err)
	}
	return data, nil
}

func (c *client) Write(ctx context.Context, path string, data map[string]any) (map[string]any, error) {
	if err := ValidatePath(path); err != nil {
		return nil, wrapErr("write", path, err)
	}
	path = CanonicalPath(path)
	var out map[string]any
	err := c.s.do(ctx, c.ns, func(ac *api.Client) error {
		secret, err := ac.Logical().WriteWithContext(ctx, path, data)
		if err != nil {
			return err
		}
		if secret != nil {
			out = secret.Data
		}
		return nil
	})
	if err != nil {
		return nil, wrapErr("write", path, err)
	}
	return out, nil
}

func (c *client) Delete(ctx context.Context, path string) error {
	if err := ValidatePath(path); err != nil {
		return wrapErr("delete", path, err)
	}
	path = CanonicalPath(path)
	err := c.s.do(ctx, c.ns, func(ac *api.Client) error {
		_, err := ac.Logical().DeleteWithContext(ctx, path)
		return err
	})
	if err != nil {
		return wrapErr("delete", path, err)
	}
	return nil
}

func (c *client) List(ctx context.Context, path string) ([]string, error) {
	if err := ValidatePath(path); err != nil {
		return nil, wrapErr("list", path, err)
	}
	var keys []string
	err := c.s.do(ctx, c.ns, func(ac *api.Client) error {
		r := ac.NewRequest(http.MethodGet, "/v1/"+path)
		r.Params.Set("list", queryTrue)
		secret, err := rawSecret(ctx, ac, r)
		if err != nil {
			return err
		}
		raw, _ := secret.Data["keys"].([]any)
		keys = make([]string, 0, len(raw))
		for _, k := range raw {
			if s, ok := k.(string); ok {
				keys = append(keys, s)
			}
		}
		return nil
	})
	if err != nil {
		return nil, wrapErr("list", path, err)
	}
	return keys, nil
}

// rawSecret sends r and parses the response. Unlike api.Logical, a 404 keeps OpenBao's error strings, so a
// missing namespace stays distinguishable, and a 404 that carries warnings or data is still not found.
func rawSecret(ctx context.Context, ac *api.Client, r *api.Request) (*api.Secret, error) {
	if timeout := ac.ClientTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	resp, err := ac.RawRequestWithContext(ctx, r)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		if re, ok := errors.AsType[*ResponseError](convertErr(err)); ok && re.StatusCode == http.StatusNotFound && resp != nil {
			if secret, perr := api.ParseSecret(resp.Body); perr == nil && secret != nil && secret.Data != nil {
				return nil, &NotFoundDataError{ResponseError: re, Data: secret.Data}
			}
		}
		return nil, err
	}
	secret, err := api.ParseSecret(resp.Body)
	if errors.Is(err, io.EOF) || (err == nil && secret == nil) {
		return nil, &ResponseError{StatusCode: http.StatusNotFound}
	}
	return secret, err
}

// NotFoundDataError is a 404 whose body still carried data, such as the version metadata of a KV version 2
// secret whose latest version is deleted. It matches ErrNotFound like any other 404; Error never prints Data.
type NotFoundDataError struct {
	*ResponseError
	Data map[string]any
}

func (e *NotFoundDataError) Unwrap() error { return e.ResponseError }

func (c *client) WithNamespace(ns string) Client {
	return &client{s: c.s, ns: joinNamespace(c.ns, ns)}
}

func (c *client) Namespace() string {
	return c.ns
}

// joinNamespace appends rel to base without resolving "." or "..", so a relative namespace can never
// climb out of the base; the server rejects such segments as invalid names.
func joinNamespace(base, rel string) string {
	var parts []string
	for p := range strings.SplitSeq(base+"/"+rel, "/") {
		switch p {
		case "", ".":
		case "..":
			// OpenBao cleans the namespace header, so ".." would climb out of base; fail closed instead.
			parts = append(parts, "invalid..segment")
		default:
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// ValidateNamespace rejects namespace paths with empty, "." or ".." segments (so also a leading or trailing "/")
// or a leading "root" segment.
func ValidateNamespace(ns string) error {
	if ns == "" {
		return nil
	}
	for i, p := range strings.Split(ns, "/") {
		if p == "" || p == "." || p == ".." || (i == 0 && p == "root") {
			return fmt.Errorf("invalid namespace %q: segment %q is not allowed", ns, p)
		}
	}
	return nil
}

// ErrUncleanPath is returned for a request path with an empty, "." or ".." segment. The api client would clean it
// before sending, so the request would reach another path than the one the operator checked and indexed.
var ErrUncleanPath = errors.New(`path must not contain empty, "." or ".." segments`)

// ValidatePath refuses a path that path.Clean would change; one trailing slash, as in a list path, is allowed.
func ValidatePath(p string) error {
	trimmed := strings.TrimSuffix(p, "/")
	if trimmed == "" {
		return nil
	}
	if pathpkg.Clean(trimmed) != trimmed || slices.ContainsFunc(strings.Split(trimmed, "/"), func(s string) bool {
		return s == "." || s == ".."
	}) {
		return fmt.Errorf("%w: %q", ErrUncleanPath, p)
	}
	return nil
}

// CanonicalPath returns the one spelling of a validated path that request paths, status.path, the ownership index
// and the create lock share: without leading or trailing slashes, which OpenBao ignores.
func CanonicalPath(p string) string {
	return strings.Trim(p, "/")
}

func wrapErr(op, path string, err error) error {
	return fmt.Errorf("%s %s: %w", op, path, err)
}

// convertErr replaces the api's ResponseError, whose message carries the request URL and, when unparsable,
// the raw response body, with ResponseError.
func convertErr(err error) error {
	var re *api.ResponseError
	if !errors.As(err, &re) {
		return err
	}
	out := &ResponseError{StatusCode: re.StatusCode}
	if !re.RawError {
		out.Errors = re.Errors
	}
	return out
}

func statusCode(err error) int {
	if re, ok := errors.AsType[*ResponseError](err); ok {
		return re.StatusCode
	}
	return 0
}

// normalizeAddress validates an OpenBao address and strips trailing slashes. A path prefix is allowed for
// servers behind a reverse proxy, but the address must not include the /v1 API prefix.
func normalizeAddress(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse address: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("address %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("address %q: host is empty", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("address %q: must not have a query or fragment", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "/v1" || strings.HasSuffix(u.Path, "/v1") {
		return "", fmt.Errorf("address %q: must not include /v1", raw)
	}
	return u.String(), nil
}

// errRedirect is returned for a 3xx response. Following it would re-send the token and the body to the host it
// names, and the api client would otherwise read a 3xx as an empty success.
var errRedirect = errors.New("OpenBao answered with a redirect, which the operator never follows")

func refuseRedirects(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if err == nil && resp != nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return false, fmt.Errorf("%w (status %d)", errRedirect, resp.StatusCode)
	}
	return api.DefaultRetryPolicy(ctx, resp, err)
}

// newAPIClient builds a token-less api client. When connection is not empty the transport is instrumented
// with that connection label.
func newAPIClient(cfg Config, connection string) (*api.Client, *http.Client, error) {
	addr, err := normalizeAddress(cfg.Address)
	if err != nil {
		return nil, nil, err
	}
	ac := api.NewConfig()
	if ac.Error != nil {
		return nil, nil, fmt.Errorf("api config: %w", ac.Error)
	}
	// NewConfig reads no BAO_*/VAULT_* variable as long as DisableEnvironment stays set; NewClient would otherwise
	// apply DefaultConfig, including BAO_AGENT_ADDR, which replaces Address.
	ac.DisableEnvironment = true
	ac.AgentAddress = ""
	ac.DisableRedirects = true
	ac.CheckRetry = refuseRedirects
	ac.Address = addr
	ac.MaxRetries = maxRetries
	ac.MinRetryWait = minRetryWait
	ac.MaxRetryWait = maxRetryWait
	if cfg.Timeout > 0 {
		ac.Timeout = cfg.Timeout
	}

	tr, ok := ac.HttpClient.Transport.(*http.Transport)
	if !ok {
		return nil, nil, errors.New("api config: unexpected transport type")
	}
	// cleanhttp honours HTTP(S)_PROXY; only the OpenBaoServer spec may decide where tokens are sent.
	tr.Proxy = nil
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if len(cfg.CABundle) > 0 {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cfg.CABundle) {
			return nil, nil, errors.New("ca bundle contains no valid PEM certificate")
		}
		tr.TLSClientConfig.RootCAs = roots
	}
	if connection != "" {
		ac.HttpClient.Transport = &instrumentedTransport{next: tr, connection: connection}
	}

	c, err := api.NewClient(ac)
	if err != nil {
		return nil, nil, fmt.Errorf("api client: %w", err)
	}
	c.ClearNamespace()
	c.ClearToken()
	return c, ac.HttpClient, nil
}
