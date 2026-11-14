package sys

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	audHeaders     = `{"Authorization":["Bearer fake"]}`
	audHTTP        = "http"
	audFile        = "file"
	audDescription = "siem forwarder"
	audURI         = "https://siem.example.com"
	audHeadersKey  = "headers"
	audSecretKey   = "siem-token/headers"
	audDenied      = "permission denied"
	audNoHMAC      = "false"
	audHMACKey     = "hmac_accessor"
	audURIKey      = "uri"
)

func audDevice() *sysv1alpha1.Audit {
	return &sysv1alpha1.Audit{
		ObjectMeta: metav1.ObjectMeta{Name: "siem", Namespace: testNamespace},
		Spec: sysv1alpha1.AuditSpec{
			Path:        "audit/siem",
			Type:        audHTTP,
			Description: audDescription,
			Local:       true,
			Options:     map[string]sysv1alpha1.AuditOptionValue{audURIKey: audURI, audHMACKey: audNoHMAC},
			OptionsSecretRefs: map[string]commonv1alpha1.SecretKeySelector{
				audHeadersKey: {Name: "siem-token", Key: audHeadersKey},
			},
		},
	}
}

// audTable is the read shape of GET sys/audit (handleAuditTable) holding one device.
func audTable(options map[string]any) map[string]map[string]any {
	return map[string]map[string]any{auditTablePath: {
		"audit/siem/": map[string]any{
			litPath:        "audit/siem/",
			keyType:        audHTTP,
			keyDescription: audDescription,
			keyLocal:       true,
			keyOptions:     options,
		},
	}}
}

func audOptions() map[string]any {
	return map[string]any{audURIKey: audURI, audHMACKey: audNoHMAC, audHeadersKey: audHeaders}
}

func TestAuditPathAndDesired(t *testing.T) {
	a := AuditAdapter{}
	obj := audDevice()
	if got := a.Path(obj); got != "sys/audit/audit/siem" {
		t.Errorf("path = %q", got)
	}
	obj.Spec.Path = ""
	if got := a.Path(obj); got != "sys/audit/siem" {
		t.Errorf("default path = %q", got)
	}
	if !a.Destructive() || a.DriftTier() != engine.TierAccessCritical {
		t.Error("an audit device must be destructive and access-critical")
	}

	desired, err := a.Desired(context.Background(), secretResolver{audSecretKey: audHeaders}, audDevice())
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Payload{
		keyType:        audHTTP,
		keyDescription: audDescription,
		keyLocal:       true,
		keyOptions:     map[string]string{audURIKey: audURI, audHMACKey: audNoHMAC, audHeadersKey: audHeaders},
	}
	if !reflect.DeepEqual(desired, want) {
		t.Errorf("desired = %v", desired)
	}

	_, err = a.Desired(context.Background(), secretResolver{}, audDevice())
	if !errors.Is(err, engine.ErrReferenceNotReady) || !strings.Contains(err.Error(), "optionsSecretRefs.headers") {
		t.Errorf("missing secret: err = %v", err)
	}
	if got := a.SecretRefs(audDevice()); !slices.Equal(got, []string{"siem-token"}) {
		t.Errorf("secret refs = %v", got)
	}
}

func TestAuditDesiredMinimal(t *testing.T) {
	obj := &sysv1alpha1.Audit{
		ObjectMeta: metav1.ObjectMeta{Name: audFile},
		Spec:       sysv1alpha1.AuditSpec{Type: audFile, Options: map[string]sysv1alpha1.AuditOptionValue{"file_path": "stdout"}},
	}
	a := AuditAdapter{}
	desired, err := a.Desired(context.Background(), nil, obj)
	if err != nil {
		t.Fatal(err)
	}
	c := newConfigClient(map[string]map[string]any{auditTablePath: {
		"file/": map[string]any{litPath: "file/", keyType: audFile, keyDescription: "", keyLocal: false,
			keyOptions: map[string]any{"file_path": "stdout"}},
	}})
	observed, found, err := a.Observe(context.Background(), c, obj)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())
}

func TestAuditObserve(t *testing.T) {
	a := AuditAdapter{}
	ctx := context.Background()
	desired, _ := a.Desired(ctx, secretResolver{audSecretKey: audHeaders}, audDevice())

	observed, found, err := a.Observe(ctx, newConfigClient(audTable(audOptions())), audDevice())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	rotated := audOptions()
	rotated[audHeadersKey] = `{"Authorization":["Bearer old"]}`
	observed, _, _ = a.Observe(ctx, newConfigClient(audTable(rotated)), audDevice())
	assertDiffers(t, desired, observed, a.Fields(), keyOptions)

	extra := audOptions()
	extra["log_raw"] = "true"
	observed, _, _ = a.Observe(ctx, newConfigClient(audTable(extra)), audDevice())
	assertDiffers(t, desired, observed, a.Fields(), keyOptions)

	if _, found, err := a.Observe(ctx, newConfigClient(map[string]map[string]any{auditTablePath: {}}), audDevice()); err != nil || found {
		t.Errorf("device missing from the table: found=%v err=%v", found, err)
	}
	if _, found, err := a.Observe(ctx, newConfigClient(nil), audDevice()); err != nil || found {
		t.Errorf("404 table: found=%v err=%v", found, err)
	}
	c := newConfigClient(nil)
	c.errs["read "+auditTablePath] = &openbao.ResponseError{StatusCode: 403, Errors: []string{audDenied}}
	if _, _, err := a.Observe(ctx, c, audDevice()); err == nil {
		t.Error("403 must stay an error")
	}
	_, _, err = a.Observe(ctx, newConfigClient(nil).WithNamespace(testNamespace), audDevice())
	assertUnsupported(t, err)
}

func TestAuditWrite(t *testing.T) {
	a := AuditAdapter{}
	ctx := context.Background()
	desired, _ := a.Desired(ctx, secretResolver{audSecretKey: audHeaders}, audDevice())
	path := a.Path(audDevice())

	c := newConfigClient(nil)
	if _, err := a.Write(ctx, c, audDevice(), desired, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.writes[path], desired) {
		t.Errorf("enable payload = %v", c.writes[path])
	}

	c = newConfigClient(nil)
	_, err := a.Write(ctx, c, audDevice(), desired, false)
	assertUnsupported(t, err)
	if len(c.writes) != 0 {
		t.Errorf("an update wrote %v", c.writes)
	}

	for msg, hint := range map[string]string{
		auditAPICreationRefused + "; use declarative, config-based audit device management instead": "unsafe_allow_api_audit_creation",
		auditPrefixRefused: "allow_audit_log_prefixing",
	} {
		c = newConfigClient(nil)
		c.errs["write "+path] = badRequest(msg)
		_, err = a.Write(ctx, c, audDevice(), desired, true)
		assertUnsupported(t, err)
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("err = %v, want a hint at %s", err, hint)
		}
	}

	c = newConfigClient(nil)
	c.errs["write "+path] = badRequest("path already in use")
	if _, err = a.Write(ctx, c, audDevice(), desired, true); err == nil || errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("other 400: err = %v, want a plain error", err)
	}
}

func TestAuditDelete(t *testing.T) {
	a := AuditAdapter{}
	ctx := context.Background()
	c := newConfigClient(nil)
	if err := a.Delete(ctx, c, audDevice()); err != nil || !slices.Equal(c.deletes, []string{"sys/audit/audit/siem"}) {
		t.Errorf("deletes = %v err = %v", c.deletes, err)
	}

	err := a.Delete(ctx, audDeleteFails{c}, audDevice())
	if err == nil || !strings.Contains(err.Error(), "deletionPolicy to Orphan") {
		t.Errorf("config-managed device: err = %v", err)
	}
}

// audDeleteFails answers every delete like OpenBao does for a configuration-managed audit device.
type audDeleteFails struct{ *configClient }

func (audDeleteFails) Delete(context.Context, string) error { return badRequest(auditConfigManaged) }
