package sys

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// goldenQuota is GET sys/quotas/rate-limit/q1 from bao server -dev 2.7.2 after writing path=openbao-config/kv2.
const goldenQuota = `{"block_interval":30,"inheritable":true,"interval":2,"name":"q1","path":"openbao-config/kv2/","rate":10.5,"role":"","type":"rate-limit"}`

func fullQuota() *sysv1alpha1.RateLimitQuota {
	return &sysv1alpha1.RateLimitQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "kv-limit", Namespace: testNamespace},
		Spec: sysv1alpha1.RateLimitQuotaSpec{
			Name:          "q1",
			Path:          "openbao-config/kv2",
			Rate:          "10.5",
			Interval:      metaDuration(2 * time.Second),
			BlockInterval: metaDuration(30 * time.Second),
			Inheritable:   new(true),
		},
	}
}

func TestRateLimitQuotaDesired(t *testing.T) {
	a := RateLimitQuotaAdapter{}
	q := fullQuota()
	if got := a.Path(q); got != litQuotaQ1 {
		t.Errorf("path = %q", got)
	}
	q.Spec.Role = "app"
	desired, err := a.Desired(context.Background(), nil, q)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		litRate: 10.5, litPath: "openbao-config/kv2", "role": "app",
		keyRotationInterval: int64(2), keyBlockInterval: int64(30), litInheritable: true,
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired = %v, want %v", desired, want)
	}
	assertFieldSpecCovers(t, desired, a.Fields())

	minimal := &sysv1alpha1.RateLimitQuota{ObjectMeta: metav1.ObjectMeta{Name: "global"}, Spec: sysv1alpha1.RateLimitQuotaSpec{Rate: "100"}}
	desired, _ = a.Desired(context.Background(), nil, minimal)
	want = engine.Payload{litRate: 100.0, litPath: "", keyRotationInterval: int64(1), keyBlockInterval: int64(0), litInheritable: true}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("minimal desired = %v, want %v", desired, want)
	}
	minimal.Spec.Path = litTeamA
	desired, _ = a.Desired(context.Background(), nil, minimal)
	if desired[litInheritable] != false {
		t.Errorf("scoped quota: inheritable = %v, want false (the server default)", desired[litInheritable])
	}
}

// goldenGlobalQuota is GET sys/quotas/rate-limit/q2 from bao server -dev 2.7.2 after writing only rate=100.
const goldenGlobalQuota = `{"block_interval":0,"inheritable":true,"interval":1,"name":"q2","path":"","rate":100,"role":"","type":"rate-limit"}`

// TestRateLimitQuotaFullReplace checks that a quota whose spec leaves the optional fields unset matches the
// server defaults and repairs values set out of band, which the next write would reset anyway.
func TestRateLimitQuotaFullReplace(t *testing.T) {
	a := RateLimitQuotaAdapter{}
	q := &sysv1alpha1.RateLimitQuota{ObjectMeta: metav1.ObjectMeta{Name: "q2"}, Spec: sysv1alpha1.RateLimitQuotaSpec{Rate: "100"}}
	desired, _ := a.Desired(context.Background(), nil, q)
	for name, mutate := range map[string]func(map[string]any){
		"":                  nil,
		keyBlockInterval:    func(d map[string]any) { d[keyBlockInterval] = json.Number("30") },
		keyRotationInterval: func(d map[string]any) { d[keyRotationInterval] = json.Number("5") },
		litInheritable:      func(d map[string]any) { d[litInheritable] = false },
		litPath:             func(d map[string]any) { d[litPath] = litTeamA },
	} {
		read := decodeRead(t, goldenGlobalQuota)
		if mutate != nil {
			mutate(read)
		}
		c := &recordingBao{reads: map[string]map[string]any{"sys/quotas/rate-limit/q2": read}}
		observed, found, err := a.Observe(context.Background(), c, q)
		if err != nil || !found {
			t.Fatalf("%q: found=%v err=%v", name, found, err)
		}
		var want []string
		if name != "" {
			want = []string{name}
		}
		if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, want) {
			t.Errorf("%q: differing = %v, want %v", name, got, want)
		}
	}
}

func TestRateLimitQuotaRoleMismatch(t *testing.T) {
	read := decodeRead(t, goldenQuota)
	read["role"] = "other"
	c := &recordingBao{reads: map[string]map[string]any{litQuotaQ1: read}}
	if _, _, err := (RateLimitQuotaAdapter{}).Observe(context.Background(), c, fullQuota()); err == nil {
		t.Error("a stored role that differs from the spec must be an error")
	}
}

func TestRateLimitQuotaObserveGolden(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		differing []string
	}{
		{name: "in sync, trailing slash"},
		{name: "rate changed", mutate: func(d map[string]any) { d[litRate] = json.Number("11") }, differing: []string{litRate}},
		{name: "path changed", mutate: func(d map[string]any) { d[litPath] = "openbao-config/other/" }, differing: []string{litPath}},
		{name: "interval changed", mutate: func(d map[string]any) { d[keyRotationInterval] = json.Number("1") }, differing: []string{keyRotationInterval}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := decodeRead(t, goldenQuota)
			if tt.mutate != nil {
				tt.mutate(read)
			}
			a := RateLimitQuotaAdapter{}
			q := fullQuota()
			c := &recordingBao{reads: map[string]map[string]any{litQuotaQ1: read}}
			observed, found, err := a.Observe(context.Background(), c, q)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			desired, _ := a.Desired(context.Background(), nil, q)
			if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, tt.differing) {
				t.Errorf("differing = %v, want %v", got, tt.differing)
			}
		})
	}

	if _, found, err := (RateLimitQuotaAdapter{}).Observe(context.Background(), &recordingBao{}, fullQuota()); err != nil || found {
		t.Errorf("absent: found=%v err=%v", found, err)
	}
	child := &recordingBao{namespace: litTeamA}
	if _, _, err := (RateLimitQuotaAdapter{}).Observe(context.Background(), child, fullQuota()); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("child namespace: err = %v, want ErrUnsupported", err)
	}
}
