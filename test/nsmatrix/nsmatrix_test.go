//go:build integration

// Package nsmatrix runs every built-in kind through each way a connection can reach an OpenBao namespace, against
// envtest, the manager built from ./cmd and one `bao server -dev` per mode (namespaces team-a, team-a/apps and
// team-b in each), and writes the kind x mode matrix to .tmp/night/ns-matrix.md (NSM_OUT).
//
// U1 logs in at the root and operates in the root (u1a), in team-a and team-a/apps through spec.namespace (u1b),
// and in team-a through a connection based there with authNamespace "" (u1c). U2 logs in through an AppRole mount
// inside team-a and operates in team-a and team-a/apps (u2); its negatives point the same team-a login at the root
// and at team-b and must be refused without a write. Every operator request carries the attribution header, which
// the declarative audit device of each server logs, so the suite proves per object where it wrote and that it wrote
// nowhere else.
package nsmatrix

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

const (
	nsmSettleQuiet  = 90 * time.Second
	nsmSettleMax    = 6 * time.Minute
	nsmKindKeyRot   = "KeyRotationConfig"
	nsmKindAccLevel = "AccessLevel"
	nsmFail         = "FAIL"
)

// nsmRootOnly are the kinds whose endpoints OpenBao 2.7.2 serves only in the root namespace; the probe phase
// re-measures this on every run and fails when the server and this list disagree.
var nsmRootOnly = map[string]bool{
	"Audit": true, "AuditRequestHeader": true, "RateLimitQuota": true, "QuotaConfig": true, "Plugin": true,
	"CORSConfig": true, "UIHeader": true, "Logger": true, "RaftAutopilot": true,
}

// nsmExceptions are the kinds with no example that gets Ready in the root namespace of `bao server -dev`, so the
// child modes have no control to compare with. Any other kind without a Ready example fails the matrix.
var nsmExceptions = map[string]string{
	"Audit":                   "bao -dev refuses API-created audit devices (unsafe_allow_api_audit_creation off)",
	"RaftAutopilot":           "needs raft storage; bao -dev runs on inmem storage",
	"Plugin":                  "bao -dev has no plugin_directory, so the catalog refuses every registration",
	"DatabaseStaticRole":      "needs a verified DatabaseConnection to a reachable database server",
	"RabbitMQConfig":          "OpenBao validates the connection on write; needs a reachable RabbitMQ",
	"KubernetesAuthConfig":    "needs a real cluster CA certificate; the example carries a placeholder",
	"KubernetesSecretsConfig": "needs a reachable Kubernetes API (kubernetes_host)",
	"KubernetesSecretsRole":   "needs a KubernetesSecretsConfig, which needs a reachable Kubernetes API",
	"PKIACMEConfig": "its fixture's referents (PKIIssuer obco-int-2026, PKIRole obco-acme in mount pki) share names " +
		"with the PKIIssuer and PKIRole examples in other mounts, and all kinds are applied together",
}

// nsmMode is one way of reaching OpenBao, with its own server, Kubernetes namespace and connection.
type nsmMode struct {
	name, title   string
	k8sNS         string
	connNamespace string
	authNamespace *string
	tenantLogin   bool
	specNS        string
	// expect is the absolute OpenBao namespace objects must land in; for a negative mode it is the namespace the
	// team-a login must not reach.
	expect   string
	negative bool

	bao              *nsmBao
	operator, tenant approle
	created          []*unstructured.Unstructured
	createErrs       map[string][]string
	states           map[string]nsmCellState
	audit            []auditEntry
}

func nsmModes() []*nsmMode {
	root := ""
	teamA := nsmTeamA
	return []*nsmMode{
		{name: "u1a", title: "U1a root login, root", expect: ""},
		{name: "u1b", title: "U1b root login, spec.namespace team-a", specNS: nsmTeamA, expect: nsmTeamA},
		{name: "u1b-nested", title: "U1b root login, spec.namespace team-a/apps", specNS: nsmTeamAApps, expect: nsmTeamAApps},
		{name: "u1c", title: "U1c connection team-a, authNamespace \"\"", connNamespace: nsmTeamA, authNamespace: &root,
			expect: nsmTeamA},
		{name: "u2", title: "U2 team-a login, team-a", connNamespace: nsmTeamA, tenantLogin: true, expect: nsmTeamA},
		{name: "u2-nested", title: "U2 team-a login, spec.namespace apps", connNamespace: nsmTeamA, tenantLogin: true,
			specNS: "apps", expect: nsmTeamAApps},
		{name: "u2-root", title: "U2 negative: team-a login, connection at root", authNamespace: &teamA,
			tenantLogin: true, expect: "", negative: true},
		{name: "u2-sibling", title: "U2 negative: team-a login, connection team-b", connNamespace: nsmTeamB,
			authNamespace: &teamA, tenantLogin: true, expect: nsmTeamB, negative: true},
	}
}

// nsmEscapes are spec.namespace values that would leave the connection namespace if they were accepted.
var nsmEscapes = []string{"..", "../team-b", "/team-b", "team-a/../team-b", "./team-a", "apps/"}

// nsmCellState is what one object reached, read after the run.
type nsmCellState struct {
	nsmObjState
	statusNS, statusPath string
}

// nsmRow is one kind of the matrix.
type nsmRow struct {
	kind   *nsmKind
	cells  map[string]string
	escape string
	notes  []string
	failed bool
}

func (r *nsmRow) note(format string, args ...any) {
	r.notes = append(r.notes, fmt.Sprintf(format, args...))
}

func (r *nsmRow) fail(mode, format string, args ...any) {
	r.failed = true
	r.cells[mode] = nsmFail
	r.note(mode+": "+format, args...)
}

func TestNamespaceMatrix(t *testing.T) {
	ctrllog.SetLogger(logr.Discard())
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	baoBin := testenv.RequireBao(t)
	assets := testenv.RequireAssets(t, root)
	crdDir := nsmEnvOr("NSM_CRD_DIR", filepath.Join(root, "config", "crd", "bases"))
	logDir := nsmEnvOr("NSM_LOG_DIR", t.TempDir())
	out := nsmEnvOr("NSM_OUT", filepath.Join(root, ".tmp", "night", "ns-matrix.md"))
	drift := 4 * time.Second
	if v := os.Getenv("NSM_DRIFT_INTERVAL"); v != "" {
		if drift, err = time.ParseDuration(v); err != nil || drift <= 0 {
			t.Fatalf("NSM_DRIFT_INTERVAL=%q: want a positive duration", v)
		}
	}
	filter := regexp.MustCompile(os.Getenv("NSM_KINDS"))
	managerBin := os.Getenv("NSM_MANAGER_BIN")
	if managerBin == "" {
		managerBin = filepath.Join(t.TempDir(), "manager")
		build := exec.Command("go", "build", "-o", managerBin, "./cmd/")
		build.Dir = root
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build manager: %v\n%s", err, output)
		}
	}

	catalog := nsmLoadCatalog(t, root, crdDir)
	var rows []*nsmRow
	for _, k := range catalog.kinds {
		if k.plugin() || !filter.MatchString(k.Kind) {
			continue
		}
		rows = append(rows, &nsmRow{kind: k, cells: map[string]string{}})
	}

	modes := nsmModes()
	var wg sync.WaitGroup
	errs := make([]error, len(modes))
	for i, m := range modes {
		m.k8sNS = "nsm-" + m.name
		m.bao = nsmStartBao(t, baoBin, logDir, m.name)
		wg.Go(func() { m.operator, m.tenant, errs[i] = m.bao.prepare() })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("prepare %s: %v", modes[i].name, err)
		}
	}
	probes := nsmRunProbes(t, modes[0].bao)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cluster := nsmStartCluster(t, assets, crdDir, logDir)
	watch := make([]string, 0, len(modes))
	for _, m := range modes {
		watch = append(watch, m.k8sNS)
	}
	nsmStartManager(t, managerBin, cluster.kubeconfig, strings.Join(watch, ","), filepath.Join(logDir, "manager.log"), drift)
	for _, m := range modes {
		if err := nsmConnect(ctx, cluster.k8s, m); err != nil {
			t.Fatalf("connect %s: %v", m.name, err)
		}
	}
	t.Logf("logs in %s, drift interval %s", logDir, drift)

	for _, r := range rows {
		r.escape = nsmEscapeCheck(ctx, cluster.k8s, r.kind, modes[4].k8sNS)
		if strings.HasPrefix(r.escape, nsmFail) {
			r.failed = true
		}
	}
	for _, m := range modes {
		nsmApply(ctx, t, cluster.k8s, catalog, rows, m)
	}
	nsmSettle(ctx, cluster.k8s, modes)
	atRest := time.Now()
	time.Sleep(time.Duration(3.5 * float64(drift)))
	for _, m := range modes {
		if m.audit, err = m.bao.audit(); err != nil {
			t.Fatalf("read audit log of %s: %v", m.name, err)
		}
		for _, o := range m.created {
			m.states[nsmKey(o)] = nsmReadState(ctx, cluster.k8s, o)
		}
	}

	for _, r := range rows {
		for _, m := range modes {
			nsmEvaluate(r, m, modes[0], atRest)
		}
	}
	nsmWriteReport(t, out, modes, rows, probes, drift)
	for _, r := range rows {
		if r.failed {
			t.Errorf("%s: %s", r.kind.Kind, strings.Join(r.notes, "; "))
		}
	}
	for _, p := range probes {
		if p.verdict != "" {
			t.Errorf("probe %s %s: %s", p.kind, p.path, p.verdict)
		}
	}
}

// nsmProbe is one direct request in a child namespace that shows whether a kind's endpoint exists there.
type nsmProbe struct {
	kind, method, path, ns string
	code                   int
	message, verdict       string
	rootOnly               bool
}

// nsmRunProbes reads one endpoint per sys kind in team-a and team-a/apps with the root token and checks that the
// kinds OpenBao serves only in the root namespace are exactly nsmRootOnly.
func nsmRunProbes(t *testing.T, b *nsmBao) []*nsmProbe {
	t.Helper()
	specs := []struct{ kind, method, path string }{
		{"Audit", http.MethodGet, "sys/audit"},
		{"AuditRequestHeader", http.MethodGet, "sys/config/auditing/request-headers"},
		{"RateLimitQuota", http.MethodGet, "sys/quotas/rate-limit?list=true"},
		{"QuotaConfig", http.MethodGet, "sys/quotas/config"},
		{"Plugin", http.MethodGet, "sys/plugins/catalog/secret/kv"},
		{"CORSConfig", http.MethodGet, "sys/config/cors"},
		{"UIHeader", http.MethodGet, "sys/config/ui/headers?list=true"},
		{"Logger", http.MethodGet, "sys/loggers"},
		{"RaftAutopilot", http.MethodGet, "sys/storage/raft/autopilot/configuration"},
		{nsmKindKeyRot, http.MethodGet, "sys/rotate/config"},
		{"Workflow", http.MethodGet, "sys/workflows/manage?list=true"},
		{"OpenBaoNamespace", http.MethodGet, "sys/namespaces?list=true"},
		{"Policy", http.MethodGet, "sys/policies/acl/default"},
		{"PasswordPolicy", http.MethodGet, "sys/policies/password?list=true"},
		{"AuthMount", http.MethodGet, "sys/auth"},
		{"SecretsMount", http.MethodGet, "sys/mounts"},
	}
	out := make([]*nsmProbe, 0, 2*len(specs))
	for _, ns := range []string{nsmTeamA, nsmTeamAApps} {
		for _, s := range specs {
			p := &nsmProbe{kind: s.kind, method: s.method, path: s.path, ns: ns}
			resp, code, err := b.do(ns, s.method, s.path, nil)
			p.code = code
			if err != nil {
				p.message = fmt.Sprint(resp["errors"])
			}
			p.rootOnly = (code == http.StatusBadRequest && (strings.Contains(p.message, "only available in root namespace") ||
				strings.Contains(p.message, "unavailable in namespaces"))) ||
				(code == http.StatusNotFound && strings.Contains(p.message, "unsupported path"))
			if p.rootOnly != nsmRootOnly[s.kind] {
				p.verdict = fmt.Sprintf("root-only %v, nsmRootOnly says %v", p.rootOnly, nsmRootOnly[s.kind])
			}
			out = append(out, p)
		}
	}
	return out
}

// nsmEscapeCheck creates the first example of k with each escaping spec.namespace and expects the CRD to reject it.
func nsmEscapeCheck(ctx context.Context, k8s client.Client, k *nsmKind, ns string) string {
	docs := k.examples()
	if len(docs) == 0 {
		return "-"
	}
	if props, _ := k.Spec["properties"].(map[string]any); props["namespace"] == nil {
		return "n/a (no spec.namespace)"
	}
	var accepted []string
	for i, value := range nsmEscapes {
		obj := nsmRewrite(docs[0], ns, value)
		obj.SetName(fmt.Sprintf("nsm-escape-%d", i))
		err := k8s.Create(ctx, obj)
		switch {
		case err == nil:
			accepted = append(accepted, value)
			_ = k8s.Delete(ctx, obj)
		case !nsmIsInvalid(err) || !strings.Contains(err.Error(), "spec.namespace"):
			accepted = append(accepted, value+" ("+nsmShortErr(err)+")")
		}
	}
	if len(accepted) > 0 {
		return nsmFail + ": accepted " + strings.Join(accepted, ", ")
	}
	return fmt.Sprintf("rejected %d/%d", len(nsmEscapes), len(nsmEscapes))
}

// nsmApply creates the examples of every kind under test and their referents in mode m's Kubernetes namespace with
// m's spec.namespace, Secrets first, and enables the mounts they need in m's target namespace.
func nsmApply(ctx context.Context, t *testing.T, k8s client.Client, c *nsmCatalog, rows []*nsmRow, m *nsmMode) {
	t.Helper()
	m.createErrs, m.states = map[string][]string{}, map[string]nsmCellState{}
	seen := map[string]bool{}
	var all []*unstructured.Unstructured
	for _, r := range rows {
		for _, d := range r.kind.examples() {
			if key := nsmKey(d); !seen[key] {
				seen[key] = true
				all = append(all, nsmRewrite(d, m.k8sNS, m.specNS))
			}
		}
	}
	for _, r := range rows {
		for _, d := range c.referents(r.kind, r.kind.examples()) {
			if key := nsmKey(d); !seen[key] && !nsmPluginKind(c, d.GetKind()) {
				seen[key] = true
				all = append(all, nsmRewrite(d, m.k8sNS, m.specNS))
			}
		}
	}
	managed := nsmManagedMountPaths(all)
	for _, secretsFirst := range []bool{true, false} {
		for _, obj := range all {
			if (obj.GetKind() == nsmKindSecret) != secretsFirst {
				continue
			}
			err := k8s.Create(ctx, obj)
			switch {
			case obj.GetKind() == nsmKindSecret:
				if err != nil && !apierrors.IsAlreadyExists(err) {
					t.Logf("%s: create Secret %s: %v", m.name, obj.GetName(), err)
				}
			case err != nil:
				m.createErrs[nsmKey(obj)] = append(m.createErrs[nsmKey(obj)], nsmShortErr(err))
			default:
				m.created = append(m.created, obj)
				if mt, path, ok := nsmMountOf(obj); ok && !managed[nsmMountKey(mt.auth, path)] {
					if err := m.bao.ensureMount(m.expect, mt, path); err != nil {
						t.Logf("%s: mount %s for %s: %v", m.name, path, nsmKey(obj), err)
					}
				}
			}
		}
	}
}

// nsmSettle waits until every object of a positive mode is Ready and every object has a current condition, or no
// state changed for nsmSettleQuiet, or nsmSettleMax passed.
func nsmSettle(ctx context.Context, k8s client.Client, modes []*nsmMode) {
	start, changed := time.Now(), time.Now()
	last := map[string]string{}
	for time.Since(start) < nsmSettleMax {
		pending, uncurrent := 0, 0
		for _, m := range modes {
			for _, o := range m.created {
				s := nsmState(ctx, k8s, o)
				key := m.name + "/" + nsmKey(o)
				if v := fmt.Sprint(s.ready, s.reason); last[key] != v {
					last[key], changed = v, time.Now()
				}
				if !s.current {
					uncurrent++
				}
				if !m.negative && !s.ready {
					pending++
				}
			}
		}
		if uncurrent == 0 && (pending == 0 || time.Since(changed) > nsmSettleQuiet) {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func nsmReadState(ctx context.Context, k8s client.Client, obj *unstructured.Unstructured) nsmCellState {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(obj.GroupVersionKind())
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
		return nsmCellState{nsmObjState: nsmObjState{reason: "GetFailed", message: err.Error()}}
	}
	s := nsmCellState{nsmObjState: nsmStateOf(cur)}
	s.statusNS, _, _ = unstructured.NestedString(cur.Object, "status", "namespace")
	s.statusPath, _, _ = unstructured.NestedString(cur.Object, "status", "path")
	s.statusNS = strings.Trim(s.statusNS, "/")
	return s
}

// nsmEvaluate fills the cell of mode m for row r, comparing each example with its control: the same example in
// the root mode u1a.
func nsmEvaluate(r *nsmRow, m, control *nsmMode, atRest time.Time) {
	kind := r.kind.Kind
	docs := r.kind.examples()
	if len(docs) == 0 {
		if reason, ok := nsmExceptions[kind]; ok {
			r.cells[m.name] = "exception: " + reason
			return
		}
		r.fail(m.name, "no example document")
		return
	}
	child := m.expect != "" && !m.negative
	var passed, broken int
	reasons := map[string]int{}
	var admission int
	for _, d := range docs {
		key := nsmKey(d)
		s, ok := m.states[key]
		if errs := m.createErrs[key]; child && nsmRootOnly[kind] && len(errs) > 0 {
			if !strings.Contains(errs[0], "only in the root namespace") {
				r.fail(m.name, "%s rejected by the CRD for another reason: %s", d.GetName(), errs[0])
			}
			admission++
			continue
		}
		if errs := m.createErrs[key]; len(errs) > 0 || !ok {
			if len(errs) > 0 && m.name == control.name {
				r.note("%s: %s rejected by the CRD: %s", m.name, d.GetName(), errs[0])
			}
			broken++
			continue
		}
		reasons[s.reason]++
		switch outcome, msg := nsmCheckDoc(kind, m, control, d, s, atRest); outcome {
		case nsmDocFail:
			r.fail(m.name, "%s: %s", d.GetName(), msg)
		case nsmDocPass:
			passed++
		case nsmDocNoControl:
			broken++
		}
	}
	if r.cells[m.name] == nsmFail {
		return
	}
	switch {
	case m.negative:
		r.cells[m.name] = "refused (" + nsmCounts(reasons) + ")"
	case child && nsmRootOnly[kind] && admission > 0:
		r.cells[m.name] = "root-only refused (CRD)"
	case child && nsmRootOnly[kind]:
		r.cells[m.name] = "root-only refused"
	case child && kind == nsmKindKeyRot:
		r.cells[m.name] = "exception: needs a sealable namespace (Unsupported)"
	case passed > 0:
		r.cells[m.name] = "pass"
		if broken > 0 {
			r.cells[m.name] = fmt.Sprintf("pass (%d/%d examples)", passed, passed+broken)
		}
	default:
		if reason, ok := nsmExceptions[kind]; ok {
			r.cells[m.name] = "exception: " + reason
			if m.name == control.name {
				r.note("%s: not Ready in the root namespace: %s", m.name, nsmCounts(reasons))
			}
			return
		}
		r.fail(m.name, "no example Ready in the root namespace (%s) and no declared exception", nsmCounts(reasons))
	}
}

// Outcomes of one example in one mode.
const (
	nsmDocPass = iota
	nsmDocRefused
	nsmDocNoControl
	nsmDocFail
)

// nsmCheckDoc checks one example d with state s in mode m.
func nsmCheckDoc(kind string, m, control *nsmMode, d *unstructured.Unstructured, s nsmCellState,
	atRest time.Time) (int, string) {
	res := nsmResource(d, m.k8sNS)
	writes, calls, late := nsmAudited(m.audit, res, atRest)
	child := m.expect != "" && !m.negative
	switch {
	case m.negative:
		if written := nsmWrites(m.audit, res, func(string) bool { return true }); len(written) > 0 {
			return nsmDocFail, "wrote " + strings.Join(written, ", ")
		}
		if kind == nsmKindAccLevel {
			if calls > 0 {
				return nsmDocFail, fmt.Sprintf("%d OpenBao calls for a kind without an OpenBao object", calls)
			}
			return nsmDocRefused, ""
		}
		if s.ready {
			return nsmDocFail, fmt.Sprintf("Ready through a login that must not reach %s", nsmShow(m.expect))
		}
		if want := []string{commonv1alpha1.ReasonPermissionDenied, commonv1alpha1.ReasonReferenceNotReady,
			commonv1alpha1.ReasonRootOnlyKind}; !slices.Contains(want, s.reason) {
			return nsmDocFail, fmt.Sprintf("refused with %s, want one of %v", s.String(), want)
		}
		return nsmDocRefused, ""
	case child && nsmRootOnly[kind]:
		if s.reason != commonv1alpha1.ReasonRootOnlyKind || calls > 0 {
			return nsmDocFail, fmt.Sprintf("%s with %d OpenBao calls, want %s before any call", s.String(), calls,
				commonv1alpha1.ReasonRootOnlyKind)
		}
		return nsmDocRefused, ""
	case child && kind == nsmKindKeyRot:
		if s.reason != commonv1alpha1.ReasonUnsupported || writes > 0 {
			return nsmDocFail, fmt.Sprintf("%s with %d writes, want %s", s.String(), writes, commonv1alpha1.ReasonUnsupported)
		}
		return nsmDocRefused, ""
	}
	if c, ok := control.states[nsmKey(d)]; !ok || !c.ready {
		return nsmDocNoControl, ""
	}
	if msg := nsmCheckPositive(m, d, s, res, writes, late); msg != "" {
		return nsmDocFail, msg
	}
	return nsmDocPass, ""
}

// nsmCheckPositive checks one Ready-able example in a positive mode: Ready, recorded in and present at the expected
// namespace, every write it made in that namespace, none at rest.
func nsmCheckPositive(m *nsmMode, d *unstructured.Unstructured, s nsmCellState, res string, writes, late int) string {
	if !s.ready {
		return "not Ready: " + s.String()
	}
	if d.GetKind() == nsmKindAccLevel {
		return ""
	}
	if s.statusNS != m.expect {
		return fmt.Sprintf("status.namespace %q, want %q", s.statusNS, m.expect)
	}
	if outside := nsmWrites(m.audit, res, func(ns string) bool { return ns != m.expect }); len(outside) > 0 {
		return "wrote outside " + nsmShow(m.expect) + ": " + strings.Join(outside, ", ")
	}
	if late > 0 {
		return fmt.Sprintf("%d writes at rest", late)
	}
	rel := s.statusPath
	if m.expect != "" {
		var ok bool
		if rel, ok = strings.CutPrefix(s.statusPath, m.expect+"/"); !ok {
			return fmt.Sprintf("status.path %q is not under %q", s.statusPath, m.expect)
		}
	}
	_, code, err := m.bao.do(m.expect, http.MethodGet, rel, nil)
	if err == nil {
		return ""
	}
	// A collection path (AppRoleSecretID's secret-id) cannot be read but lists what was created in it.
	if code == http.StatusMethodNotAllowed {
		if _, _, err := m.bao.do(m.expect, http.MethodGet, rel+"?list=true", nil); err == nil {
			return ""
		}
	}
	// Composite kinds (AccessLayout, PKIConfig) record a prefix or parent path; any object they wrote proves them.
	for _, w := range nsmWrites(m.audit, res, func(ns string) bool { return ns == m.expect }) {
		if _, _, err := m.bao.do(m.expect, http.MethodGet, strings.TrimPrefix(w, nsmShow(m.expect)+":"), nil); err == nil {
			return ""
		}
	}
	// PKIConfig records <mount>/config, which cannot be read, and writes only the sections that differ from the
	// defaults, so a PKIConfig at defaults made no write; its sections read back.
	if d.GetKind() == "PKIConfig" {
		for _, section := range []string{"cluster", "urls", "crl", "issuers"} {
			if _, _, err := m.bao.do(m.expect, http.MethodGet, rel+"/"+section, nil); err == nil {
				return ""
			}
		}
	}
	return fmt.Sprintf("read-back of %s in %s: status %d, and none of its %d writes reads back", rel, nsmShow(m.expect),
		code, writes)
}

// nsmAudited counts the successful writes and all calls the operator made for resource res, and the successful
// writes after atRest.
func nsmAudited(entries []auditEntry, res string, atRest time.Time) (writes, calls, late int) {
	for _, e := range entries {
		if !strings.HasSuffix(e.resource, res) {
			continue
		}
		calls++
		if e.write() {
			writes++
			if e.at.After(atRest) {
				late++
			}
		}
	}
	return writes, calls, late
}

// nsmWrites lists the successful writes for res in the namespaces that in selects.
func nsmWrites(entries []auditEntry, res string, in func(ns string) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.resource, res) && e.write() && in(e.namespace) {
			if w := nsmShow(e.namespace) + ":" + e.path; !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	return out
}

// nsmResource is the attribution suffix of an example in Kubernetes namespace ns, "/<kind>/<namespace>/<name>";
// the engine prefixes it with the API group.
func nsmResource(d *unstructured.Unstructured, ns string) string {
	return "/" + d.GetKind() + "/" + ns + "/" + d.GetName()
}

func nsmKey(d *unstructured.Unstructured) string { return d.GetKind() + "/" + d.GetName() }

func nsmShow(ns string) string {
	if ns == "" {
		return "root"
	}
	return ns
}

func nsmCounts(m map[string]int) string {
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s x%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func nsmPluginKind(c *nsmCatalog, kind string) bool {
	for _, k := range c.kinds {
		if k.Kind == kind {
			return k.plugin()
		}
	}
	return false
}

func nsmShortErr(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return strings.ReplaceAll(msg, "|", "/")
}

func nsmEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func nsmWriteReport(t *testing.T, out string, modes []*nsmMode, rows []*nsmRow, probes []*nsmProbe, drift time.Duration) {
	t.Helper()
	var b strings.Builder
	totals := map[string]int{}
	for _, r := range rows {
		for _, m := range modes {
			cell := r.cells[m.name]
			switch {
			case cell == nsmFail:
				totals["fail"]++
			case strings.HasPrefix(cell, "pass"):
				totals["pass"]++
			case strings.HasPrefix(cell, "root-only refused"):
				totals["root-only refused"]++
			case strings.HasPrefix(cell, "refused"):
				totals["negative refused"]++
			case strings.HasPrefix(cell, "exception"):
				totals["declared exception"]++
			}
		}
	}
	fmt.Fprintf(&b, "# namespace matrix\n\ngenerated %s by `go test -tags integration ./test/nsmatrix/` "+
		"(OpenBao `bao server -dev` per mode with namespaces team-a, team-a/apps, team-b; envtest; manager built "+
		"from ./cmd; drift interval %s).\n\n", time.Now().Format(time.RFC3339), drift)
	fmt.Fprintf(&b, "kinds: %d built-in; cells: %d pass, %d root-only refused, %d negative refused, %d declared "+
		"exception, %d fail.\n\n", len(rows), totals["pass"], totals["root-only refused"], totals["negative refused"],
		totals["declared exception"], totals["fail"])
	b.WriteString("modes:\n\n")
	for _, m := range modes {
		target := "lands in " + nsmShow(m.expect)
		if m.negative {
			target = "must not reach " + nsmShow(m.expect)
		}
		fmt.Fprintf(&b, "- `%s`: %s; %s\n", m.name, m.title, target)
	}
	b.WriteString("\npass: every example that is Ready in u1a is Ready, status.namespace is the target, it reads " +
		"back there with the root token, every write the audit log attributes to it (X-Openbao-Config-Resource) is in " +
		"the target namespace and none happened during 3.5 drift intervals at rest. root-only refused: reason " +
		"RootOnlyKind and no OpenBao call for the object; (CRD) when the CRD itself rejects spec.namespace. refused: never Ready, reason PermissionDenied, " +
		"ReferenceNotReady or RootOnlyKind, and no successful write in any namespace. escape: spec.namespace values " +
		strings.Join(nsmEscapes, ", ") + " rejected by the CRD.\n\n")
	b.WriteString("| kind |")
	for _, m := range modes {
		b.WriteString(" " + m.name + " |")
	}
	b.WriteString(" escape | notes |\n|---|")
	for range modes {
		b.WriteString("---|")
	}
	b.WriteString("---|---|\n")
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].kind.Kind < rows[j].kind.Kind })
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s |", r.kind.Kind)
		for _, m := range modes {
			fmt.Fprintf(&b, " %s |", strings.ReplaceAll(r.cells[m.name], "|", "/"))
		}
		notes := strings.ReplaceAll(strings.Join(r.notes, "; "), "|", "/")
		fmt.Fprintf(&b, " %s | %s |\n", r.escape, strings.ReplaceAll(notes, "\n", " "))
	}
	for _, kind := range slices.Sorted(maps.Keys(nsmConnectionKinds)) {
		note := "the harness: each mode is one OpenBaoServer and one OpenBaoConnection"
		if kind == "ClusterOpenBaoConnection" {
			note = "not exercised here; it resolves namespaces like OpenBaoConnection (internal/connection)"
		}
		fmt.Fprintf(&b, "| %s |%s - | %s |\n", kind, strings.Repeat(" - |", len(modes)), note)
	}
	b.WriteString("\n## root-only probe (root token, OpenBao 2.7.2)\n\n| kind | namespace | request | status | " +
		"error | root-only |\n|---|---|---|---|---|---|\n")
	for _, p := range probes {
		msg := strings.ReplaceAll(strings.ReplaceAll(p.message, "\n", " "), "|", "/")
		fmt.Fprintf(&b, "| %s | %s | %s %s | %d | %s | %v |\n", p.kind, p.ns, p.method, p.path, p.code, msg, p.rootOnly)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("matrix written to %s", out)
}
