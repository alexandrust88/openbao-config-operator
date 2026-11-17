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

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func fixture(t *testing.T, handler http.HandlerFunc) config {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	directory := t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	jwt := filepath.Join(directory, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jwt, []byte("fake-service-account-jwt"), 0600); err != nil {
		t.Fatal(err)
	}
	return config{address: server.URL, namespace: "alpha", authMount: "kubernetes", role: "app", mount: "transit", caFile: ca, jwtFile: jwt}
}
func reply(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
func session(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Header.Get("X-Vault-Namespace") != "alpha" {
		t.Error("namespace missing")
	}
	switch r.URL.Path {
	case "/v1/auth/kubernetes/login":
		if r.Header.Get("X-Vault-Token") != "" {
			t.Error("login sent a token")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["jwt"] != "fake-service-account-jwt" || body["role"] != "app" {
			t.Error("workload auth body incorrect")
		}
		reply(t, w, map[string]any{"auth": map[string]string{"client_token": "fake-bao-token"}})
		return true
	case "/v1/auth/token/revoke-self":
		if r.Header.Get("X-Vault-Token") != "fake-bao-token" {
			t.Error("revoke token missing")
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if r.Header.Get("X-Vault-Token") != "fake-bao-token" {
		t.Error("application token missing")
	}
	return false
}
func TestCrossKeyMigrationDecryptsThenEncrypts(t *testing.T) {
	var paths []string
	plaintext := base64.StdEncoding.EncodeToString([]byte("fixture"))
	cfg := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if session(t, w, r) {
			return
		}
		paths = append(paths, r.URL.Path)
		var body input
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v1/transit/decrypt/old":
			if body.Ciphertext != "vault:v1:old" {
				t.Error("wrong source ciphertext")
			}
			reply(t, w, map[string]any{"data": input{Plaintext: plaintext}})
		case "/v1/transit/encrypt/new":
			if body.Plaintext != plaintext {
				t.Error("migration did not pass decrypted plaintext")
			}
			reply(t, w, map[string]any{"data": input{Ciphertext: "vault:v1:new"}})
		default:
			t.Error("unexpected path")
			w.WriteHeader(404)
		}
	})
	var out bytes.Buffer
	err := run(t.Context(), []string{"migrate-key", "--key", "old", "--destination-key", "new"}, strings.NewReader(`{"ciphertext":"vault:v1:old"}`), &out, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/v1/transit/decrypt/old,/v1/transit/encrypt/new" {
		t.Fatal(paths)
	}
	if out.String() != "{\"ciphertext\":\"vault:v1:new\"}\n" {
		t.Fatal("unexpected migration result")
	}
	if strings.Contains(out.String(), plaintext) {
		t.Fatal("migration disclosed plaintext")
	}
}
func TestRewrapKeepsKeyAndNeverDecrypts(t *testing.T) {
	cfg := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if session(t, w, r) {
			return
		}
		if r.URL.Path != "/v1/transit/rewrap/app" {
			t.Error("rewrap changed key or decrypted data")
		}
		reply(t, w, map[string]any{"data": input{Ciphertext: "vault:v2:rewrapped"}})
	})
	var out bytes.Buffer
	if err := run(t.Context(), []string{"rewrap", "--key", "app"}, strings.NewReader(`{"ciphertext":"vault:v1:old"}`), &out, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "vault:v2:rewrapped") {
		t.Fatal("new version missing")
	}
}
func TestRedirectDoesNotForwardCredential(t *testing.T) {
	var foreign atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { foreign.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	cfg := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if session(t, w, r) {
			return
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	})
	var out bytes.Buffer
	err := run(t.Context(), []string{"encrypt", "--key", "app"}, strings.NewReader(`{"plaintext":"cHJvYmU="}`), &out, cfg)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect was not rejected: %v", err)
	}
	if foreign.Load() != 0 || out.Len() != 0 {
		t.Fatal("redirect forwarded credentials or emitted output")
	}
}
func TestErrorsNeverExposeResponseAndRevokeSession(t *testing.T) {
	var revoked bool
	cfg := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/token/revoke-self" {
			revoked = true
		}
		if session(t, w, r) {
			return
		}
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"errors":["private-data fake-bao-token"]}`)
	})
	var out bytes.Buffer
	err := run(t.Context(), []string{"encrypt", "--key", "app"}, strings.NewReader(`{"plaintext":"cHJvYmU="}`), &out, cfg)
	if err == nil || err.Error() != "OpenBao returned HTTP 403" || !revoked || out.Len() != 0 {
		t.Fatal("failure was not safely reported/revoked")
	}
}
func TestInputValidationBeforeRequests(t *testing.T) {
	for _, value := range []string{`{"plaintext":"cHJvYmU=","extra":"private"}`, `{"plaintext":"cHJvYmU="} {}`, strings.Repeat("x", maxBody+1)} {
		err := run(t.Context(), []string{"encrypt", "--key", "app"}, strings.NewReader(value), io.Discard, config{})
		if err == nil || strings.Contains(err.Error(), "BAO_ADDR") {
			t.Fatal("invalid input reached authentication")
		}
	}
	for _, key := range []string{"../export", "a/b", "?token=x"} {
		if err := run(t.Context(), []string{"encrypt", "--key", key}, strings.NewReader(`{}`), io.Discard, config{}); err == nil {
			t.Fatal("accepted unsafe key")
		}
	}
}
func TestSmokeRequiresForbiddenNotUnavailable(t *testing.T) {
	for _, denyStatus := range []int{403, 404, 500} {
		t.Run(fmt.Sprint(denyStatus), func(t *testing.T) {
			plaintext := base64.StdEncoding.EncodeToString([]byte("nonsecret transit application smoke probe"))
			cfg := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Vault-Namespace") == "other" {
					w.WriteHeader(denyStatus)
					return
				}
				if session(t, w, r) {
					return
				}
				switch r.URL.Path {
				case "/v1/transit/encrypt/app", "/v1/transit/rewrap/app", "/v1/transit/encrypt/migration":
					reply(t, w, map[string]any{"data": input{Ciphertext: "vault:v1:fixture"}})
				case "/v1/transit/decrypt/app", "/v1/transit/decrypt/migration":
					reply(t, w, map[string]any{"data": input{Plaintext: plaintext}})
				default:
					w.WriteHeader(denyStatus)
				}
			})
			var out bytes.Buffer
			err := run(t.Context(), []string{"smoke", "--key", "app", "--migration-key", "migration", "--denied-key", "other-key", "--denied-namespace", "other"}, strings.NewReader(""), &out, cfg)
			if denyStatus == 403 {
				if err != nil || out.String() != "{\"ok\":true}\n" {
					t.Fatal("expected smoke success", err)
				}
			} else if err == nil || out.Len() != 0 {
				t.Fatal("unavailable target counted as denial")
			}
		})
	}
}
func TestTransportRejectsInsecureOriginAndUntrustedCA(t *testing.T) {
	for _, address := range []string{"http://example.invalid", "https://user:password@example.invalid", "https://example.invalid/v1", "https://example.invalid?token=x"} {
		if _, err := newClient(config{address: address}); err == nil {
			t.Fatal("accepted insecure origin")
		}
	}
	cfg := fixture(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("untrusted connection reached handler") })
	cfg.caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cfg.caFile, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newClient(cfg); err == nil {
		t.Fatal("invalid CA accepted")
	}
}
func TestCanceledRequestIsSanitized(t *testing.T) {
	cfg := fixture(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("canceled request reached server") })
	c, err := newClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.http.CloseIdleConnections()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.request(ctx, "alpha", http.MethodPost, "probe", input{}); err == nil || err.Error() != "OpenBao request failed" {
		t.Fatal("unexpected canceled-request error")
	}
}

func TestRewrapRejectsDestinationKey(t *testing.T) {
	err := run(t.Context(), []string{"rewrap", "--key", "app", "--destination-key", "new"}, strings.NewReader(`{"ciphertext":"vault:v1:old"}`), io.Discard, config{})
	if err == nil || err.Error() != "destination-key is only valid for migrate-key" {
		t.Fatal("rewrap silently ignored a cross-key request")
	}
}
