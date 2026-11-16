//go:build integration

// The catalogue loader is copied from test/conformance/catalog_test.go (build tag conformance) so both suites read
// the same examples and fixtures; keep the two in step.

package nsmatrix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	nsmKindConnection = "OpenBaoConnection"
	nsmKindSecret     = "Secret"
	nsmKeyName        = "name"
	nsmKeyKind        = "kind"
	nsmKeySpec        = "spec"
	nsmConnectionRef  = "connectionRef"
)

// nsmConnectionKinds are configured by the harness itself, not reconciled through the engine.
var nsmConnectionKinds = map[string]bool{
	nsmKindConnection: true, "ClusterOpenBaoConnection": true, "OpenBaoServer": true,
}

// nsmMount is a mount a kind needs before it can be reconciled.
type nsmMount struct {
	auth    bool
	typ     string
	version string
}

// nsmMountTypes maps a kind to the mount type its mountPath names.
var nsmMountTypes = func() map[string]nsmMount {
	out := map[string]nsmMount{}
	for m, kinds := range map[nsmMount][]string{
		{auth: true, typ: "kubernetes"}: {"KubernetesAuthConfig", "KubernetesAuthRole", "AccessGrant"},
		{auth: true, typ: "jwt"}:        {"JWTAuthConfig", "JWTAuthRole", "JWTCELRole"},
		{auth: true, typ: "approle"}:    {"AppRole", "AppRoleSecretID", "EntityAlias", "GroupAlias"},
		{auth: true, typ: "cert"}:       {"CertAuthConfig", "CertAuthRole", "CertAuthCRL"},
		{auth: true, typ: "userpass"}:   {"UserpassUser"},
		{typ: "kv", version: "2"}:       {"KVSecret", "KVConfig", "KVSecretMetadata", "RandomSecret"},
		{typ: "database"}:               {"DatabaseConnection", "DatabaseRole", "DatabaseStaticRole"},
		{typ: "pki"}:                    {"PKIIssuer", "PKIKey", "PKIRole", "PKICELRole", "PKIConfig", "PKIKeyConfig", "PKIACMEConfig", "PKIAutoTidy"},
		{typ: "transit"}:                {"TransitKey", "TransitCacheConfig", "TransitKeyConfig"},
		{typ: "ssh"}:                    {"SSHCA", "SSHRole"},
		{typ: "kubernetes"}:             {"KubernetesSecretsConfig", "KubernetesSecretsRole"},
		{typ: "rabbitmq"}:               {"RabbitMQConfig", "RabbitMQRole"},
		{typ: "totp"}:                   {"TOTPKey"},
	} {
		for _, k := range kinds {
			out[k] = m
		}
	}
	return out
}()

// nsmKind is one managed kind: its CRD spec schema, availability and example documents.
type nsmKind struct {
	Kind, Group, Short string
	Spec               map[string]any
	Availability       string
	ExampleFile        string
	Docs               []*unstructured.Unstructured
	GenDocs            []*unstructured.Unstructured
	// Prereqs are the other-kind documents of the kind's fixture: referents of this kind only, never indexed.
	Prereqs []*unstructured.Unstructured
}

func (k *nsmKind) plugin() bool { return strings.HasPrefix(k.Availability, "external-plugin") }

// own returns the documents of this kind, examples first, then generated examples.
func (k *nsmKind) own(generated bool) []*unstructured.Unstructured {
	docs := k.Docs
	if generated {
		docs = append(slices.Clone(docs), k.GenDocs...)
	}
	var out []*unstructured.Unstructured
	for _, d := range docs {
		if d.GetKind() == k.Kind {
			out = append(out, d)
		}
	}
	return out
}

// examples returns the example documents of this kind, or the generated ones when there is no example.
func (k *nsmKind) examples() []*unstructured.Unstructured {
	if docs := k.own(false); len(docs) > 0 {
		return docs
	}
	return k.own(true)
}

type nsmCatalog struct {
	kinds []*nsmKind
	// index holds every example document by kind/name, for resolving references across files. Generated
	// examples are left out (except for kinds without an example): their placeholder names match each other.
	index map[string][]*unstructured.Unstructured
}

func nsmLoadCatalog(t *testing.T, root, crdDir string) *nsmCatalog {
	t.Helper()
	avail := nsmAvailability(t, filepath.Join(root, "docs", "kinds.md"))
	files, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c := &nsmCatalog{index: map[string][]*unstructured.Unstructured{}}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var crd struct {
			Spec struct {
				Group string `json:"group"`
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
				Versions []struct {
					Schema struct {
						OpenAPIV3Schema map[string]any `json:"openAPIV3Schema"`
					} `json:"schema"`
				} `json:"versions"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if nsmConnectionKinds[crd.Spec.Names.Kind] || len(crd.Spec.Versions) == 0 {
			continue
		}
		props, _ := crd.Spec.Versions[0].Schema.OpenAPIV3Schema["properties"].(map[string]any)
		spec, _ := props["spec"].(map[string]any)
		k := &nsmKind{
			Kind:         crd.Spec.Names.Kind,
			Group:        crd.Spec.Group,
			Short:        strings.SplitN(crd.Spec.Group, ".", 2)[0],
			Spec:         spec,
			Availability: avail[crd.Spec.Names.Kind],
		}
		if k.Availability == "" {
			k.Availability = "builtin"
		}
		name := strings.ToLower(k.Kind) + ".yaml"
		k.ExampleFile = filepath.Join(root, "examples", k.Short, name)
		gen := filepath.Join(root, "examples-generated", k.Short, name)
		if _, err := os.Stat(k.ExampleFile); err != nil {
			k.ExampleFile = gen
		}
		k.Docs = nsmDecodeFile(t, k.ExampleFile)
		if k.ExampleFile != gen {
			if _, err := os.Stat(gen); err == nil {
				k.GenDocs = nsmDecodeFile(t, gen)
			}
		}
		if fixture := nsmDecodeFile(t, filepath.Join(root, "test", "conformance", "fixtures", k.Short, name)); fixture != nil {
			k.Docs, k.Prereqs = nsmOverlay(k.Kind, k.Docs, fixture)
		}
		c.kinds = append(c.kinds, k)
	}
	sort.Slice(c.kinds, func(i, j int) bool {
		if c.kinds[i].Short != c.kinds[j].Short {
			return c.kinds[i].Short < c.kinds[j].Short
		}
		return c.kinds[i].Kind < c.kinds[j].Kind
	})
	for _, k := range c.kinds {
		if k.plugin() {
			continue
		}
		for _, d := range k.Docs {
			key := d.GetKind() + "/" + d.GetName()
			c.index[key] = append(c.index[key], d)
		}
	}
	return c
}

// nsmOverlay applies a fixture file (test/conformance/fixtures/<group>/<kind>.yaml) to the example documents of
// kind: its documents of kind replace the example's own, its other documents become prerequisites.
func nsmOverlay(kind string, docs, fixture []*unstructured.Unstructured) (own, prereqs []*unstructured.Unstructured) {
	for _, d := range fixture {
		if d.GetKind() == kind {
			own = append(own, d)
		} else {
			prereqs = append(prereqs, d)
		}
	}
	for _, d := range docs {
		if d.GetKind() != kind || len(own) == 0 {
			own = append(own, d)
		}
	}
	return own, prereqs
}

// nsmAvailability reads the availability column of docs/kinds.md by kind.
func nsmAvailability(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cols := strings.Split(strings.ReplaceAll(line, `\|`, "/"), "|")
		if len(cols) < 4 {
			continue
		}
		out[strings.Trim(strings.TrimSpace(cols[1]), "`")] = strings.TrimSpace(cols[3])
	}
	return out
}

func nsmDecodeFile(t *testing.T, path string) []*unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var objs []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		err := decoder.Decode(&obj.Object)
		if errors.Is(err, io.EOF) {
			return objs
		}
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(obj.Object) > 0 {
			objs = append(objs, obj)
		}
	}
}

// referents returns the documents that docs need: every other-kind document of the example file, plus the
// documents named by *Ref/*Refs fields anywhere in the catalogue, transitively. Documents of kind are excluded
// so they do not add writes to the kind under test.
func (c *nsmCatalog) referents(k *nsmKind, docs []*unstructured.Unstructured) []*unstructured.Unstructured {
	seen := map[string]bool{}
	var out []*unstructured.Unstructured
	var visit func(d *unstructured.Unstructured)
	add := func(d *unstructured.Unstructured) {
		key := d.GetKind() + "/" + d.GetName()
		if d.GetKind() == k.Kind || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, d)
		visit(d)
	}
	visit = func(d *unstructured.Unstructured) {
		for field, kind := range nsmNameReferents[d.GetKind()] {
			for _, name := range nsmFieldStrings(d.Object["spec"], strings.Split(field, ".")) {
				var first *unstructured.Unstructured
				found := false
				for _, key := range nsmSortedKeys(c.index) {
					if !strings.HasPrefix(key, kind+"/") {
						continue
					}
					for _, m := range c.index[key] {
						if first == nil {
							first = m
						}
						if specName, _, _ := unstructured.NestedString(m.Object, "spec", nsmKeyName); specName == name ||
							(specName == "" && m.GetName() == name) {
							add(m)
							found = true
						}
					}
				}
				if !found && first != nil {
					clone := first.DeepCopy()
					clone.SetName(name)
					_ = unstructured.SetNestedField(clone.Object, name, "spec", nsmKeyName)
					unstructured.RemoveNestedField(clone.Object, "spec", "writeSecretRef")
					add(clone)
				}
			}
		}
		for _, ref := range nsmRefNames(d.Object["spec"], "") {
			for key, matches := range c.index {
				if strings.HasSuffix(key, "/"+ref.name) && (ref.secret == (strings.HasPrefix(key, nsmKindSecret+"/"))) { // gitleaks:allow
					for _, m := range matches {
						add(m)
					}
				}
			}
		}
	}
	for _, d := range k.Prereqs {
		add(d)
	}
	for _, d := range k.Docs {
		if d.GetKind() != k.Kind {
			add(d)
		}
	}
	for _, d := range docs {
		visit(d)
	}
	return out
}

type nsmRef struct {
	name   string
	secret bool
}

// nsmRefNames collects the names of references under v: values of "name" inside a field ending in Ref or
// Refs, other than connectionRef.
// nsmNameReferents lists, per kind, the spec fields (dotted, through lists) that name an OpenBao object instead of
// referencing a resource with a *Ref field, and the kind that creates that object. The referent is the example
// document whose spec.name, or metadata.name when spec.name is unset, equals the value; without one, the first
// example of that kind renamed to the value, without the output Secret the original already owns.
var nsmNameReferents = map[string]map[string]string{
	"AccessGrant":    {"rules.level": "AccessLevel"},
	"AccessLayout":   {"roles.levels.level": "AccessLevel"},
	"OIDCAssignment": {"entities.entityName": "Entity", "groups.groupName": "Group"},
	"OIDCClient":     {"key": "OIDCKey", "assignments": "OIDCAssignment"},
	"OIDCProvider":   {"allowedClients.clientName": "OIDCClient", "scopesSupported": "OIDCScope"},
	"OIDCRole":       {"key": "OIDCKey"},
	"RandomSecret":   {"passwordPolicyName": "PasswordPolicy"},
}

// nsmSortedKeys returns the keys of the document index in order, so a cloned referent is chosen deterministically.
func nsmSortedKeys(index map[string][]*unstructured.Unstructured) []string {
	keys := make([]string, 0, len(index))
	for key := range index {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// nsmFieldStrings returns the strings at path below v, descending into lists.
func nsmFieldStrings(v any, path []string) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, nsmFieldStrings(item, path)...)
		}
		return out
	case map[string]any:
		if len(path) > 0 {
			return nsmFieldStrings(x[path[0]], path[1:])
		}
	case string:
		if len(path) == 0 {
			return []string{x}
		}
	}
	return nil
}

func nsmRefNames(v any, field string) []nsmRef {
	var out []nsmRef
	switch x := v.(type) {
	case map[string]any:
		isRef := (strings.HasSuffix(field, "Ref") || strings.HasSuffix(field, "Refs")) && field != nsmConnectionRef
		if name, ok := x[nsmKeyName].(string); ok && isRef {
			out = append(out, nsmRef{name: name, secret: strings.Contains(field, "Secret") ||
				strings.Contains(field, "secret")})
		}
		for k, child := range x {
			out = append(out, nsmRefNames(child, k)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, nsmRefNames(child, field)...)
		}
	}
	return out
}

// nsmRewrite points an example document at the test namespace and connection and sets spec.namespace to specNS
// (removed when empty); spec.driftInterval is dropped so the manager flag sets the pace.
func nsmRewrite(d *unstructured.Unstructured, ns, specNS string) *unstructured.Unstructured {
	c := d.DeepCopy()
	c.SetNamespace(ns)
	c.SetResourceVersion("")
	c.SetUID("")
	spec, ok := c.Object["spec"].(map[string]any)
	if !ok || c.GetKind() == nsmKindSecret {
		return c
	}
	if _, ok := spec[nsmConnectionRef]; ok {
		spec[nsmConnectionRef] = map[string]any{nsmKeyKind: nsmKindConnection, nsmKeyName: nsmConnection}
	}
	delete(spec, "namespace")
	delete(spec, "driftInterval")
	if specNS != "" {
		spec["namespace"] = specNS
	}
	return c
}

// nsmMountOf returns the mount a (defaulted) object needs, if its kind has one.
func nsmMountOf(obj *unstructured.Unstructured) (nsmMount, string, bool) {
	m, ok := nsmMountTypes[obj.GetKind()]
	if !ok {
		return m, "", false
	}
	path, _, _ := unstructured.NestedString(obj.Object, "spec", "mountPath")
	if path == "" {
		return m, "", false
	}
	if m.typ == "kv" {
		if v, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "kvVersion"); found && fmt.Sprint(v) == "1" {
			m.version = "1"
		}
	}
	return m, path, true
}

// nsmManagedMountPaths lists the mount paths that AuthMount/SecretsMount documents create themselves, keyed by
// nsmMountKey: an auth method and a secrets engine may share a path.
func nsmManagedMountPaths(objs []*unstructured.Unstructured) map[string]bool {
	out := map[string]bool{}
	for _, o := range objs {
		if o.GetKind() != "AuthMount" && o.GetKind() != "SecretsMount" {
			continue
		}
		path, _, _ := unstructured.NestedString(o.Object, "spec", "path")
		if path == "" {
			path = o.GetName()
		}
		out[nsmMountKey(o.GetKind() == "AuthMount", path)] = true
	}
	return out
}

func nsmMountKey(auth bool, path string) string {
	if auth {
		return "auth/" + strings.Trim(path, "/")
	}
	return strings.Trim(path, "/")
}

// nsmObjState is the engine-reported state of one object.
type nsmObjState struct {
	ready, current bool
	reason         string
	message        string
}

func nsmState(ctx context.Context, k8s client.Client, obj *unstructured.Unstructured) nsmObjState {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(obj.GroupVersionKind())
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
		return nsmObjState{reason: "GetFailed", message: err.Error()}
	}
	return nsmStateOf(cur)
}

func nsmStateOf(cur *unstructured.Unstructured) nsmObjState {
	gen := cur.GetGeneration()
	observed, _, _ := unstructured.NestedInt64(cur.Object, "status", "observedGeneration")
	s := nsmObjState{current: observed == gen}
	conds, _, _ := unstructured.NestedSlice(cur.Object, "status", "conditions")
	found := false
	for _, c := range conds {
		cond, _ := c.(map[string]any)
		condGen, _, _ := unstructured.NestedInt64(cond, "observedGeneration")
		reason, _ := cond["reason"].(string)
		message, _ := cond["message"].(string)
		switch {
		case cond["type"] == "Ready":
			found = true
			s.ready = cond["status"] == "True" && s.current && condGen == gen
			if s.reason == "" {
				s.reason, s.message = reason, message
			}
		case cond["type"] == "Synced" && cond["status"] == "False" && condGen == gen:
			s.reason, s.message = reason, message
		}
	}
	switch {
	case !found:
		s.reason, s.message = "NoStatus", "no Ready condition (controller not wired?)"
	case !s.ready && !s.current:
		s.message = fmt.Sprintf("status.observedGeneration %d behind generation %d; %s", observed, gen, s.message)
	}
	return s
}

func (s nsmObjState) String() string {
	msg := s.message
	if len(msg) > 160 {
		msg = msg[:160] + "..."
	}
	return s.reason + ": " + strings.ReplaceAll(msg, "|", "/")
}

// nsmIsInvalid reports a CRD validation (OpenAPI or CEL) rejection.
func nsmIsInvalid(err error) bool {
	return apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)
}
