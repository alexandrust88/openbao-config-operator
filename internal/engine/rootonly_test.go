package engine

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

type rootOnlyAdapter struct{ testAdapter }

func (rootOnlyAdapter) RootOnly() bool { return true }

func TestRootOnlyError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		adapter   any
		namespace string
		want      bool
	}{
		{"root", rootOnlyAdapter{}, "", false},
		{"root with slash", rootOnlyAdapter{}, "/", false},
		{"explicit root", rootOnlyAdapter{}, "root", false},
		{"child", rootOnlyAdapter{}, "team-a", true},
		{"nested child", rootOnlyAdapter{}, "team-a/apps/", true},
		{"any namespace kind", testAdapter{}, "team-a", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RootOnlyError(tc.adapter, "Logger", tc.namespace)
			if got := err != nil; got != tc.want {
				t.Fatalf("RootOnlyError = %v, want error %v", err, tc.want)
			}
			if tc.want && (!errors.Is(err, ErrRootOnly) || !errors.Is(err, ErrInvalidSpec)) {
				t.Fatalf("RootOnlyError = %v, want ErrRootOnly wrapping ErrInvalidSpec", err)
			}
		})
	}
}

func TestRefuseRootOnlyBeforeAnyCall(t *testing.T) {
	h := newHarness(t, rootOnlyAdapter{}, newResource(testName))
	obj := h.get()
	p := &pass[*testResource]{obj: obj, before: obj.DeepCopyObject().(client.Object), bao: h.conns.client.WithNamespace("apps")}
	_, done, err := h.r.refuseRootOnly(context.Background(), p)
	if !done || !errors.Is(err, reconcile.TerminalError(nil)) || !errors.Is(err, ErrRootOnly) {
		t.Fatalf("refuseRootOnly = done %v, err %v; want a terminal ErrRootOnly", done, err)
	}
	got := h.get()
	assertCondition(t, got, commonv1alpha1.ConditionReady, metav1.ConditionFalse, commonv1alpha1.ReasonRootOnlyKind)
	assertCondition(t, got, commonv1alpha1.ConditionSynced, metav1.ConditionFalse, commonv1alpha1.ReasonRootOnlyKind)
	if reads, writes := h.store.counts(); reads+writes != 0 {
		t.Fatalf("reads %d, writes %d; want no OpenBao call", reads, writes)
	}

	h = newHarness(t, rootOnlyAdapter{}, newResource(testName))
	obj = h.get()
	p = &pass[*testResource]{obj: obj, before: obj.DeepCopyObject().(client.Object), bao: &fakeBao{store: h.store}}
	if _, done, err := h.r.refuseRootOnly(context.Background(), p); done || err != nil {
		t.Fatalf("refuseRootOnly in the root namespace = done %v, err %v; want neither", done, err)
	}
}
