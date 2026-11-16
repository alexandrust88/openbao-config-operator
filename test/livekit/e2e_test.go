//go:build integration

package livekit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	defaultE2EOverlays = "root/minimal,root/full,child/minimal,child/full"
	modeChild          = "child"
	overlayObserve     = "observe/"
	settleGrace        = 20 * time.Second
)

// run is one overlay applied to its own `bao server -dev`.
type run struct {
	overlay, mode, prefix string
	bao                   *bao
	objs                  []*unstructured.Unstructured
	rows                  []row
	ready, excused        int
}

// row is the final state of one object of a run.
type row struct {
	kind, name string
	state      objState
	excuse     string
}

// TestEndToEnd applies overlays (LIVEKIT_OVERLAYS, default root and child in both variants) each to its own
// `bao server -dev` through the manager built from ./cmd, and asserts that every object reaches Ready unless a file
// in test/livekit/exceptions excuses it. The connections are rewritten to AppRole logins (operator at the root,
// tenant inside tenantNamespace for the child mode); URL CRLs use a local server and plugins use built test binaries.
// Other fields are applied as rendered. observe/<variant>
// flips the objects of root/<variant> to Observe once they are settled. LIVEKIT_COMPONENTS limits the scenarios.
func TestEndToEnd(t *testing.T) {
	ctrllog.SetLogger(logr.Discard())
	inventory := liveInventory(t)
	root := repoRoot(t)
	kit := kitDir(root)
	kustomize := kustomizeBin(t, root)
	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)
	exc := loadExceptions(t)
	p := params(t, kit)
	ns := p["kubernetesNamespace"]
	logDir := envOr("LIVEKIT_LOG_DIR", t.TempDir())
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		t.Fatal(err)
	}
	out := envOr("LIVEKIT_OUT", filepath.Join(root, ".tmp", "livekit", "e2e.md"))
	timeout, err := time.ParseDuration(envOr("LIVEKIT_TIMEOUT", "6m"))
	if err != nil {
		t.Fatalf("LIVEKIT_TIMEOUT: %v", err)
	}
	filter := regexp.MustCompile(os.Getenv("LIVEKIT_COMPONENTS"))

	requested := strings.Split(envOr("LIVEKIT_OVERLAYS", defaultE2EOverlays), ",")
	var runs []*run
	var observe []string
	for _, o := range requested {
		o = strings.TrimSpace(o)
		if variant, ok := strings.CutPrefix(o, overlayObserve); ok {
			observe = append(observe, "root/"+variant)
			continue
		}
		runs = append(runs, &run{overlay: o})
	}
	for _, o := range observe {
		if !slices.ContainsFunc(runs, func(r *run) bool { return r.overlay == o }) {
			runs = append(runs, &run{overlay: o})
		}
	}

	managerBin := buildManager(t, root)
	c := startCluster(t, assets, filepath.Join(root, "config", "crd", "bases"), logDir)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := c.k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}
	createInputSecrets(t, kit, assets, c.kubeconfig, ns)

	var pluginBin string
	for _, r := range runs {
		dir := filepath.Join(kit, "overlays", r.overlay)
		r.mode = modeOf(t, dir)
		r.prefix = overlayPrefix(t, dir)
		if os.Getenv("LIVEKIT_COMPONENTS") != "" {
			dir = filteredOverlay(t, kit, dir, filter)
		}
		r.bao = startBao(t, baoBin, logDir, strings.ReplaceAll(r.overlay, "/", "-"))
		operator, tenant, err := r.bao.prepare(p["tenantNamespace"])
		if err != nil {
			t.Fatalf("prepare bao for %s: %v", r.overlay, err)
		}
		login, mount := operator, operatorMount
		if r.mode == modeChild {
			login, mount = tenant, tenantMount
		}
		secret := r.prefix + "livekit-approle"
		if err := c.k8s.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secret, Namespace: ns},
			Data:       map[string][]byte{"role-id": []byte(login.roleID), "secret-id": []byte(login.secretID)},
		}); err != nil {
			t.Fatal(err)
		}
		for _, o := range render(t, kustomize, dir) {
			if !isOperatorKind(o) {
				continue
			}
			rewriteConnection(o, r.bao.addr, mount, secret, ns)
			r.objs = append(r.objs, o)
		}
		prepareCRLURLs(t, r.objs)
		preparePluginFixtures(t, root, logDir, r, &pluginBin)
	}

	startManager(t, managerBin, c.kubeconfig, ns, filepath.Join(logDir, "manager.log"), 30*time.Second)
	t.Logf("logs in %s", logDir)
	for _, r := range runs {
		for _, o := range r.objs {
			if err := c.k8s.Create(ctx, o.DeepCopy()); err != nil {
				t.Errorf("%s: create %s/%s: %v", r.overlay, o.GetKind(), o.GetName(), err)
			}
		}
	}
	settle(ctx, c.k8s, runs, exc, timeout)
	for _, r := range runs {
		evaluate(ctx, c.k8s, r, r.overlay, exc)
		verifyOutputSecrets(ctx, t, c.k8s, r)
		verifyPausedPolicies(ctx, t, c.k8s, r)
		verifyPluginFixtures(t, r)
	}

	observed := make([]*run, 0, len(observe))
	for _, o := range observe {
		i := slices.IndexFunc(runs, func(r *run) bool { return r.overlay == o })
		src := runs[i]
		r := &run{overlay: overlayObserve + strings.TrimPrefix(o, "root/"), mode: src.mode, prefix: src.prefix,
			bao: src.bao, objs: src.objs}
		for _, obj := range r.objs {
			if !managed(obj) {
				continue
			}
			patch := []byte(`{"spec":{"managementPolicy":"Observe"}}`)
			if err := c.k8s.Patch(ctx, obj.DeepCopy(), client.RawPatch("application/merge-patch+json", patch)); err != nil {
				t.Errorf("%s: patch %s/%s: %v", r.overlay, obj.GetKind(), obj.GetName(), err)
			}
		}
		observed = append(observed, r)
	}
	if len(observed) > 0 {
		time.Sleep(5 * time.Second)
		settle(ctx, c.k8s, observed, exc, timeout)
		for _, r := range observed {
			evaluate(ctx, c.k8s, r, r.overlay, exc)
		}
		runs = append(runs, observed...)
	}

	report(t, out, runs)
	reportLiveInventory(t, out, inventory, runs, requested)
	for _, r := range runs {
		for _, row := range r.rows {
			if !row.state.ready && row.excuse == "" {
				t.Errorf("%s: %s/%s not Ready: %s: %s", r.overlay, row.kind, row.name, row.state.reason, row.state.message)
			}
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// modeOf returns the mode component an overlay uses (modes/<mode>), or the mode of the overlay it builds on.
func modeOf(t testing.TB, dir string) string {
	t.Helper()
	k := readKustomization(t, dir)
	for _, c := range k.Components {
		if _, mode, ok := strings.Cut(c, "modes/"); ok {
			return strings.TrimSuffix(mode, "/")
		}
	}
	for _, r := range k.Resources {
		sub := filepath.Join(dir, r)
		if _, err := os.Stat(filepath.Join(sub, "kustomization.yaml")); err == nil && filepath.Base(sub) != "params" {
			return modeOf(t, sub)
		}
	}
	t.Fatalf("%s: no modes/<mode> component", dir)
	return ""
}

type kustomizationFile struct {
	Resources  []string `json:"resources"`
	Components []string `json:"components"`
	Labels     []struct {
		Pairs map[string]string `json:"pairs"`
	} `json:"labels"`
}

func readKustomization(t testing.TB, dir string) kustomizationFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var k kustomizationFile
	if err := yaml.Unmarshal(raw, &k); err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	return k
}

// filteredOverlay writes a copy of an overlay whose set components are replaced by the scenario components that
// match filter; kustomize refuses absolute paths, so every path is made relative to the copy.
func filteredOverlay(t *testing.T, kit, dir string, filter *regexp.Regexp) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var k map[string]any
	if err := yaml.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	abs := func(list any) []any {
		var out []any
		items, _ := list.([]any)
		for _, item := range items {
			s, _ := item.(string)
			path := filepath.Clean(filepath.Join(dir, s))
			if rel, err := filepath.Rel(filepath.Join(kit, "sets"), path); err == nil && !strings.HasPrefix(rel, "..") {
				for _, c := range readKustomization(t, path).Components {
					cdir := filepath.Clean(filepath.Join(path, c))
					if filter.MatchString(filepath.Base(filepath.Dir(cdir))) {
						out = append(out, cdir)
					}
				}
				continue
			}
			out = append(out, path)
		}
		return out
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rel := func(list []any) []any {
		for i, item := range list {
			r, err := filepath.Rel(tmp, item.(string))
			if err != nil {
				t.Fatal(err)
			}
			list[i] = r
		}
		return list
	}
	k["resources"] = rel(abs(k["resources"]))
	k["components"] = rel(abs(k["components"]))
	out, err := yaml.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "kustomization.yaml"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	return tmp
}

// rewriteConnection points the kit's OpenBaoServer at the dev server and makes its connections log in with the
// AppRole Secret instead of the Kubernetes auth the kit declares.
func rewriteConnection(o *unstructured.Unstructured, addr, mount, secret, ns string) {
	switch o.GetKind() {
	case "OpenBaoServer":
		_ = unstructured.SetNestedField(o.Object, addr, keySpec, "address")
		_ = unstructured.SetNestedField(o.Object, true, keySpec, "allowInsecureHTTP")
		unstructured.RemoveNestedField(o.Object, keySpec, "caBundle")
	case "OpenBaoConnection", "ClusterOpenBaoConnection":
		ref := func(key string) map[string]any {
			r := map[string]any{keyName: secret, "key": key}
			if o.GetKind() == "ClusterOpenBaoConnection" {
				r["namespace"] = ns
			}
			return r
		}
		_ = unstructured.SetNestedField(o.Object, map[string]any{"appRole": map[string]any{
			"mountPath": mount, "roleIDSecretRef": ref("role-id"), "secretIDSecretRef": ref("secret-id"),
		}}, keySpec, "auth")
	}
}

// createInputSecrets runs create-secrets.sh against envtest with the kubectl of the envtest assets.
func createInputSecrets(t *testing.T, kit, assets, kubeconfig, ns string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(kit, "create-secrets.sh"), "--namespace", ns)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig, "PATH="+assets+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create-secrets.sh: %v\n%s", err, out)
	}
}

// settle waits until every object of every run is Ready or excused, or the timeout passes, and then for a grace
// period, so an excused object reports its final reason rather than a transient one such as a missing mount.
func settle(ctx context.Context, k8s client.Client, runs []*run, exc exceptions, timeout time.Duration) {
	defer time.Sleep(settleGrace)
	_ = poll(timeout, func() error {
		for _, r := range runs {
			for _, o := range r.objs {
				s := current(ctx, k8s, o)
				if !s.ready && excuse(exc, r, o) == "" {
					return fmt.Errorf("%s/%s not settled", o.GetKind(), o.GetName())
				}
			}
		}
		return nil
	})
}

func current(ctx context.Context, k8s client.Client, o *unstructured.Unstructured) objState {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(o.GroupVersionKind())
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(o), cur); err != nil {
		if apierrors.IsNotFound(err) {
			return objState{reason: "NotFound", message: "not created"}
		}
		return objState{reason: "GetFailed", message: err.Error()}
	}
	return stateOf(cur)
}

func excuse(exc exceptions, r *run, o *unstructured.Unstructured) string {
	name := strings.TrimPrefix(o.GetName(), r.prefix)
	overlay := r.overlay
	for _, e := range exc.NotReady {
		if e.matches(overlay, o.GetKind(), name) {
			return e.Reason
		}
	}
	return ""
}

func evaluate(ctx context.Context, k8s client.Client, r *run, overlay string, exc exceptions) {
	r.rows, r.ready, r.excused = nil, 0, 0
	for _, o := range r.objs {
		s := current(ctx, k8s, o)
		rw := row{kind: o.GetKind(), name: o.GetName(), state: s}
		switch {
		case s.ready:
			r.ready++
		default:
			if rw.excuse = excuse(exc, &run{overlay: overlay, prefix: r.prefix}, o); rw.excuse != "" {
				r.excused++
			}
		}
		r.rows = append(r.rows, rw)
	}
	sort.Slice(r.rows, func(i, j int) bool {
		if r.rows[i].kind != r.rows[j].kind {
			return r.rows[i].kind < r.rows[j].kind
		}
		return r.rows[i].name < r.rows[j].name
	})
}

func report(t *testing.T, out string, runs []*run) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# live kit end-to-end\n\ngenerated %s by `make test-livekit` against `bao server -dev`, envtest and "+
		"the manager built from ./cmd.\n\n| overlay | objects | ready | excused | failed |\n|---|---|---|---|---|\n",
		time.Now().Format(time.RFC3339))
	for _, r := range runs {
		failed := len(r.rows) - r.ready - r.excused
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", r.overlay, len(r.rows), r.ready, r.excused, failed)
		t.Logf("%s: %d objects, %d Ready, %d excused, %d failed", r.overlay, len(r.rows), r.ready, r.excused, failed)
	}
	b.WriteString("\n## not Ready\n\n| overlay | kind | name | reason | message | excuse |\n|---|---|---|---|---|---|\n")
	for _, r := range runs {
		for _, rw := range r.rows {
			if rw.state.ready {
				continue
			}
			msg := strings.ReplaceAll(strings.Join(strings.Fields(rw.state.message), " "), "|", "/")
			if len(msg) > 200 {
				msg = msg[:200] + "..."
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", r.overlay, rw.kind, rw.name, rw.state.reason, msg,
				strings.Join(strings.Fields(rw.excuse), " "))
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %s", out)
}
