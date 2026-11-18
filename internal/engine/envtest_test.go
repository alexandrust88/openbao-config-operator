package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	corecontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/core"
	syscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	envNamespace = "engine-env"
	envPolicy    = "owned"
	envText      = `path "a/*" { capabilities = ["read"] }`
)

// memBao is an in-memory OpenBao that counts writes per path.
type memBao struct {
	mu     sync.Mutex
	data   map[string]map[string]any
	writes map[string]int
}

func (m *memBao) Read(_ context.Context, path string) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.data[path]
	if !ok {
		return nil, openbao.ErrNotFound
	}
	return maps.Clone(d), nil
}

func (m *memBao) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[path] = maps.Clone(data)
	m.writes[path]++
	return nil, nil
}

func (m *memBao) Delete(_ context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, path)
	return nil
}

func (m *memBao) List(context.Context, string) ([]string, error) { return nil, openbao.ErrNotFound }

func (m *memBao) WithNamespace(string) openbao.Client { return m }

func (m *memBao) Namespace() string { return "" }

func (m *memBao) policy(path string) (string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	text, _ := m.data[path]["policy"].(string)
	return text, m.writes[path]
}

// memConns hands out bao for a BaoConnection whose Ready condition is True, as the real resolver does.
type memConns struct {
	bao    *memBao
	reader client.Reader
}

func (c memConns) For(ctx context.Context, ns string, ref commonv1alpha1.ConnectionReference) (openbao.Client, error) {
	conn := &corev1alpha1.BaoConnection{}
	if err := c.reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, conn); err != nil {
		return nil, fmt.Errorf("%w: %w", engine.ErrConnectionNotReady, err)
	}
	if !meta.IsStatusConditionTrue(conn.Status.Conditions, commonv1alpha1.ConditionReady) {
		return nil, fmt.Errorf("%w: %s", engine.ErrConnectionNotReady, ref.Name)
	}
	return c.bao, nil
}

// TestEnvtestEngine runs the ACLPolicy and BaoEndpoint controllers against a real API server, so CRD defaults, the
// cross-kind path index and the annotation predicate are exercised as in a cluster. One manager serves every
// case because controller names are unique per process.
func TestEnvtestEngine(t *testing.T) {
	root := filepath.Join("..", "..")
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		assets = filepath.Join(root, "bin", "k8s", "1.35.0-darwin-arm64")
	}
	if _, err := os.Stat(filepath.Join(assets, "kube-apiserver")); err != nil {
		t.Skip("envtest binaries not found")
	}
	env := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		ErrorIfCRDPathMissing: true,
		CRDDirectoryPaths: []string{
			filepath.Join(root, "config", "crd", "bases", "sys.obco.dev_aclpolicies.yaml"),
			filepath.Join(root, "config", "crd", "bases", "sys.obco.dev_authmounts.yaml"),
			filepath.Join(root, "config", "crd", "bases", "core.obco.dev_baoendpoints.yaml"),
			filepath.Join(root, "config", "crd", "bases", "core.obco.dev_baoconnections.yaml"),
			filepath.Join(root, "config", "crd", "bases", "core.obco.dev_clusterbaoconnections.yaml"),
		},
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, sysv1alpha1.AddToScheme, corev1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	bao := &memBao{data: map[string]map[string]any{}, writes: map[string]int{}}
	opts := engine.Options{DriftIntervalCritical: time.Second, DriftIntervalStandard: time.Second}
	conns := memConns{bao: bao, reader: mgr.GetAPIReader()}
	if err := syscontroller.SetupACLPolicy(mgr, conns, opts); err != nil {
		t.Fatal(err)
	}
	if err := corecontroller.SetupBaoEndpoint(mgr, conns, opts); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager: %v", err)
		}
	})
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: envNamespace}}); err != nil {
		t.Fatal(err)
	}
	envConnection(t, c, "bao", true)

	policy := &sysv1alpha1.ACLPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: envPolicy, Namespace: envNamespace},
		Spec: sysv1alpha1.ACLPolicySpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "bao"}},
			Policy:       envText,
		},
	}
	if err := c.Create(ctx, policy); err != nil {
		t.Fatal(err)
	}
	policyPath := "sys/policies/acl/" + envPolicy
	envWaitReason(t, c, policy, commonv1alpha1.ReasonReconcileSuccess)

	t.Run("creationPolicy defaults to Create", func(t *testing.T) {
		if policy.Spec.CreationPolicy != commonv1alpha1.CreationPolicyCreate {
			t.Fatalf("creationPolicy = %q", policy.Spec.CreationPolicy)
		}
	})

	t.Run("deletionPolicy defaults to Orphan and adds no finalizer", func(t *testing.T) {
		if policy.Spec.DeletionPolicy != commonv1alpha1.DeletionPolicyOrphan || len(policy.Finalizers) != 0 {
			t.Fatalf("deletionPolicy = %q, finalizers = %v", policy.Spec.DeletionPolicy, policy.Finalizers)
		}
	})

	t.Run("connectionRef and openbaoNamespace are immutable", func(t *testing.T) {
		for msg, mutate := range map[string]func(*sysv1alpha1.ACLPolicy){
			"connectionRef is immutable":    func(p *sysv1alpha1.ACLPolicy) { p.Spec.ConnectionRef.Name = "other" },
			"openbaoNamespace is immutable": func(p *sysv1alpha1.ACLPolicy) { p.Spec.Namespace = "team-a" },
		} {
			changed := &sysv1alpha1.ACLPolicy{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(policy), changed); err != nil {
				t.Fatal(err)
			}
			mutate(changed)
			if err := c.Update(ctx, changed); err == nil || !strings.Contains(err.Error(), msg) {
				t.Errorf("update: err = %v, want %q", err, msg)
			}
		}
	})

	t.Run("a newer resource of another kind on the same path gets Conflict", func(t *testing.T) {
		data, _ := json.Marshal(map[string]any{"policy": "path \"*\" { capabilities = [\"sudo\"] }"})
		endpoint := &corev1alpha1.BaoEndpoint{
			ObjectMeta: metav1.ObjectMeta{Name: "takeover", Namespace: envNamespace},
			Spec: corev1alpha1.BaoEndpointSpec{
				ResourceSpec: commonv1alpha1.ResourceSpec{
					ConnectionRef:  commonv1alpha1.ConnectionReference{Name: "bao"},
					CreationPolicy: commonv1alpha1.CreationPolicyAdopt,
				},
				Path: policyPath,
				Data: &runtime.RawExtension{Raw: data},
			},
		}
		if err := c.Create(ctx, endpoint); err != nil {
			t.Fatal(err)
		}
		envWaitReason(t, c, endpoint, commonv1alpha1.ReasonConflict)
		if text, _ := bao.policy(policyPath); text != envText {
			t.Fatalf("the BaoEndpoint overwrote the ACLPolicy's object: %q", text)
		}
	})

	t.Run("a path with .. segments is refused without an OpenBao write", func(t *testing.T) {
		envRefusesUncleanPath(ctx, t, c, bao)
	})

	t.Run("deleting a BaoEndpoint needs the destructive-delete annotation", func(t *testing.T) {
		envGuardsEndpointDelete(ctx, t, c, bao)
	})

	t.Run("the pause annotation stops and resumes reconciling", func(t *testing.T) {
		envPatch(t, c, policy, func(p *sysv1alpha1.ACLPolicy) {
			p.Annotations = map[string]string{commonv1alpha1.AnnotationPaused: "true"}
		})
		envWaitReason(t, c, policy, commonv1alpha1.ReasonPaused)
		_, writes := bao.policy(policyPath)
		envPatch(t, c, policy, func(p *sysv1alpha1.ACLPolicy) { p.Spec.Policy = envText + "\n# paused" })
		time.Sleep(2 * time.Second)
		if text, now := bao.policy(policyPath); now != writes || text != envText {
			t.Fatalf("paused ACLPolicy wrote: %d -> %d", writes, now)
		}
		envPatch(t, c, policy, func(p *sysv1alpha1.ACLPolicy) { delete(p.Annotations, commonv1alpha1.AnnotationPaused) })
		envWaitReason(t, c, policy, commonv1alpha1.ReasonReconcileSuccess)
		if text, _ := bao.policy(policyPath); text != envText+"\n# paused" {
			t.Fatalf("resumed ACLPolicy did not write the spec: %q", text)
		}
	})

	t.Run("a resource is Ready within 2s of its connection", func(t *testing.T) {
		envConnection(t, c, "late", false)
		late := &sysv1alpha1.ACLPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "late", Namespace: envNamespace},
			Spec: sysv1alpha1.ACLPolicySpec{
				ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "late"}},
				Policy:       envText,
			},
		}
		if err := c.Create(ctx, late); err != nil {
			t.Fatal(err)
		}
		envWaitReason(t, c, late, commonv1alpha1.ReasonConnectionNotReady)
		envConnection(t, c, "late", true)
		start := time.Now()
		envWaitReason(t, c, late, commonv1alpha1.ReasonReconcileSuccess)
		if waited := time.Since(start); waited > 2*time.Second {
			t.Fatalf("Ready %v after the connection, want <= 2s", waited)
		}
	})
}

// envConnection creates the BaoConnection name, or updates it, with the given Ready status.
func envConnection(t *testing.T, c client.Client, name string, ready bool) {
	t.Helper()
	ctx := context.Background()
	conn := &corev1alpha1.BaoConnection{}
	key := client.ObjectKey{Namespace: envNamespace, Name: name}
	if err := c.Get(ctx, key, conn); err != nil {
		conn = &corev1alpha1.BaoConnection{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: envNamespace},
			Spec: corev1alpha1.BaoConnectionSpec{
				ConnectionSpec: corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: "server"}},
				Auth: corev1alpha1.ConnectionAuth{Token: &corev1alpha1.TokenAuth{
					SecretRef: commonv1alpha1.SecretKeySelector{Name: "token", Key: "token"},
				}},
			},
		}
		if err := c.Create(ctx, conn); err != nil {
			t.Fatal(err)
		}
	}
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&conn.Status.Conditions, metav1.Condition{
		Type: commonv1alpha1.ConditionReady, Status: status, Reason: "Test", ObservedGeneration: conn.Generation,
	})
	if err := c.Status().Update(ctx, conn); err != nil {
		t.Fatal(err)
	}
}

func envPatch(t *testing.T, c client.Client, p *sysv1alpha1.ACLPolicy, mutate func(*sysv1alpha1.ACLPolicy)) {
	t.Helper()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	base := p.DeepCopy()
	mutate(p)
	if err := c.Patch(context.Background(), p, client.MergeFrom(base)); err != nil {
		t.Fatal(err)
	}
}

// envWaitReason waits until the Synced condition of obj carries reason.
func envWaitReason(t *testing.T, c client.Client, obj engine.Object, reason string) {
	t.Helper()
	var last string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal(err)
		}
		synced := meta.FindStatusCondition(obj.GetResourceStatus().Conditions, commonv1alpha1.ConditionSynced)
		if synced != nil && synced.Reason == reason {
			return
		}
		last = fmt.Sprintf("%+v", synced)
	}
	t.Fatalf("Synced never reached %s; last %s", reason, last)
}

// envRefusesUncleanPath proves the engine refuses a path with ".." segments before any OpenBao write.
func envRefusesUncleanPath(ctx context.Context, t *testing.T, c client.Client, bao *memBao) {
	t.Helper()
	const escape = "secret/../sys/policies/acl/escape"
	endpoint := &corev1alpha1.BaoEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "escape", Namespace: envNamespace},
		Spec: corev1alpha1.BaoEndpointSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "bao"}},
			Path:         escape,
			Data:         &runtime.RawExtension{Raw: []byte(`{"policy":"path \"*\" { capabilities = [\"sudo\"] }"}`)},
		},
	}
	if err := c.Create(ctx, endpoint); err != nil {
		t.Fatalf("create: %v (the CRD admits the path; the engine is the control)", err)
	}
	envWaitReason(t, c, endpoint, commonv1alpha1.ReasonInvalidSpec)
	for _, path := range []string{escape, "sys/policies/acl/escape"} {
		if _, writes := bao.policy(path); writes != 0 {
			t.Fatalf("%d writes to %s", writes, path)
		}
	}
}

// envGuardsEndpointDelete proves a BaoEndpoint delete waits for the destructive-delete annotation.
func envGuardsEndpointDelete(ctx context.Context, t *testing.T, c client.Client, bao *memBao) {
	t.Helper()
	const mount = "sys/mounts/guarded"
	endpoint := &corev1alpha1.BaoEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "guarded", Namespace: envNamespace},
		Spec: corev1alpha1.BaoEndpointSpec{
			ResourceSpec: commonv1alpha1.ResourceSpec{
				ConnectionRef:  commonv1alpha1.ConnectionReference{Name: "bao"},
				DeletionPolicy: commonv1alpha1.DeletionPolicyDelete,
			},
			Path: mount,
			Data: &runtime.RawExtension{Raw: []byte(`{"type":"kv"}`)},
		},
	}
	if err := c.Create(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	envWaitReason(t, c, endpoint, commonv1alpha1.ReasonReconcileSuccess)
	if err := c.Delete(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	envWaitReason(t, c, endpoint, commonv1alpha1.ReasonDeleteGuarded)
	if _, err := bao.Read(ctx, mount); err != nil {
		t.Fatalf("guarded delete removed the mount: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(endpoint), endpoint); err != nil {
		t.Fatal(err)
	}
	base := endpoint.DeepCopy()
	endpoint.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
	if err := c.Patch(ctx, endpoint, client.MergeFrom(base)); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, err := bao.Read(ctx, mount); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the annotated delete did not remove the mount")
		}
	}
}
