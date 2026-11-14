package connection

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const litFinalizer = "openbaoconfig.io/connection-in-use"

// TestForDeletingConnectionKeepsLastConfig lets the resources a deleted connection still holds clean up after
// its credentials are gone, as when the whole Namespace is deleted.
func TestForDeletingConnectionKeepsLastConfig(t *testing.T) {
	ctx := context.Background()
	conn := namespacedConn(tokenAuth)
	conn.Finalizers = []string{litFinalizer}
	tokenSecret := secret(teamA, "bao-token", map[string]string{tokenKey: litToken})
	r, pool := newResolver(t, conn, tokenSecret)
	ref := commonv1alpha1.ConnectionReference{Name: connName}

	if _, err := r.For(ctx, teamA, ref); err != nil {
		t.Fatal(err)
	}
	working := pool.cfg.Auth.CacheKey()
	if err := r.Client.Delete(ctx, tokenSecret); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}

	if _, err := r.ForCleanup(ctx, teamA, ref); err != nil {
		t.Fatalf("ForCleanup on a connection being deleted: %v", err)
	}
	if pool.cfg.Auth.CacheKey() != working {
		t.Error("a connection being deleted did not reuse its last working configuration")
	}

	r.Forget(PoolKey(conn))
	_, err := r.ForCleanup(ctx, teamA, ref)
	if !errors.Is(err, engine.ErrConnectionNotReady) || !errors.Is(err, ErrReferenceNotFound) {
		t.Errorf("err = %v, want ErrConnectionNotReady wrapping ErrReferenceNotFound", err)
	}
}

// TestForDeletingConnectionRechecksAdmission keeps the last configuration of a connection being deleted for
// cleanup only: an ordinary reconcile is denied once the server no longer admits the namespace.
func TestForDeletingConnectionRechecksAdmission(t *testing.T) {
	ctx := context.Background()
	conn := namespacedConn(tokenAuth)
	conn.Finalizers = []string{litFinalizer}
	r, _ := newResolver(t, conn, secret(teamA, "bao-token", map[string]string{tokenKey: litToken}))
	ref := commonv1alpha1.ConnectionReference{Name: connName}
	if _, err := r.For(ctx, teamA, ref); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}
	server := &corev1alpha1.OpenBaoServer{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: serverName}, server); err != nil {
		t.Fatal(err)
	}
	server.Spec.AllowedNamespaces = &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: "revoked"}}
	if err := r.Client.Update(ctx, server); err != nil {
		t.Fatal(err)
	}

	if _, err := r.For(ctx, teamA, ref); !errors.Is(err, engine.ErrConnectionDenied) {
		t.Errorf("reconcile through a deleting connection the server no longer admits: err = %v, want ErrConnectionDenied", err)
	}
	if _, err := r.ForCleanup(ctx, teamA, ref); err != nil {
		t.Errorf("cleanup through a deleting connection: %v", err)
	}
}

func TestForStillRefusesDeletingConnectionThatWasNotReady(t *testing.T) {
	conn := namespacedConn(tokenAuth)
	conn.Finalizers = []string{litFinalizer}
	conn.Status.Conditions = nil
	r, _ := newResolver(t, conn)
	if err := r.Client.Delete(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	_, err := r.For(context.Background(), teamA, commonv1alpha1.ConnectionReference{Name: connName})
	if !errors.Is(err, engine.ErrConnectionNotReady) {
		t.Errorf("err = %v, want ErrConnectionNotReady", err)
	}
}

func TestServerFor(t *testing.T) {
	server := tenantServer()
	server.Annotations = map[string]string{commonv1alpha1.AnnotationPaused: litTrue}
	r, _ := newResolver(t, server, namespacedConn(tokenAuth))
	ctx := context.Background()

	got, err := r.ServerFor(ctx, teamA, commonv1alpha1.ConnectionReference{Name: connName})
	if err != nil {
		t.Fatal(err)
	}
	if got != (engine.Server{Key: serverName, Paused: true}) {
		t.Errorf("ServerFor = %+v, want key %s and paused", got, serverName)
	}
	if _, err := r.ServerFor(ctx, teamA, commonv1alpha1.ConnectionReference{Name: "absent"}); !errors.Is(err,
		engine.ErrConnectionNotReady) {
		t.Errorf("missing connection: err = %v, want ErrConnectionNotReady", err)
	}
}

func TestConnectionKey(t *testing.T) {
	for want, ref := range map[string]commonv1alpha1.ConnectionReference{
		"OpenBaoConnection/team-a/bao":   {Name: connName},
		"ClusterOpenBaoConnection/bao":   {Kind: commonv1alpha1.ConnectionKindCluster, Name: connName},
		"OpenBaoConnection/team-a/other": {Kind: commonv1alpha1.ConnectionKindNamespaced, Name: "other"},
	} {
		if got := engine.ConnectionKey(teamA, ref); got != want {
			t.Errorf("engine.ConnectionKey(%+v) = %q, want %q", ref, got, want)
		}
	}
}
