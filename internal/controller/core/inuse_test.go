package core

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func inUseEndpoint(name string, ref commonv1alpha1.ConnectionReference, finalizers ...string) *corev1alpha1.BaoEndpoint {
	return &corev1alpha1.BaoEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: teamA, Finalizers: finalizers},
		Spec: corev1alpha1.BaoEndpointSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: ref},
			Path:         "sys/policies/acl/" + name,
		},
	}
}

func TestConnectionFinalizerAndReadyGauge(t *testing.T) {
	conn, secret := tokenConnection()
	r := newConnectionReconciler(t, &recordingPool{client: lookupSelfClient{}}, testServer(), tenantNamespace(teamA), conn, secret)
	_, got := reconcileAndGet(t, r)
	if !controllerutil.ContainsFinalizer(got, connectionFinalizer) {
		t.Errorf("finalizers = %v, want %s", got.Finalizers, connectionFinalizer)
	}
	gauge := connectionReady.WithLabelValues(string(commonv1alpha1.ConnectionKindNamespaced), teamA, connName)
	if v := testutil.ToFloat64(gauge); v != 1 {
		t.Errorf("obco_connection_ready = %v, want 1", v)
	}

	if err := r.Delete(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	reconcileAndGet(t, r)
	if v := testutil.ToFloat64(gauge); v != 0 {
		t.Errorf("obco_connection_ready = %v, want 0 once the credentials are gone", v)
	}
}

func TestConnectionHeldWhileInUse(t *testing.T) {
	ctx := context.Background()
	conn, secret := tokenConnection()
	conn.Finalizers = []string{connectionFinalizer}
	holder := inUseEndpoint("holder", commonv1alpha1.ConnectionReference{Name: connName}, engine.DefaultFinalizer)
	pool := &recordingPool{client: lookupSelfClient{}}
	r := newConnectionReconciler(t, pool, testServer(), tenantNamespace(teamA), conn, secret, holder,
		inUseEndpoint("no-finalizer", commonv1alpha1.ConnectionReference{Name: connName}),
		inUseEndpoint("cluster-ref", commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster,
			Name: connName}, engine.DefaultFinalizer))
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(conn)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}

	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	current := &corev1alpha1.BaoConnection{}
	if err := r.Get(ctx, req.NamespacedName, current); err != nil {
		t.Fatalf("connection released while a resource holds it: %v", err)
	}
	if res.RequeueAfter != inUseRecheck || len(pool.invalidated) != 0 {
		t.Errorf("result = %+v, invalidated = %v; want a recheck and the client kept", res, pool.invalidated)
	}

	if err := r.Get(ctx, client.ObjectKeyFromObject(holder), holder); err != nil {
		t.Fatal(err)
	}
	controllerutil.RemoveFinalizer(holder, engine.DefaultFinalizer)
	if err := r.Update(ctx, holder); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, req.NamespacedName, current); !apierrors.IsNotFound(err) {
		t.Errorf("get after release: err = %v, want NotFound", err)
	}
	if len(pool.invalidated) != 1 {
		t.Errorf("invalidated = %v, want the released connection's client dropped", pool.invalidated)
	}
	if connectionReady.DeleteLabelValues(string(commonv1alpha1.ConnectionKindNamespaced), teamA, connName) {
		t.Error("obco_connection_ready series left after the release")
	}
}

func TestServerHeldWhileConnectionsExist(t *testing.T) {
	ctx := context.Background()
	server := testServer()
	server.Finalizers = []string{serverFinalizer}
	conn, _ := tokenConnection()
	r := newServerReconciler(healthyProbe, server, conn, tenantNamespace(teamA))
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(server)}
	if err := r.Delete(ctx, server); err != nil {
		t.Fatal(err)
	}

	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, req.NamespacedName, server); err != nil || res.RequeueAfter != inUseRecheck {
		t.Fatalf("server released while a connection references it: err = %v, result %+v", err, res)
	}

	if err := r.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, req.NamespacedName, server); !apierrors.IsNotFound(err) {
		t.Errorf("get after release: err = %v, want NotFound", err)
	}
}

func TestServerGetsFinalizer(t *testing.T) {
	_, got := reconcileServer(t, newServerReconciler(healthyProbe, unprobedServer()))
	if !controllerutil.ContainsFinalizer(got, serverFinalizer) {
		t.Errorf("finalizers = %v, want %s", got.Finalizers, serverFinalizer)
	}
}

// TestClusterConnectionIgnoresDeniedHolders proves a resource in a namespace the cluster connection does not admit
// cannot hold the connection with a finalizer it added itself.
func TestClusterConnectionIgnoresDeniedHolders(t *testing.T) {
	ctx := context.Background()
	const outsider = "inuse-outsider"
	conn := &corev1alpha1.ClusterBaoConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Finalizers: []string{connectionFinalizer}},
		Spec: corev1alpha1.ClusterBaoConnectionSpec{
			ConnectionSpec:    corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: openbaoName}},
			AllowedNamespaces: &metav1.LabelSelector{MatchLabels: map[string]string{tenantLabel: "true"}},
		},
	}
	ref := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKindCluster, Name: conn.Name}
	holder := inUseEndpoint("holder", ref, engine.DefaultFinalizer)
	squatter := inUseEndpoint("squatter", ref, engine.DefaultFinalizer)
	squatter.Namespace = outsider
	r := newConnectionReconciler(t, &recordingPool{client: lookupSelfClient{}}, testServer(), conn, holder, squatter,
		tenantNamespace(teamA), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: outsider}})
	r.kind = commonv1alpha1.ConnectionKindCluster
	r.newObject = func() connectionObject { return &corev1alpha1.ClusterBaoConnection{} }
	r.newList = func() client.ObjectList { return &corev1alpha1.ClusterBaoConnectionList{} }
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(conn)}
	if err := r.Delete(ctx, conn); err != nil {
		t.Fatal(err)
	}

	if res, err := r.Reconcile(ctx, req); err != nil || res.RequeueAfter != inUseRecheck {
		t.Fatalf("result = %+v, err = %v; want the admitted holder to keep the connection", res, err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(holder), holder); err != nil {
		t.Fatal(err)
	}
	controllerutil.RemoveFinalizer(holder, engine.DefaultFinalizer)
	if err := r.Update(ctx, holder); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, req.NamespacedName, &corev1alpha1.ClusterBaoConnection{}); !apierrors.IsNotFound(err) {
		t.Errorf("get after release: err = %v, want NotFound: the squatter's finalizer must not count", err)
	}
}

// TestServerIgnoresDeniedConnections proves a BaoConnection in a namespace the server does not admit cannot
// hold the server.
func TestServerIgnoresDeniedConnections(t *testing.T) {
	ctx := context.Background()
	server := testServer()
	server.Finalizers = []string{serverFinalizer}
	conn, _ := tokenConnection()
	conn.Namespace = "inuse-outsider"
	r := newServerReconciler(healthyProbe, server, conn,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: conn.Namespace}})
	if err := r.Delete(ctx, server); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(server)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(server), server); !apierrors.IsNotFound(err) {
		t.Errorf("get after release: err = %v, want NotFound", err)
	}
}
