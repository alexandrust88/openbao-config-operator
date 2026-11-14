package sys

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// goldenNamespace is GET sys/namespaces/child in namespace openbao-config from bao server -dev 2.7.2.
const goldenNamespace = `{"custom_metadata":{"team":"a"},"id":"uwCelB","locked":false,"path":"openbao-config/child/","tainted":false,"uuid":"16daeee2-ba6d-5f25-0a7d-9f0e54b8e9d2"}`

func newNamespace(metadata map[string]string) *sysv1alpha1.OpenBaoNamespace {
	return &sysv1alpha1.OpenBaoNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: "team-a", Namespace: testNamespace},
		Spec:       sysv1alpha1.OpenBaoNamespaceSpec{Name: "child", CustomMetadata: metadata},
	}
}

func TestOpenBaoNamespacePathAndDesired(t *testing.T) {
	a := OpenBaoNamespaceAdapter{}
	n := newNamespace(map[string]string{testNamespace: "a"})
	if got := a.Path(n); got != litSysNamespacesChild {
		t.Errorf("path = %q", got)
	}
	n.Spec.Name = ""
	if got := a.Path(n); got != "sys/namespaces/team-a" {
		t.Errorf("default path = %q", got)
	}
	desired, _ := a.Desired(context.Background(), nil, n)
	if !reflect.DeepEqual(desired, engine.Payload{litCustomMetadata: map[string]string{testNamespace: "a"}}) {
		t.Errorf("desired = %v", desired)
	}
	desired, _ = a.Desired(context.Background(), nil, newNamespace(nil))
	if !reflect.DeepEqual(desired, engine.Payload{litCustomMetadata: map[string]string{}}) {
		t.Errorf("unset custom metadata must be sent empty, got %v", desired)
	}
}

func TestOpenBaoNamespaceObserve(t *testing.T) {
	tests := []struct {
		name      string
		metadata  map[string]string
		differing []string
	}{
		{name: "in sync", metadata: map[string]string{testNamespace: "a"}},
		{name: "value changed", metadata: map[string]string{testNamespace: "b"}, differing: []string{litCustomMetadata}},
		{name: "stored key removed from spec", metadata: map[string]string{"owner": "x"}, differing: []string{litCustomMetadata}},
		{name: "unset metadata clears stored keys", metadata: nil, differing: []string{litCustomMetadata}},
		{name: "empty map clears stored keys", metadata: map[string]string{}, differing: []string{litCustomMetadata}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := OpenBaoNamespaceAdapter{}
			n := newNamespace(tt.metadata)
			c := &recordingBao{reads: map[string]map[string]any{litSysNamespacesChild: decodeRead(t, goldenNamespace)}}
			observed, found, err := a.Observe(context.Background(), c, n)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			desired, _ := a.Desired(context.Background(), nil, n)
			if got := engine.Diff(desired, observed, a.Fields()); !slices.Equal(got, tt.differing) {
				t.Errorf("differing = %v, want %v", got, tt.differing)
			}
		})
	}
}

func TestOpenBaoNamespaceObserveAbsentAndTainted(t *testing.T) {
	a := OpenBaoNamespaceAdapter{}
	n := newNamespace(nil)
	if _, found, err := a.Observe(context.Background(), &recordingBao{}, n); err != nil || found {
		t.Errorf("absent: found=%v err=%v", found, err)
	}
	tainted := decodeRead(t, goldenNamespace)
	tainted["tainted"] = true
	c := &recordingBao{reads: map[string]map[string]any{litSysNamespacesChild: tainted}}
	if _, _, err := a.Observe(context.Background(), c, n); !errors.Is(err, engine.ErrInProgress) {
		t.Errorf("tainted: err = %v, want ErrInProgress", err)
	}
}

func TestOpenBaoNamespaceDelete(t *testing.T) {
	a := OpenBaoNamespaceAdapter{}
	n := newNamespace(nil)

	c := &recordingBao{}
	if err := a.Delete(context.Background(), c, n); err != nil {
		t.Errorf("gone after delete: err = %v", err)
	}
	if !slices.Equal(c.deletes, []string{litSysNamespacesChild}) {
		t.Errorf("deletes = %v", c.deletes)
	}

	c = &recordingBao{reads: map[string]map[string]any{litSysNamespacesChild: decodeRead(t, goldenNamespace)}}
	if err := a.Delete(context.Background(), c, n); !errors.Is(err, engine.ErrInProgress) {
		t.Errorf("still present: err = %v, want ErrInProgress", err)
	}

	children := &openbao.ResponseError{StatusCode: 400, Errors: []string{"unable to delete namespace containing child namespaces"}}
	c = &recordingBao{deleteErr: children}
	err := a.Delete(context.Background(), c, n)
	if err == nil || errors.Is(err, engine.ErrInProgress) || !strings.Contains(err.Error(), "delete them first") {
		t.Errorf("children: err = %v, want a clear blocking error", err)
	}

	c = &recordingBao{deleteErr: &openbao.ResponseError{StatusCode: 409}}
	if err := a.Delete(context.Background(), c, n); err == nil || !strings.Contains(err.Error(), "child namespaces") {
		t.Errorf("409: err = %v", err)
	}

	c = &recordingBao{deleteErr: &openbao.ResponseError{StatusCode: 403, Errors: []string{"permission denied"}}}
	if err := a.Delete(context.Background(), c, n); err == nil || strings.Contains(err.Error(), "child namespaces") {
		t.Errorf("403: err = %v", err)
	}
}

func TestOpenBaoNamespaceWriteStatus(t *testing.T) {
	n := newNamespace(nil)
	if err := (OpenBaoNamespaceAdapter{}).WriteStatus(context.Background(), nil, n, decodeRead(t, goldenNamespace)); err != nil {
		t.Fatal(err)
	}
	if n.Status.ID != "uwCelB" {
		t.Errorf("status.id = %q", n.Status.ID)
	}
}

func TestOpenBaoNamespaceMarker(t *testing.T) {
	a := OpenBaoNamespaceAdapter{}
	n := newNamespace(map[string]string{testNamespace: "a"})
	desired, _ := a.Desired(context.Background(), nil, n)
	marker := engine.OwnershipMarker{ManagedBy: "openbao-config-operator", ClusterID: "c1", UID: "uid-1"}
	marked := a.Mark(n, desired, marker)
	want := map[string]string{
		testNamespace: "a", "openbaoconfig-managed-by": "openbao-config-operator",
		"openbaoconfig-cluster-id": "c1", "openbaoconfig-uid": "uid-1",
	}
	if !reflect.DeepEqual(marked[litCustomMetadata], want) {
		t.Errorf("marked custom_metadata = %v, want %v", marked[litCustomMetadata], want)
	}
	if len(desired[litCustomMetadata].(map[string]string)) != 1 {
		t.Error("Mark mutated desired")
	}

	read := decodeRead(t, goldenNamespace)
	read[litCustomMetadata] = map[string]any{testNamespace: "a", "openbaoconfig-managed-by": "openbao-config-operator",
		"openbaoconfig-cluster-id": "c1", "openbaoconfig-uid": "uid-1"}
	got, ok := a.MarkerOf(read)
	if !ok || got != marker {
		t.Errorf("MarkerOf = %v, %v, want %v", got, ok, marker)
	}
	assertInSync(t, marked, read, a.Fields())
	if _, ok := a.MarkerOf(decodeRead(t, goldenNamespace)); ok {
		t.Error("an unmarked namespace must report no marker")
	}
}
