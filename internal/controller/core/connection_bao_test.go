package core

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

func startDevBao(t *testing.T) string {
	t.Helper()
	return testenv.StartBao(t, testenv.RequireBao(t), "root")
}

func TestReconcileAgainstDevServer(t *testing.T) {
	address := startDevBao(t)
	server := unprobedServer()
	server.Spec.Address, server.Spec.AllowInsecureHTTP = address, true
	conn, secret := tokenConnection()
	secret.Data[tokenKey] = []byte("root\n")
	pool := openbao.NewPool()
	r := newConnectionReconciler(t, pool, server, tenantNamespace(teamA), conn, secret)
	sr := &serverReconciler{Client: r.Client, resolver: r.resolver, probe: openbao.Probe}

	_, probed := reconcileServer(t, sr)
	if !meta.IsStatusConditionTrue(probed.Status.Conditions, commonv1alpha1.ConditionReady) ||
		probed.Status.Version == "" || probed.Status.Sealed {
		t.Fatalf("server not Ready: %+v", probed.Status)
	}
	_, got := reconcileAndGet(t, r)
	s := got.Status
	if !meta.IsStatusConditionTrue(s.Conditions, commonv1alpha1.ConditionReady) {
		t.Fatalf("not Ready: %+v", s.Conditions)
	}
	if !s.Authenticated || s.Version != probed.Status.Version || s.Sealed {
		t.Errorf("unexpected status %+v", s)
	}
	if _, err := r.resolver.For(context.Background(), teamA, commonv1alpha1.ConnectionReference{Name: connName}); err != nil {
		t.Errorf("For after Ready: %v", err)
	}
}
