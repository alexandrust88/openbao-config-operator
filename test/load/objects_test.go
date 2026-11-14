//go:build load

package load

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const secretKey = "password"

// kind is one managed kind in a load set.
type kind struct {
	prefix string
	gvr    schema.GroupVersionResource
	gvk    schema.GroupVersionKind
	spec   func(name string, index int) map[string]any
	// secret creates a Secret with the same name as the object, holding secretKey.
	secret bool
	// referrer marks a kind that references another kind of its set; it is applied before its referents.
	referrer bool
}

func newKind(prefix, group, kindName, resource string, secret bool, spec func(string, int) map[string]any) kind {
	gv := schema.GroupVersion{Group: group + ".openbaoconfig.io", Version: "v1alpha1"}
	return kind{prefix: prefix, gvr: gv.WithResource(resource), gvk: gv.WithKind(kindName), spec: spec, secret: secret}
}

func (k kind) asReferrer() kind {
	k.referrer = true
	return k
}

func policyText(name string) string {
	return fmt.Sprintf("path \"secret/data/load/%s\" {\n  capabilities = [\"read\"]\n}\n", name)
}

func kvPath(name string) string { return "load/" + name }

func secretValue(name string) string { return "load-value-" + name }

var (
	policyKind = newKind("pol", "sys", kindPolicy, "policies", false, func(n string, _ int) map[string]any {
		return map[string]any{litPolicy: policyText(n)}
	})
	kvKind = newKind("kv", "secrets", kindKVSecret, "kvsecrets", true, func(n string, _ int) map[string]any {
		return map[string]any{litMountPath: "secret", "path": kvPath(n),
			"dataFrom": []any{map[string]any{"secretRef": map[string]any{litName: n}}}}
	})
	entityKind = newKind("ent", "identity", "Entity", "entities", false, func(string, int) map[string]any {
		return map[string]any{"policies": []any{litDefault}, litMetadata: map[string]any{"team": "load"}}
	})
	// entityAliasKind precedes entityKind in the refs set: the alias at index i names the entity at i+1.
	entityAliasKind = newKind("eal", "identity", "EntityAlias", "entityaliases", false,
		func(n string, i int) map[string]any {
			return map[string]any{"aliasName": n, litMountPath: litUserpass,
				"entityRef": map[string]any{litName: fmt.Sprintf("%s-%05d", entityKind.prefix, i+1)}}
		}).asReferrer()
	groupKind = newKind("grp", "identity", "Group", "groups", false, func(string, int) map[string]any {
		return map[string]any{litType: "internal", "policies": []any{litDefault}}
	})
	// groupMembershipKind precedes entityKind and groupKind in the members set: the membership at index i adds the
	// entity at i+1 to the group at i+2.
	groupMembershipKind = newKind("gmb", "identity", "GroupMembership", "groupmemberships", false,
		func(_ string, i int) map[string]any {
			return map[string]any{
				"entityRef": map[string]any{litName: fmt.Sprintf("%s-%05d", entityKind.prefix, i+1)},
				"groupRef":  map[string]any{litName: fmt.Sprintf("%s-%05d", groupKind.prefix, i+2)},
			}
		}).asReferrer()
	mixedKinds = []kind{
		policyKind,
		kvKind,
		newKind("pwp", "sys", "PasswordPolicy", "passwordpolicies", false, func(string, int) map[string]any {
			return map[string]any{litPolicy: "length = 20\nrule \"charset\" {\n  charset = \"abcdefghijklmnopqrstuvwxyz\"\n}\n"}
		}),
		newKind("arh", "sys", "AuditRequestHeader", "auditrequestheaders", false, func(string, int) map[string]any {
			return map[string]any{"hmac": false}
		}),
		newKind("tkr", "auth", "TokenRole", "tokenroles", false, func(string, int) map[string]any {
			return map[string]any{"allowedPolicies": []any{litDefault}, "orphan": true}
		}),
		newKind("upu", "auth", "UserpassUser", "userpassusers", true, func(n string, _ int) map[string]any {
			return map[string]any{litMountPath: litUserpass, "tokenPolicies": []any{litDefault},
				"passwordSecretRef": map[string]any{litName: n, litKey: secretKey}}
		}),
		newKind("apr", "auth", "AppRole", "approles", false, func(string, int) map[string]any {
			return map[string]any{litMountPath: litApprole, "tokenPolicies": []any{litDefault}, "secretIDTTL": "1h"}
		}),
		entityKind,
		groupKind,
		newKind("trk", "secrets", "TransitKey", "transitkeys", false, func(string, int) map[string]any {
			return map[string]any{litMountPath: "transit", litType: "aes256-gcm96", "deletionAllowed": true}
		}),
	}
	connectionGVR = schema.GroupVersionResource{Group: "core.openbaoconfig.io", Version: "v1alpha1",
		Resource: "openbaoconnections"}
)

func kindSet(name string) ([]kind, error) {
	switch name {
	case "simple":
		return []kind{policyKind, kvKind}, nil
	case "mixed":
		return mixedKinds, nil
	case "refs":
		return []kind{entityAliasKind, entityKind}, nil
	case "members":
		return []kind{groupMembershipKind, entityKind, groupKind}, nil
	default:
		return nil, fmt.Errorf("unknown kind set %q, want simple, mixed, refs or members", name)
	}
}

// object is one generated resource.
type object struct {
	kind  kind
	name  string
	index int
}

func (o object) key() string { return o.kind.gvk.Kind + "/" + o.name }

// plan spreads n objects round-robin over kinds.
func plan(kinds []kind, n int) []object {
	objects := make([]object, n)
	for i := range n {
		k := kinds[i%len(kinds)]
		objects[i] = object{kind: k, name: fmt.Sprintf("%s-%05d", k.prefix, i), index: i}
	}
	return objects
}

func (o object) unstructured() *unstructured.Unstructured {
	spec := o.kind.spec(o.name, o.index)
	spec["connectionRef"] = map[string]any{litName: connectionName}
	spec["deletionPolicy"] = "Delete"
	// The harness deletes everything it creates, including kinds whose delete destroys data.
	u := &unstructured.Unstructured{Object: map[string]any{
		litMetadata: map[string]any{litName: o.name, "namespace": objectNamespace,
			"annotations": map[string]any{"openbaoconfig.io/allow-destructive-delete": "true"}},
		"spec": spec,
	}}
	u.SetGroupVersionKind(o.kind.gvk)
	return u
}

func (o object) secret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: o.name, Namespace: objectNamespace},
		Data:       map[string][]byte{secretKey: []byte(secretValue(o.name))},
	}
}

// parallel runs fn over objects with workers goroutines and returns the first error.
func parallel(objects []object, workers int, fn func(object) error) error {
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
		next  = make(chan object)
	)
	for range workers {
		wg.Go(func() {
			for o := range next {
				if err := fn(o); err != nil {
					once.Do(func() { first = err })
				}
			}
		})
	}
	for _, o := range objects {
		next <- o
	}
	close(next)
	wg.Wait()
	return first
}

// createObjects creates the Secrets first, then the resources, recording each resource's create time. Referrers
// go first and their referents refsDelay later, so every reference starts out not ready; it returns when the
// referents were applied.
func createObjects(ctx context.Context, k8s client.Client, tr *tracker, objects []object, workers int,
	refsDelay time.Duration) (time.Time, error) {
	err := parallel(objects, workers, func(o object) error {
		if !o.kind.secret {
			return nil
		}
		return k8s.Create(ctx, o.secret())
	})
	if err != nil {
		return time.Time{}, err
	}
	var referrers, rest []object
	for _, o := range objects {
		if o.kind.referrer {
			referrers = append(referrers, o)
		} else {
			rest = append(rest, o)
		}
	}
	create := func(o object) error {
		tr.created(o.key(), time.Now())
		if err := k8s.Create(ctx, o.unstructured()); err != nil {
			return fmt.Errorf("create %s: %w", o.key(), err)
		}
		return nil
	}
	if len(referrers) > 0 {
		if err := parallel(referrers, workers, create); err != nil {
			return time.Time{}, err
		}
		time.Sleep(refsDelay)
	}
	referents := time.Now()
	return referents, parallel(rest, workers, create)
}

func deleteObjects(ctx context.Context, k8s client.Client, tr *tracker, objects []object, workers int) error {
	return parallel(objects, workers, func(o object) error {
		tr.deleteRequested(o.key(), time.Now())
		return client.IgnoreNotFound(k8s.Delete(ctx, o.unstructured()))
	})
}

// tamper changes the OpenBao object behind o out of band. Only Policy and KVSecret are tampered.
func tamper(bao *baoServer, o object) error {
	switch o.kind.gvk.Kind {
	case kindPolicy:
		changed := strings.Replace(policyText(o.name), `["read"]`, `["read", "list"]`, 1)
		_, _, err := bao.do(http.MethodPut, "sys/policies/acl/"+o.name, map[string]any{litPolicy: changed})
		return err
	case kindKVSecret:
		_, _, err := bao.do(http.MethodPut, "secret/data/"+kvPath(o.name), map[string]any{"data": map[string]any{}})
		return err
	default:
		return fmt.Errorf("tamper: unsupported kind %s", o.kind.gvk.Kind)
	}
}

// repaired reports whether the OpenBao object behind o holds the desired content again.
func repaired(bao *baoServer, o object) (bool, error) {
	switch o.kind.gvk.Kind {
	case kindPolicy:
		resp, _, err := bao.do(http.MethodGet, "sys/policies/acl/"+o.name, nil)
		if err != nil {
			return false, err
		}
		data, _ := resp["data"].(map[string]any)
		return data[litPolicy] == policyText(o.name), nil
	case kindKVSecret:
		resp, _, err := bao.do(http.MethodGet, "secret/data/"+kvPath(o.name), nil)
		if err != nil {
			return false, err
		}
		data, _ := resp["data"].(map[string]any)
		values, _ := data["data"].(map[string]any)
		return values[secretKey] == secretValue(o.name), nil
	default:
		return false, fmt.Errorf("repaired: unsupported kind %s", o.kind.gvk.Kind)
	}
}

// addUnmanagedKey writes a key the KVSecret does not own next to the managed one.
func addUnmanagedKey(bao *baoServer, o object) error {
	_, _, err := bao.do(http.MethodPut, "secret/data/"+kvPath(o.name), map[string]any{"data": map[string]any{
		secretKey: secretValue(o.name), "unmanaged": "added-out-of-band",
	}})
	return err
}

// kvState returns the current version and the key names of the KV secret behind o.
func kvState(bao *baoServer, o object) (int, []string, error) {
	resp, _, err := bao.do(http.MethodGet, "secret/metadata/"+kvPath(o.name), nil)
	if err != nil {
		return 0, nil, err
	}
	meta, _ := resp["data"].(map[string]any)
	version, _ := meta["current_version"].(float64)
	resp, _, err = bao.do(http.MethodGet, "secret/data/"+kvPath(o.name), nil)
	if err != nil {
		return 0, nil, err
	}
	data, _ := resp["data"].(map[string]any)
	values, _ := data["data"].(map[string]any)
	return int(version), slices.Sorted(maps.Keys(values)), nil
}

// remaining lists the generated OpenBao objects that still exist: Policies, and KV secrets that still hold the
// managed key (the operator keeps keys it does not own, such as the drift probe's).
func remaining(bao *baoServer) ([]string, error) {
	var left []string
	for _, k := range []kind{policyKind, kvKind} {
		path := "sys/policies/acl"
		if k.gvk.Kind == kindKVSecret {
			path = "secret/metadata/load"
		}
		resp, code, err := bao.do("LIST", path, nil)
		if code == http.StatusNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		data, _ := resp["data"].(map[string]any)
		keys, _ := data["keys"].([]any)
		for _, key := range keys {
			name, _ := key.(string)
			if !strings.HasPrefix(name, k.prefix+"-") {
				continue
			}
			if k.gvk.Kind == kindKVSecret {
				version, values, err := kvState(bao, object{kind: kvKind, name: name})
				if err != nil {
					return nil, err
				}
				if !slices.Contains(values, secretKey) {
					continue
				}
				name = fmt.Sprintf("%s (version %d, keys %v)", name, version, values)
			}
			left = append(left, path+"/"+name)
		}
	}
	return left, nil
}

// prepareBao mounts the engines the kind sets need and creates the operator's AppRole, whose credentials it
// returns.
func prepareBao(bao *baoServer) (roleID, secretID string, err error) {
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"sys/mounts/secret", map[string]any{litType: "kv", "options": map[string]any{"version": "2"}}},
		{"sys/mounts/transit", map[string]any{litType: "transit"}},
		{"sys/auth/userpass", map[string]any{litType: litUserpass}},
		{"sys/auth/approle", map[string]any{litType: litApprole}},
		{"sys/auth/" + operatorAppRoleMount, map[string]any{litType: litApprole}},
		{"sys/policies/acl/load-operator", map[string]any{litPolicy: "path \"*\" {\n  capabilities = " +
			"[\"create\", \"read\", \"update\", \"patch\", \"delete\", \"list\", \"sudo\"]\n}\n"}},
		{"auth/" + operatorAppRoleMount + "/role/operator", map[string]any{"token_policies": []string{"load-operator"},
			"token_ttl": "1h", "token_max_ttl": "4h"}},
	} {
		if _, _, err := bao.do(http.MethodPost, step.path, step.body); err != nil {
			return "", "", err
		}
	}
	resp, _, err := bao.do(http.MethodGet, "auth/"+operatorAppRoleMount+"/role/operator/role-id", nil)
	if err != nil {
		return "", "", err
	}
	data, _ := resp["data"].(map[string]any)
	roleID, _ = data["role_id"].(string)
	resp, _, err = bao.do(http.MethodPost, "auth/"+operatorAppRoleMount+"/role/operator/secret-id", nil)
	if err != nil {
		return "", "", err
	}
	data, _ = resp["data"].(map[string]any)
	secretID, _ = data["secret_id"].(string)
	return roleID, secretID, nil
}

// identityCounts is what grows in OpenBao when a client logs in again instead of reusing its token.
type identityCounts struct {
	TokenAccessors int `json:"tokenAccessors"`
	Entities       int `json:"entities"`
	EntityAliases  int `json:"entityAliases"`
}

func countIdentity(bao *baoServer) (identityCounts, error) {
	var c identityCounts
	for path, dst := range map[string]*int{
		"auth/token/accessors":     &c.TokenAccessors,
		"identity/entity/id":       &c.Entities,
		"identity/entity-alias/id": &c.EntityAliases,
	} {
		resp, code, err := bao.do("LIST", path, nil)
		if code == http.StatusNotFound {
			continue
		}
		if err != nil {
			return c, err
		}
		data, _ := resp["data"].(map[string]any)
		keys, _ := data["keys"].([]any)
		*dst = len(keys)
	}
	return c, nil
}
