package importer

import (
	"strings"
	"testing"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
)

// TestServerCABundleInPlatformNamespace keeps the trust anchor of the emitted OpenBaoServer out of the tenant
// namespace of the resources, where anyone who can write Secrets would control TLS trust for the server.
func TestServerCABundleInPlatformNamespace(t *testing.T) {
	imp := &importer{result: &Result{}, opts: Options{
		Connection: commonv1alpha1.ConnectionReference{Name: "openbao"},
		Namespace:  "team-a",
		Server:     &Server{Name: "openbao", Address: "https://bao.example", CABundle: true, CANamespace: "openbao-system"},
	}}
	server, ok := imp.serverObjects()[0].(*corev1alpha1.OpenBaoServer)
	if !ok {
		t.Fatal("first object is not the OpenBaoServer")
	}
	ref := server.Spec.CABundle.SecretRef
	if ref == nil || ref.Namespace != "openbao-system" || ref.Name != "openbao-ca" {
		t.Fatalf("caBundle.secretRef = %+v, want openbao-system/openbao-ca", ref)
	}
	if !strings.Contains(strings.Join(imp.result.Notes, "\n"), "Secret openbao-system/openbao-ca") {
		t.Errorf("notes %q do not name the CA bundle Secret and its namespace", imp.result.Notes)
	}
}
