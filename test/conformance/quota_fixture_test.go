//go:build conformance

package conformance

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

func TestQuotaFixtureDoesNotThrottleNamespaceSetup(t *testing.T) {
	objects := confDecodeFile(t, "fixtures/sys/ratelimitquota.yaml")
	if len(objects) != 1 {
		t.Fatal("expected one quota fixture")
	}
	rateText, _, err := unstructured.NestedString(objects[0].Object, "spec", "rate")
	if err != nil {
		t.Fatal(err)
	}
	rate, err := strconv.ParseFloat(rateText, 64)
	if err != nil {
		t.Fatal(err)
	}
	bao := confStartBao(t, testenv.RequireBao(t), t.TempDir())
	// The defaults test removes path, and its blockInterval case can block the shared client's IP.
	_, _, err = bao.do("", http.MethodPost, "sys/quotas/rate-limit/fixture", map[string]any{
		"rate": rate, "path": "", "interval": 1, "block_interval": 30, "inheritable": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for item := range 16 {
				if err := bao.createNamespace(fmt.Sprintf("quota-setup-%d-%d", worker, item)); err != nil {
					t.Errorf("quota fixture interfered with parallel namespace setup: %v", err)
					return
				}
			}
		})
	}
	workers.Wait()
}
