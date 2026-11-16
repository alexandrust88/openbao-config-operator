//go:build integration

package livekit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

// prepareCRLURLs serves the rendered PEM fixture locally so URL examples exercise a real OpenBao fetch.
func prepareCRLURLs(t *testing.T, objs []*unstructured.Unstructured) {
	t.Helper()
	var pem string
	var urls []*unstructured.Unstructured
	for _, obj := range objs {
		if obj.GetKind() != "CertAuthCRL" {
			continue
		}
		if value, _, _ := unstructured.NestedString(obj.Object, "spec", "crl"); value != "" {
			pem = value
		}
		if value, _, _ := unstructured.NestedString(obj.Object, "spec", "url"); value != "" {
			urls = append(urls, obj)
		}
	}
	if len(urls) == 0 {
		return
	}
	if pem == "" {
		t.Fatal("URL CRL examples require a rendered PEM CRL fixture")
	}
	var mu sync.Mutex
	fetched := make(map[string]bool, len(urls))
	for _, obj := range urls {
		fetched["/"+obj.GetName()+".crl"] = false
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := fetched[r.URL.Path]; !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		fetched[r.URL.Path] = true
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write([]byte(pem))
	}))
	t.Cleanup(func() {
		server.Close()
		mu.Lock()
		defer mu.Unlock()
		for path, ok := range fetched {
			if !ok {
				t.Errorf("OpenBao did not fetch CRL fixture %s", path)
			}
		}
	})
	for _, obj := range urls {
		url := server.URL + "/" + obj.GetName() + ".crl"
		if err := unstructured.SetNestedField(obj.Object, url, "spec", "url"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCRLURLFixture(t *testing.T) {
	root := repoRoot(t)
	bin := testenv.RequireBao(t)
	objs := render(t, kustomizeBin(t, root), kitDir(root)+"/overlays/root/full")
	prepareCRLURLs(t, objs)
	b := startBao(t, bin, t.TempDir(), "crl-url")
	if _, _, err := b.do("", http.MethodPost, "sys/auth/fixture-cert", map[string]any{"type": "cert"}); err != nil {
		t.Fatal(err)
	}
	fetched := 0
	for _, obj := range objs {
		if obj.GetKind() != "CertAuthCRL" {
			continue
		}
		url, _, _ := unstructured.NestedString(obj.Object, "spec", "url")
		if url == "" {
			continue
		}
		path := "auth/fixture-cert/crls/" + obj.GetName()
		if _, _, err := b.do("", http.MethodPost, path, map[string]any{"url": url}); err != nil {
			t.Fatal(err)
		}
		got, _, err := b.do("", http.MethodGet, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		stored, _, err := unstructured.NestedString(got, "data", "cdp", "url")
		if err != nil {
			t.Fatal(err)
		}
		if stored != url {
			t.Fatalf("stored CRL URL = %q, want %q", stored, url)
		}
		fetched++
	}
	if fetched == 0 {
		t.Fatal("rendered full overlay contains no URL CRL")
	}
}
