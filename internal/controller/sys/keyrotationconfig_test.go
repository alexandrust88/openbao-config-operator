package sys

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
)

func TestKeyRotationConfigDesired(t *testing.T) {
	a := KeyRotationConfigAdapter{}
	ctx := context.Background()
	partial, _ := a.Desired(ctx, nil, &sysv1alpha1.KeyRotationConfig{Spec: sysv1alpha1.KeyRotationConfigSpec{Interval: "720h"}})
	want := map[string]any{litEnabled: true, "max_operations": keyRotationMaxOperations, keyRotationInterval: "720h"}
	if !reflect.DeepEqual(partial, want) {
		t.Errorf("partial desired = %v, want %v (defaults for the omitted fields)", partial, want)
	}
	// GET sys/rotate/config of a fresh bao server -dev 2.7.2.
	fresh := decodeRead(t, `{"enabled":true,"interval":0,"max_operations":3865470566}`)
	empty, _ := a.Desired(ctx, nil, &sysv1alpha1.KeyRotationConfig{})
	assertInSync(t, empty, fresh, a.Fields())
	full, _ := a.Desired(ctx, nil, &sysv1alpha1.KeyRotationConfig{Spec: sysv1alpha1.KeyRotationConfigSpec{
		Enabled: new(false), MaxOperations: 2_000_000, Interval: "0",
	}})
	if full[litEnabled] != false || full["max_operations"] != int64(2_000_000) || full[keyRotationInterval] != "0" {
		t.Errorf("full desired = %v", full)
	}
}

func TestKeyRotationConfigObserve(t *testing.T) {
	a := KeyRotationConfigAdapter{}
	ctx := context.Background()
	k := &sysv1alpha1.KeyRotationConfig{Spec: sysv1alpha1.KeyRotationConfigSpec{
		Enabled: new(true), MaxOperations: 2_000_000, Interval: "48h",
	}}
	c := newConfigClient(map[string]map[string]any{keyRotationConfigPath: {
		litEnabled: true, keyRotationInterval: "48h0m0s", "max_operations": json.Number("2000000"),
	}})
	observed, found, err := a.Observe(ctx, c, k)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	desired, _ := a.Desired(ctx, nil, k)
	assertInSync(t, desired, observed, a.Fields())

	c.data[keyRotationConfigPath][keyRotationInterval] = json.Number("0")
	observed, _, _ = a.Observe(ctx, c, k)
	assertDiffers(t, desired, observed, a.Fields(), keyRotationInterval)

	c.errs["read "+keyRotationConfigPath] = badRequest("namespace is not sealable")
	_, _, err = a.Observe(ctx, c, k)
	assertUnsupported(t, err)
}

func TestKeyRotationConfigDeleteResets(t *testing.T) {
	c := newConfigClient(nil)
	if err := (KeyRotationConfigAdapter{}).Delete(context.Background(), c, &sysv1alpha1.KeyRotationConfig{}); err != nil {
		t.Fatal(err)
	}
	written := c.writes[keyRotationConfigPath]
	if written[litEnabled] != true || written["max_operations"] != keyRotationMaxOperations || written[keyRotationInterval] != "0" {
		t.Errorf("reset payload = %v", written)
	}
}
