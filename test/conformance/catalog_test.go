//go:build conformance

package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	confKindConnection = "OpenBaoConnection"
	confKindSecret     = "Secret"
	confKeyName        = "name"
	confKeyKind        = "kind"
	confKeySpec        = "spec"
	confConnectionRef  = "connectionRef"
)

// confConnectionKinds are configured by the harness itself, not reconciled through the engine.
var confConnectionKinds = map[string]bool{
	confKindConnection: true, "ClusterOpenBaoConnection": true, "OpenBaoServer": true,
}

// confCommonFields are the ResourceSpec fields shared by every kind; the default test does not vary them.
var confCommonFields = map[string]bool{
	confConnectionRef: true, "namespace": true, "creationPolicy": true, "deletionPolicy": true,
	"driftDetection": true, "driftInterval": true, "managementPolicy": true,
}

// confIdentityFields select the OpenBao path; changing them moves the object instead of changing a field.
var confIdentityFields = map[string]bool{confKeyName: true, "path": true, "mountPath": true}

// confRootKinds configure root-namespace-only endpoints (sys singletons, catalog, quotas, audit), measured on
// bao server -dev 2.7.2; KeyRotationConfig needs a sealable namespace, which a child is not.
var confRootKinds = map[string]bool{
	"Plugin": true, "RaftAutopilot": true, "Logger": true, "KeyRotationConfig": true, "CORSConfig": true,
	"UIHeader": true, "AuditRequestHeader": true, "QuotaConfig": true, "RateLimitQuota": true, "Audit": true,
}

// confMount is a mount a kind needs before it can be reconciled.
type confMount struct {
	auth    bool
	typ     string
	version string
}

// confMountTypes maps a kind to the mount type its mountPath names.
var confMountTypes = func() map[string]confMount {
	out := map[string]confMount{}
	for m, kinds := range map[confMount][]string{
		{auth: true, typ: "kubernetes"}: {"KubernetesAuthConfig", "KubernetesAuthRole", "AccessGrant"},
		{auth: true, typ: "jwt"}:        {"JWTAuthConfig", "JWTAuthRole", "JWTCELRole"},
		{auth: true, typ: "approle"}:    {"AppRole", "AppRoleSecretID", "EntityAlias", "GroupAlias"},
		{auth: true, typ: "cert"}:       {"CertAuthConfig", "CertAuthRole"},
		{auth: true, typ: "userpass"}:   {"UserpassUser"},
		{typ: "kv", version: "2"}:       {"KVSecret", "KVConfig", "RandomSecret"},
		{typ: "database"}:               {"DatabaseConnection", "DatabaseRole", "DatabaseStaticRole"},
		{typ: "pki"}:                    {"PKIIssuer", "PKIKey", "PKIRole", "PKIConfig", "PKIACMEConfig", "PKIAutoTidy"},
		{typ: "transit"}:                {"TransitKey"},
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

// confKind is one managed kind: its CRD spec schema, availability and example documents.
type confKind struct {
	Kind, Group, Short string
	Spec               map[string]any
	Availability       string
	ExampleFile        string
	Docs               []*unstructured.Unstructured
	GenDocs            []*unstructured.Unstructured
	// Prereqs are the other-kind documents of the kind's fixture: referents of this kind only, never indexed.
	Prereqs []*unstructured.Unstructured
}

// controller is the engine's controller name, the label of controller_runtime_reconcile_total.
func (k *confKind) controller() string { return strings.ToLower(k.Kind) + "." + k.Group }

func (k *confKind) plugin() bool { return strings.HasPrefix(k.Availability, "external-plugin") }

// own returns the documents of this kind, examples first, then generated examples.
func (k *confKind) own(generated bool) []*unstructured.Unstructured {
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

func (k *confKind) property(field string) map[string]any {
	props, _ := k.Spec["properties"].(map[string]any)
	prop, _ := props[field].(map[string]any)
	return prop
}

// examples returns the example documents of this kind, or the generated ones when there is no example.
func (k *confKind) examples() []*unstructured.Unstructured {
	if docs := k.own(false); len(docs) > 0 {
		return docs
	}
	return k.own(true)
}

func (k *confKind) required() map[string]bool {
	out := map[string]bool{}
	req, _ := k.Spec["required"].([]any)
	for _, r := range req {
		if s, ok := r.(string); ok {
			out[s] = true
		}
	}
	return out
}

// optionalFields lists the top-level spec fields a user may omit, minus common and path-selecting fields.
func (k *confKind) optionalFields() []string {
	props, _ := k.Spec["properties"].(map[string]any)
	req := k.required()
	var out []string
	for name := range props {
		if !req[name] && !confCommonFields[name] && !confIdentityFields[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

var confCELField = regexp.MustCompile(`self\.\??([A-Za-z_][A-Za-z0-9_]*)`)

// celFields lists the spec fields that the spec-level CEL rules other than transition rules mention (one-of
// and dependency rules).
func (k *confKind) celFields() map[string]bool {
	out := map[string]bool{}
	rules, _ := k.Spec["x-kubernetes-validations"].([]any)
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		text, _ := rule["rule"].(string)
		if strings.Contains(text, "oldSelf") {
			continue
		}
		for _, m := range confCELField.FindAllStringSubmatch(text, -1) {
			if !confCommonFields[m[1]] {
				out[m[1]] = true
			}
		}
	}
	return out
}

type confCatalog struct {
	kinds []*confKind
	// index holds every example document by kind/name, for resolving references across files. Generated
	// examples are left out (except for kinds without an example): their placeholder names match each other.
	index map[string][]*unstructured.Unstructured
}

func confLoadCatalog(t *testing.T, root, crdDir string) *confCatalog {
	t.Helper()
	avail := confAvailability(t, filepath.Join(root, "docs", "kinds.md"))
	files, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c := &confCatalog{index: map[string][]*unstructured.Unstructured{}}
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
		if confConnectionKinds[crd.Spec.Names.Kind] || len(crd.Spec.Versions) == 0 {
			continue
		}
		props, _ := crd.Spec.Versions[0].Schema.OpenAPIV3Schema["properties"].(map[string]any)
		spec, _ := props["spec"].(map[string]any)
		k := &confKind{
			Kind:         crd.Spec.Names.Kind,
			Group:        crd.Spec.Group,
			Short:        strings.SplitN(crd.Spec.Group, ".", 2)[0],
			Spec:         spec,
			Availability: avail[crd.Spec.Names.Kind],
		}
		if k.Availability == "" {
			t.Fatalf("%s: no availability in docs/kinds.md", k.Kind)
		}
		name := strings.ToLower(k.Kind) + ".yaml"
		k.ExampleFile = filepath.Join(root, "examples", k.Short, name)
		gen := filepath.Join(root, "examples-generated", k.Short, name)
		if _, err := os.Stat(k.ExampleFile); err != nil {
			k.ExampleFile = gen
		}
		k.Docs = confDecodeFile(t, k.ExampleFile)
		if k.ExampleFile != gen {
			if _, err := os.Stat(gen); err == nil {
				k.GenDocs = confDecodeFile(t, gen)
			}
		}
		if fixture := confDecodeFile(t, filepath.Join(root, "test", "conformance", "fixtures", k.Short, name)); fixture != nil {
			k.Docs, k.Prereqs = confOverlay(k.Kind, k.Docs, fixture)
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

// confOverlay applies a fixture file (test/conformance/fixtures/<group>/<kind>.yaml) to the example documents of
// kind: its documents of kind replace the example's own, its other documents become prerequisites.
func confOverlay(kind string, docs, fixture []*unstructured.Unstructured) (own, prereqs []*unstructured.Unstructured) {
	for _, d := range fixture {
		if d.GetKind() == kind {
			own = append(own, d)
		} else {
			prereqs = append(prereqs, d)
		}
	}
	replace := len(own) > 0
	for _, d := range docs {
		if d.GetKind() != kind || !replace {
			own = append(own, d)
		}
	}
	return own, prereqs
}

// confAvailability reads the availability column of docs/kinds.md by kind.
func confAvailability(t *testing.T, path string) map[string]string {
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

func confDecodeFile(t *testing.T, path string) []*unstructured.Unstructured {
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
// documents named by *Ref/*Refs fields anywhere in the catalogue, transitively, after the kind's fixture
// prerequisites (shared: the no-write namespace). Documents of kind are excluded so they do not add writes to the
// kind under test.
func (c *confCatalog) referents(k *confKind, docs []*unstructured.Unstructured, shared bool) []*unstructured.Unstructured {
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
		for field, kind := range confNameReferents[d.GetKind()] {
			for _, name := range confFieldStrings(d.Object["spec"], strings.Split(field, ".")) {
				var first *unstructured.Unstructured
				found := false
				for _, key := range confSortedKeys(c.index) {
					if !strings.HasPrefix(key, kind+"/") {
						continue
					}
					for _, m := range c.index[key] {
						if first == nil {
							first = m
						}
						if specName, _, _ := unstructured.NestedString(m.Object, "spec", confKeyName); specName == name ||
							(specName == "" && m.GetName() == name) {
							add(m)
							found = true
						}
					}
				}
				if !found && first != nil {
					clone := first.DeepCopy()
					clone.SetName(name)
					_ = unstructured.SetNestedField(clone.Object, name, "spec", confKeyName)
					unstructured.RemoveNestedField(clone.Object, "spec", "writeSecretRef")
					add(clone)
				}
			}
		}
		for _, ref := range confRefNames(d.Object["spec"], "") {
			for key, matches := range c.index {
				if strings.HasSuffix(key, "/"+ref.name) && (ref.secret == (strings.HasPrefix(key, confKindSecret+"/"))) {
					for _, m := range matches {
						add(m)
					}
				}
			}
		}
	}
	for _, d := range k.Prereqs {
		// In the shared no-write namespace a prerequisite must not displace the example document of that name.
		if !shared || len(c.index[d.GetKind()+"/"+d.GetName()]) == 0 {
			add(d)
		}
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

type confRef struct {
	name   string
	secret bool
}

// confRefNames collects the names of references under v: values of "name" inside a field ending in Ref or
// Refs, other than connectionRef.
// confNameReferents lists, per kind, the spec fields (dotted, through lists) that name an OpenBao object instead of
// referencing a resource with a *Ref field, and the kind that creates that object. The referent is the example
// document whose spec.name, or metadata.name when spec.name is unset, equals the value; without one, the first
// example of that kind renamed to the value, without the output Secret the original already owns.
var confNameReferents = map[string]map[string]string{
	"AccessGrant":         {"rules.level": "AccessLevel"},
	"AccessLayout":        {"roles.levels.level": "AccessLevel"},
	"MFALoginEnforcement": {"entities.entityName": "Entity", "groups.groupName": "Group"},
	"OIDCAssignment":      {"entities.entityName": "Entity", "groups.groupName": "Group"},
	"OIDCClient":          {"key": "OIDCKey", "assignments": "OIDCAssignment"},
	"OIDCProvider":        {"allowedClients.clientName": "OIDCClient", "scopesSupported": "OIDCScope"},
	"OIDCRole":            {"key": "OIDCKey"},
	"RandomSecret":        {"passwordPolicyName": "PasswordPolicy"},
}

// confSortedKeys returns the keys of the document index in order, so a cloned referent is chosen deterministically.
func confSortedKeys(index map[string][]*unstructured.Unstructured) []string {
	keys := make([]string, 0, len(index))
	for key := range index {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// confFieldStrings returns the strings at path below v, descending into lists.
func confFieldStrings(v any, path []string) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, confFieldStrings(item, path)...)
		}
		return out
	case map[string]any:
		if len(path) > 0 {
			return confFieldStrings(x[path[0]], path[1:])
		}
	case string:
		if len(path) == 0 {
			return []string{x}
		}
	}
	return nil
}

func confRefNames(v any, field string) []confRef {
	var out []confRef
	switch x := v.(type) {
	case map[string]any:
		isRef := (strings.HasSuffix(field, "Ref") || strings.HasSuffix(field, "Refs")) && field != confConnectionRef
		if name, ok := x[confKeyName].(string); ok && isRef {
			out = append(out, confRef{name: name, secret: strings.Contains(field, "Secret") ||
				strings.Contains(field, "secret")})
		}
		for k, child := range x {
			out = append(out, confRefNames(child, k)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, confRefNames(child, field)...)
		}
	}
	return out
}

// confRewrite points an example document at the test namespace and connection. spec.namespace is dropped (the
// connection selects the OpenBao namespace) and so is spec.driftInterval (the manager flag sets the pace).
func confRewrite(d *unstructured.Unstructured, ns string, deletePolicy bool) *unstructured.Unstructured {
	c := d.DeepCopy()
	c.SetNamespace(ns)
	c.SetResourceVersion("")
	c.SetUID("")
	spec, ok := c.Object["spec"].(map[string]any)
	if !ok || c.GetKind() == confKindSecret {
		return c
	}
	if _, ok := spec[confConnectionRef]; ok {
		spec[confConnectionRef] = map[string]any{confKeyKind: confKindConnection, confKeyName: confConnection}
	}
	delete(spec, "namespace")
	delete(spec, "driftInterval")
	if deletePolicy {
		spec["deletionPolicy"] = "Delete"
		annotations := c.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["openbaoconfig.io/allow-destructive-delete"] = "true"
		c.SetAnnotations(annotations)
	}
	return c
}

// confMountOf returns the mount a (defaulted) object needs, if its kind has one.
func confMountOf(obj *unstructured.Unstructured) (confMount, string, bool) {
	m, ok := confMountTypes[obj.GetKind()]
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

// confManagedMountPaths lists the mount paths that AuthMount/SecretsMount documents create themselves, keyed by
// confMountKey: an auth method and a secrets engine may share a path.
func confManagedMountPaths(objs []*unstructured.Unstructured) map[string]bool {
	out := map[string]bool{}
	for _, o := range objs {
		if o.GetKind() != "AuthMount" && o.GetKind() != "SecretsMount" {
			continue
		}
		path, _, _ := unstructured.NestedString(o.Object, "spec", "path")
		if path == "" {
			path = o.GetName()
		}
		out[confMountKey(o.GetKind() == "AuthMount", path)] = true
	}
	return out
}

func confMountKey(auth bool, path string) string {
	if auth {
		return "auth/" + strings.Trim(path, "/")
	}
	return strings.Trim(path, "/")
}

// confObjState is the engine-reported state of one object.
type confObjState struct {
	ready, current bool
	reason         string
	message        string
}

func confState(ctx context.Context, k8s client.Client, obj *unstructured.Unstructured) confObjState {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(obj.GroupVersionKind())
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), cur); err != nil {
		return confObjState{reason: "GetFailed", message: err.Error()}
	}
	return confStateOf(cur)
}

func confStateOf(cur *unstructured.Unstructured) confObjState {
	gen := cur.GetGeneration()
	observed, _, _ := unstructured.NestedInt64(cur.Object, "status", "observedGeneration")
	s := confObjState{current: observed == gen}
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

func (s confObjState) String() string {
	msg := s.message
	if len(msg) > 160 {
		msg = msg[:160] + "..."
	}
	return s.reason + ": " + strings.ReplaceAll(msg, "|", "/")
}

// confIsInvalid reports a CRD validation (OpenAPI or CEL) rejection.
func confIsInvalid(err error) bool {
	return apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)
}
