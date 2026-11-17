/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command transit demonstrates a workload using operator-configured OpenBao Transit.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	maxBody   = 1 << 20
	opEncrypt = "encrypt"
	opDecrypt = "decrypt"
	opRewrap  = "rewrap"
	opMigrate = "migrate-key"
	opSmoke   = "smoke"
)

var segment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type config struct{ address, namespace, authMount, role, mount, caFile, jwtFile string }
type client struct {
	http                      *http.Client
	address, namespace, token string
}
type input struct {
	Plaintext  string `json:"plaintext,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
}
type output struct {
	Plaintext  string `json:"plaintext,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
	OK         bool   `json:"ok,omitempty"`
}
type response struct {
	Data input `json:"data"`
	Auth struct {
		Token string `json:"client_token"`
	} `json:"auth"`
}
type statusError int

func (s statusError) Error() string { return fmt.Sprintf("OpenBao returned HTTP %d", int(s)) }

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func environment() config {
	return config{address: os.Getenv("BAO_ADDR"), namespace: os.Getenv("BAO_NAMESPACE"),
		authMount: env("BAO_AUTH_MOUNT", "kubernetes"), role: os.Getenv("BAO_AUTH_ROLE"),
		mount:  env("BAO_TRANSIT_MOUNT", "transit"),
		caFile: os.Getenv("BAO_CA_FILE"), jwtFile: env("BAO_JWT_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/token")}
}
func validPath(value string, empty bool) bool {
	if value == "" {
		return empty
	}
	for part := range strings.SplitSeq(value, "/") {
		if !segment.MatchString(part) {
			return false
		}
	}
	return true
}
func newClient(cfg config) (*client, error) {
	address, err := url.Parse(cfg.address)
	if err != nil || address.Scheme != "https" || address.Host == "" || address.User != nil ||
		address.RawQuery != "" || address.Fragment != "" || (address.Path != "" && address.Path != "/") {
		return nil, errors.New("BAO_ADDR must be an HTTPS origin without credentials, query or path")
	}
	if !validPath(cfg.namespace, true) || !validPath(cfg.authMount, false) ||
		!validPath(cfg.mount, false) || !segment.MatchString(cfg.role) {
		return nil, errors.New("invalid namespace, auth mount, Transit mount or role")
	}
	ca, err := os.ReadFile(cfg.caFile)
	if err != nil {
		return nil, errors.New("cannot read mounted OpenBao CA")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid mounted OpenBao CA")
	}
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
	}
	return &client{address: strings.TrimRight(cfg.address, "/") + "/v1/", namespace: cfg.namespace,
		http: &http.Client{Transport: transport, Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}}, nil
}
func (c *client) request(ctx context.Context, namespace, method, path string, body any) (response, error) {
	var result response
	encoded, err := json.Marshal(body)
	if err != nil {
		return result, errors.New("cannot encode request")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.address+path, bytes.NewReader(encoded))
	if err != nil {
		return result, errors.New("cannot construct request")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-Vault-Token", c.token)
	}
	if namespace != "" {
		req.Header.Set("X-Vault-Namespace", namespace)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return result, errors.New("OpenBao request failed")
	}
	defer func() { _ = resp.Body.Close() }() // response payloads are never included in errors
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, statusError(resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return result, errors.New("invalid or oversized OpenBao response")
	}
	if len(data) > 0 && json.Unmarshal(data, &result) != nil {
		return result, errors.New("invalid OpenBao response")
	}
	return result, nil
}
func (c *client) login(ctx context.Context, cfg config) error {
	file, err := os.Open(cfg.jwtFile)
	if err != nil {
		return errors.New("cannot read projected ServiceAccount token")
	}
	defer func() { _ = file.Close() }()
	jwt, err := io.ReadAll(io.LimitReader(file, maxBody+1))
	if err != nil || len(jwt) == 0 || len(jwt) > maxBody {
		return errors.New("invalid projected ServiceAccount token")
	}
	result, err := c.request(ctx, c.namespace, http.MethodPost, "auth/"+cfg.authMount+"/login",
		map[string]string{"role": cfg.role, "jwt": strings.TrimSpace(string(jwt))})
	if err != nil {
		return err
	}
	if result.Auth.Token == "" {
		return errors.New("login returned no token")
	}
	c.token = result.Auth.Token
	return nil
}
func (c *client) crypto(ctx context.Context, mount, operation, key string, in input) (input, error) {
	result, err := c.request(ctx, c.namespace, http.MethodPost, mount+"/"+operation+"/"+key, in)
	if err != nil {
		return input{}, err
	}
	if operation == opDecrypt {
		if _, err := base64.StdEncoding.DecodeString(result.Data.Plaintext); err != nil || result.Data.Plaintext == "" {
			return input{}, errors.New("decrypt returned invalid plaintext")
		}
	} else if !strings.HasPrefix(result.Data.Ciphertext, "vault:v") {
		return input{}, errors.New("transit returned invalid ciphertext")
	}
	return result.Data, nil
}
func (c *client) execute(
	ctx context.Context, cfg config, operation, key, destination string, in input,
) (output, error) {
	switch operation {
	case opEncrypt:
		if _, err := base64.StdEncoding.DecodeString(in.Plaintext); err != nil || in.Plaintext == "" || in.Ciphertext != "" {
			return output{}, errors.New("encrypt requires nonempty base64 plaintext only")
		}
	case opDecrypt, opRewrap, opMigrate:
		if !strings.HasPrefix(in.Ciphertext, "vault:v") || in.Plaintext != "" {
			return output{}, errors.New("operation requires Transit ciphertext only")
		}
	default:
		return output{}, errors.New("unknown operation")
	}
	if operation == opMigrate {
		if !segment.MatchString(destination) || destination == key {
			return output{}, errors.New("migration requires a different destination key")
		}
		plain, err := c.crypto(ctx, cfg.mount, opDecrypt, key, in)
		if err != nil {
			return output{}, err
		}
		// Cross-key migration requires plaintext inside the application's trust boundary.
		next, err := c.crypto(ctx, cfg.mount, opEncrypt, destination, input{Plaintext: plain.Plaintext})
		if err != nil {
			return output{}, err
		}
		return output{Ciphertext: next.Ciphertext}, nil
	}
	result, err := c.crypto(ctx, cfg.mount, operation, key, in)
	if err != nil {
		return output{}, err
	}
	if operation == opDecrypt {
		return output{Plaintext: result.Plaintext}, nil
	}
	return output{Ciphertext: result.Ciphertext}, nil
}
func (c *client) smoke(
	ctx context.Context, cfg config, key, migrationKey, deniedKey, deniedNamespace string,
) (output, error) {
	if !segment.MatchString(migrationKey) || migrationKey == key || migrationKey == deniedKey {
		return output{}, errors.New("smoke requires a distinct migration key")
	}
	if !segment.MatchString(deniedKey) || deniedKey == key {
		return output{}, errors.New("smoke requires a distinct denied key")
	}
	if deniedNamespace == "" {
		return output{}, errors.New("smoke requires an explicit denied namespace")
	}
	if deniedNamespace == "root" {
		deniedNamespace = ""
	}
	if !validPath(deniedNamespace, true) || deniedNamespace == cfg.namespace {
		return output{}, errors.New("smoke requires a different namespace")
	}
	plain := base64.StdEncoding.EncodeToString([]byte("nonsecret transit application smoke probe"))
	encrypted, err := c.execute(ctx, cfg, opEncrypt, key, "", input{Plaintext: plain})
	if err != nil {
		return output{}, err
	}
	decrypted, err := c.execute(ctx, cfg, opDecrypt, key, "", input{Ciphertext: encrypted.Ciphertext})
	if err != nil {
		return output{}, err
	}
	if decrypted.Plaintext != plain {
		return output{}, errors.New("round trip changed the application value")
	}
	rewrapped, err := c.execute(ctx, cfg, opRewrap, key, "", input{Ciphertext: encrypted.Ciphertext})
	if err != nil {
		return output{}, err
	}
	verified, err := c.execute(ctx, cfg, opDecrypt, key, "", input{Ciphertext: rewrapped.Ciphertext})
	if err != nil {
		return output{}, err
	}
	if verified.Plaintext != plain {
		return output{}, errors.New("rewrap changed the application value")
	}
	migrated, err := c.execute(ctx, cfg, opMigrate, key, migrationKey, input{Ciphertext: encrypted.Ciphertext})
	if err != nil {
		return output{}, err
	}
	verified, err = c.execute(ctx, cfg, opDecrypt, migrationKey, "", input{Ciphertext: migrated.Ciphertext})
	if err != nil {
		return output{}, err
	}
	if verified.Plaintext != plain {
		return output{}, errors.New("migration changed the application value")
	}
	for _, probe := range []struct {
		namespace, method, path string
		body                    any
	}{
		{cfg.namespace, http.MethodPost, cfg.mount + "/encrypt/" + deniedKey, input{Plaintext: plain}},
		{deniedNamespace, http.MethodPost, cfg.mount + "/encrypt/" + key, input{Plaintext: plain}},
		{cfg.namespace, http.MethodGet, cfg.mount + "/export/encryption-key/" + key, nil},
		{cfg.namespace, http.MethodGet, cfg.mount + "/backup/" + key, nil},
	} {
		_, err := c.request(ctx, probe.namespace, probe.method, probe.path, probe.body)
		var status statusError
		if !errors.As(err, &status) || status != http.StatusForbidden {
			return output{}, errors.New("restricted operation did not return HTTP 403")
		}
	}
	return output{OK: true}, nil
}
func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, cfg config) error {
	if len(args) == 0 {
		return errors.New("usage: transit encrypt|decrypt|rewrap|migrate-key|smoke --key NAME")
	}
	flags := flag.NewFlagSet("transit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	key := flags.String("key", "", "Transit key name")
	destination := flags.String("destination-key", "", "destination key for migration")
	migrationKey := flags.String("migration-key", "", "allowed migration key for smoke test")
	deniedKey := flags.String("denied-key", "", "denied key for smoke test")
	deniedNamespace := flags.String("denied-namespace", "", "denied namespace for smoke test (root for root)")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || !segment.MatchString(*key) {
		return errors.New("invalid command flags or key name")
	}
	if args[0] != opEncrypt && args[0] != opDecrypt && args[0] != opRewrap && args[0] != opMigrate && args[0] != opSmoke {
		return errors.New("unknown operation")
	}
	if args[0] != opMigrate && *destination != "" {
		return errors.New("destination-key is only valid for migrate-key")
	}
	if args[0] != opSmoke && (*deniedKey != "" || *deniedNamespace != "" || *migrationKey != "") {
		return errors.New("denial flags are only valid for smoke")
	}
	var in input
	if args[0] != opSmoke {
		data, err := io.ReadAll(io.LimitReader(stdin, maxBody+1))
		if err != nil || len(data) > maxBody {
			return errors.New("invalid or oversized input")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil {
			return errors.New("input must be one JSON object")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return errors.New("input contains trailing data")
		}
	}
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	defer c.http.CloseIdleConnections()
	if err = c.login(ctx, cfg); err != nil {
		return err
	}
	var out output
	if args[0] == opSmoke {
		out, err = c.smoke(ctx, cfg, *key, *migrationKey, *deniedKey, *deniedNamespace)
	} else {
		out, err = c.execute(ctx, cfg, args[0], *key, *destination, in)
	}
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, revokeErr := c.request(revokeCtx, c.namespace, http.MethodPost, "auth/token/revoke-self", map[string]string{})
	c.token = ""
	if err != nil {
		return err
	}
	if revokeErr != nil {
		return errors.New("cannot revoke application session")
	}
	if json.NewEncoder(stdout).Encode(out) != nil {
		return errors.New("cannot write operation result")
	}
	return nil
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, environment()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
