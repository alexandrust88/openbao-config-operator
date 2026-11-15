//go:build load

package load

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const kindSetTree = "tree"

// treeShape is the namespace hierarchy of the tree set: Parents OpenBao namespaces under root and Children under
// each parent; every namespace holds KV kv-v2 mounts, one kubernetes auth mount with its config, Roles roles and
// Policies policies.
type treeShape struct {
	Parents  int `json:"parents"`
	Children int `json:"childrenPerParent"`
	KV       int `json:"kvMountsPerNamespace"`
	Roles    int `json:"kubernetesRolesPerNamespace"`
	Policies int `json:"policiesPerNamespace"`
}

// parseTreeShape reads "parents x children x kv[x roles x policies]"; roles and policies default to 3.
func parseTreeShape(s string) (treeShape, error) {
	parts := strings.Split(s, "x")
	if len(parts) != 3 && len(parts) != 5 {
		return treeShape{}, fmt.Errorf("tree shape %q: want PARENTSxCHILDRENxKV or PARENTSxCHILDRENxKVxROLESxPOLICIES", s)
	}
	values := []int{0, 0, 0, 3, 3}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return treeShape{}, fmt.Errorf("tree shape %q: %q is not a positive integer", s, p)
		}
		values[i] = n
	}
	return treeShape{Parents: values[0], Children: values[1], KV: values[2], Roles: values[3], Policies: values[4]}, nil
}

func (s treeShape) String() string {
	return fmt.Sprintf("%dx%dx%dx%dx%d", s.Parents, s.Children, s.KV, s.Roles, s.Policies)
}

func (s treeShape) namespaces() int { return s.Parents * (1 + s.Children) }

// Levels in creation order; delete runs them backwards, so children go before their parents.
const (
	levelParentNS     = "parent-namespace"
	levelConnection   = "connection"
	levelParentMounts = "parent-mounts"
	levelParentAuth   = "parent-auth"
	levelChildNS      = "child-namespace"
	levelChildMounts  = "child-mounts"
	levelChildAuth    = "child-auth"
)

var (
	treeLevels = []string{levelParentNS, levelConnection, levelParentMounts, levelParentAuth, levelChildNS,
		levelChildMounts, levelChildAuth}

	namespaceKind     = newKind("ns", "sys", "OpenBaoNamespace", "openbaonamespaces", false, nil)
	secretsMountKind  = newKind("kv", "sys", "SecretsMount", "secretsmounts", false, nil)
	authMountKind     = newKind("k8s", "sys", "AuthMount", "authmounts", false, nil)
	k8sAuthConfigKind = newKind("k8sc", "auth", "KubernetesAuthConfig", "kubernetesauthconfigs", false, nil)
	k8sAuthRoleKind   = newKind("k8sr", "auth", "KubernetesAuthRole", "kubernetesauthroles", false, nil)
	connectionKind    = kind{gvr: connectionGVR, gvk: connectionGVR.GroupVersion().WithKind(kindConnection)}
	treeKinds         = []kind{namespaceKind, secretsMountKind, authMountKind, k8sAuthConfigKind, k8sAuthRoleKind,
		policyKind}
)

// planTree lays out the tree in creation order. Parent namespaces and the objects in them go through the root
// connection with spec.namespace (root-to-child); each parent gets a connection into it that logs in at the root
// (child-connection-root-login), which creates the child namespaces and the objects in them.
func planTree(s treeShape, caPEM string) []object {
	byLevel := map[string][]object{}
	add := func(level string, objects ...object) { byLevel[level] = append(byLevel[level], objects...) }
	for p := 1; p <= s.Parents; p++ {
		parent := fmt.Sprintf("p%03d", p)
		conn := connectionName + "-" + parent
		add(levelParentNS, object{kind: namespaceKind, name: parent, orphan: true, spec: map[string]any{}})
		add(levelConnection, object{kind: connectionKind, name: conn, spec: map[string]any{
			"serverRef":     map[string]any{litName: connectionName},
			"namespace":     parent,
			"authNamespace": "",
			"auth":          appRoleAuth(),
		}})
		mounts, auth := namespaceObjects(s, caPEM, connectionName, parent, parent, parent)
		add(levelParentMounts, mounts...)
		add(levelParentAuth, auth...)
		for c := 1; c <= s.Children; c++ {
			child := fmt.Sprintf("%s-c%02d", parent, c)
			add(levelChildNS, object{kind: namespaceKind, name: child, conn: conn, baoNS: parent, orphan: true,
				spec: map[string]any{}})
			mounts, auth := namespaceObjects(s, caPEM, conn, child, parent+"/"+child, child)
			add(levelChildMounts, mounts...)
			add(levelChildAuth, auth...)
		}
	}
	var objects []object
	for _, level := range treeLevels {
		for _, o := range byLevel[level] {
			o.level = level
			objects = append(objects, o)
		}
	}
	return objects
}

// namespaceObjects returns the mounts and policies of one namespace, then its kubernetes auth config and roles.
// Every OpenBao name is the Kubernetes name, which carries the namespace as a prefix.
func namespaceObjects(s treeShape, caPEM, conn, relNS, baoNS, prefix string) (mounts, auth []object) {
	in := func(k kind, name string, spec map[string]any) object {
		spec["namespace"] = relNS
		return object{kind: k, name: name, conn: conn, baoNS: baoNS, spec: spec}
	}
	authMount := prefix + "-k8s"
	mounts = append(mounts, in(authMountKind, authMount, map[string]any{litType: "kubernetes"}))
	for i := 1; i <= s.KV; i++ {
		mounts = append(mounts, in(secretsMountKind, fmt.Sprintf("%s-kv-%02d", prefix, i), map[string]any{litType: "kv-v2"}))
	}
	for i := 1; i <= s.Policies; i++ {
		name := fmt.Sprintf("%s-pol-%d", prefix, i)
		mounts = append(mounts, in(policyKind, name, map[string]any{litPolicy: policyText(name)}))
	}
	// Without a CA OpenBao reads the pod's service account CA, which the harness does not have.
	config := in(k8sAuthConfigKind, authMount+"-config", map[string]any{litMountPath: authMount,
		"kubernetesHost": "https://kubernetes.default.svc", "kubernetesCACert": caPEM, "disableLocalCAJWT": true})
	// The auth method configuration cannot be deleted, so the CRD only accepts Orphan.
	config.orphan = true
	auth = append(auth, config)
	for i := 1; i <= s.Roles; i++ {
		auth = append(auth, in(k8sAuthRoleKind, fmt.Sprintf("%s-role-%d", authMount, i), map[string]any{
			litMountPath:                    authMount,
			"boundServiceAccountNames":      []any{fmt.Sprintf("app-%d", i)},
			"boundServiceAccountNamespaces": []any{prefix},
			"tokenPolicies":                 []any{fmt.Sprintf("%s-pol-%d", prefix, min(i, s.Policies))},
		}))
	}
	return mounts, auth
}

func appRoleAuth() map[string]any {
	return map[string]any{"appRole": map[string]any{
		litMountPath:        operatorAppRoleMount,
		"roleIDSecretRef":   map[string]any{litName: litAppRoleSecret, litKey: "role-id"},
		"secretIDSecretRef": map[string]any{litName: litAppRoleSecret, litKey: "secret-id"},
	}}
}

// treeCounts is what the tree left in OpenBao, read with the root token.
type treeCounts struct {
	Namespaces           int `json:"namespaces"`
	KVMounts             int `json:"kvMounts"`
	KubernetesAuthMounts int `json:"kubernetesAuthMounts"`
	Policies             int `json:"policies"`
}

type treeMark struct {
	Phase string `json:"phase"`
	treeCounts
	Seconds float64 `json:"countSeconds"`
}

type treeResult struct {
	Shape       treeShape  `json:"shape"`
	Objects     int        `json:"objects"`
	Connections int        `json:"connections"`
	Counts      []treeMark `json:"openbaoCounts"`
}

// countTree walks every namespace below root and counts its kv mounts, kubernetes auth mounts and policies other
// than default.
func countTree(bao *baoServer, workers int) (treeCounts, error) {
	parents, err := listNamespaces(bao, "")
	if err != nil {
		return treeCounts{}, err
	}
	var (
		mu         sync.Mutex
		namespaces = slices.Clone(parents)
	)
	var listed, counted errgroup.Group
	listed.SetLimit(workers)
	for _, p := range parents {
		listed.Go(func() error {
			children, err := listNamespaces(bao, p)
			mu.Lock()
			defer mu.Unlock()
			for _, c := range children {
				namespaces = append(namespaces, p+"/"+c)
			}
			return err
		})
	}
	if err := listed.Wait(); err != nil {
		return treeCounts{}, err
	}
	c := treeCounts{Namespaces: len(namespaces)}
	counted.SetLimit(workers)
	for _, ns := range namespaces {
		counted.Go(func() error {
			kv, err := countMounts(bao, ns, "sys/mounts", "kv")
			if err != nil {
				return err
			}
			k8s, err := countMounts(bao, ns, "sys/auth", "kubernetes")
			if err != nil {
				return err
			}
			policies, err := listKeys(bao, ns, "sys/policies/acl")
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			c.KVMounts += kv
			c.KubernetesAuthMounts += k8s
			c.Policies += len(slices.DeleteFunc(policies, func(p string) bool { return p == litDefault }))
			return nil
		})
	}
	return c, counted.Wait()
}

func listNamespaces(bao *baoServer, ns string) ([]string, error) {
	keys, err := listKeys(bao, ns, "sys/namespaces")
	for i, k := range keys {
		keys[i] = strings.TrimSuffix(k, "/")
	}
	return keys, err
}

func listKeys(bao *baoServer, ns, path string) ([]string, error) {
	resp, code, err := bao.doIn(ns, "LIST", path, nil)
	if code == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("namespace %q: %w", ns, err)
	}
	data, _ := resp["data"].(map[string]any)
	raw, _ := data["keys"].([]any)
	keys := make([]string, 0, len(raw))
	for _, k := range raw {
		s, _ := k.(string)
		keys = append(keys, s)
	}
	return keys, nil
}

func countMounts(bao *baoServer, ns, path, mountType string) (int, error) {
	resp, _, err := bao.doIn(ns, http.MethodGet, path, nil)
	if err != nil {
		return 0, fmt.Errorf("namespace %q: %w", ns, err)
	}
	data, _ := resp["data"].(map[string]any)
	n := 0
	for _, m := range data {
		mount, _ := m.(map[string]any)
		if mount[litType] == mountType {
			n++
		}
	}
	return n, nil
}

// markTree records the OpenBao counts after phase and checks them against want.
func (r *loadRun) markTree(phase string, want treeCounts) {
	start := time.Now()
	c, err := countTree(r.bao, r.cfg.workers)
	if err != nil {
		r.t.Errorf("count tree after %s: %v", phase, err)
		return
	}
	r.res.Tree.Counts = append(r.res.Tree.Counts, treeMark{Phase: phase, treeCounts: c,
		Seconds: seconds(time.Since(start))})
	if c != want {
		r.fail("tree after %s: OpenBao holds %+v, want %+v", phase, c, want)
	}
	r.t.Logf("tree after %s: %+v (counted in %s)", phase, c, time.Since(start).Round(time.Millisecond))
}

func (s treeShape) counts() treeCounts {
	n := s.namespaces()
	return treeCounts{Namespaces: n, KVMounts: n * s.KV, KubernetesAuthMounts: n, Policies: n * s.Policies}
}
