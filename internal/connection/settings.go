package connection

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
)

// ErrMissingServiceAccount guards the tenant boundary when CRD validation was bypassed.
var ErrMissingServiceAccount = errors.New("serviceAccountRef is required for a namespaced connection")

// ErrMissingAudiences guards against minting a token valid for the Kubernetes API for a requested ServiceAccount.
var ErrMissingAudiences = errors.New("audiences is required with serviceAccountRef")

const (
	kindSecret    = "Secret"
	kindConfigMap = "ConfigMap"
)

type keyRef struct {
	kind, namespace, name, key string
}

func (k keyRef) String() string {
	return k.kind + "/" + k.namespace + "/" + k.name + "/" + k.key
}

func (k keyRef) object() string {
	return ReferenceIndexValue(k.kind, k.namespace, k.name)
}

type objectRef struct {
	namespace, name string
}

// serviceAccountLogin is kubernetes or jwt auth; a nil serviceAccount means the operator's own token, read from
// tokenPath or, when that is empty, from the resolver's default token file.
type serviceAccountLogin struct {
	jwt            bool
	mountPath      string
	role           string
	serviceAccount *objectRef
	audiences      []string
	tokenPath      string
}

type appRoleLogin struct {
	mountPath        string
	roleID, secretID keyRef
}

// settings is the kind-independent view of a connection with every reference namespaced. tenant is the
// namespace of an OpenBaoConnection and empty for a ClusterOpenBaoConnection.
type settings struct {
	spec      corev1alpha1.ConnectionSpec
	tenant    string
	token     *keyRef
	tokenFile string
	saLogin   *serviceAccountLogin
	appRole   *appRoleLogin
}

func (s settings) references() []keyRef {
	var refs []keyRef
	if s.token != nil {
		refs = append(refs, *s.token)
	}
	if s.appRole != nil {
		refs = append(refs, s.appRole.roleID, s.appRole.secretID)
	}
	return refs
}

// PoolKey is the client cache key and metrics label of a connection. The pool fingerprints the config
// itself, so a spec or credential change needs no new key.
func PoolKey(obj client.Object) string {
	return string(obj.GetUID())
}

// References returns "Kind/namespace/name" for every Secret and ConfigMap a connection or an OpenBaoServer
// reads. It backs the field index that re-queues the object when one of them changes.
func References(obj client.Object) []string {
	var refs []keyRef
	if server, ok := obj.(*corev1alpha1.OpenBaoServer); ok {
		if ca := serverCARef(server); ca != nil {
			refs = append(refs, *ca)
		}
	} else {
		s, err := settingsOf(obj)
		if err != nil {
			return nil
		}
		refs = s.references()
	}
	var out []string
	for _, ref := range refs {
		out = append(out, ref.object())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ReferenceIndexValue is the index value References emits for an object of kind.
func ReferenceIndexValue(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}

func connectionSpec(obj client.Object) (corev1alpha1.ConnectionSpec, error) {
	switch c := obj.(type) {
	case *corev1alpha1.OpenBaoConnection:
		return c.Spec.ConnectionSpec, nil
	case *corev1alpha1.ClusterOpenBaoConnection:
		return c.Spec.ConnectionSpec, nil
	default:
		return corev1alpha1.ConnectionSpec{}, fmt.Errorf("unsupported connection type %T", obj)
	}
}

func settingsOf(obj client.Object) (settings, error) {
	switch c := obj.(type) {
	case *corev1alpha1.OpenBaoConnection:
		return namespacedSettings(c)
	case *corev1alpha1.ClusterOpenBaoConnection:
		return clusterSettings(c), nil
	default:
		return settings{}, fmt.Errorf("unsupported connection type %T", obj)
	}
}

func namespacedSettings(c *corev1alpha1.OpenBaoConnection) (settings, error) {
	ns := c.Namespace
	secret := func(sel commonv1alpha1.SecretKeySelector) keyRef {
		return keyRef{kind: kindSecret, namespace: ns, name: sel.Name, key: sel.Key}
	}
	s := settings{spec: c.Spec.ConnectionSpec, tenant: ns}
	auth := c.Spec.Auth
	switch {
	case auth.Token != nil:
		ref := secret(auth.Token.SecretRef)
		s.token = &ref
	case auth.AppRole != nil:
		s.appRole = &appRoleLogin{
			mountPath: auth.AppRole.MountPath,
			roleID:    secret(auth.AppRole.RoleIDSecretRef),
			secretID:  secret(auth.AppRole.SecretIDSecretRef),
		}
	case auth.Kubernetes != nil:
		if auth.Kubernetes.ServiceAccountRef == nil {
			return settings{}, ErrMissingServiceAccount
		}
		if len(auth.Kubernetes.Audiences) == 0 {
			return settings{}, ErrMissingAudiences
		}
		s.saLogin = &serviceAccountLogin{
			mountPath:      auth.Kubernetes.MountPath,
			role:           auth.Kubernetes.Role,
			serviceAccount: &objectRef{namespace: ns, name: auth.Kubernetes.ServiceAccountRef.Name},
			audiences:      auth.Kubernetes.Audiences,
		}
	case auth.JWT != nil:
		if auth.JWT.ServiceAccountRef == nil {
			return settings{}, ErrMissingServiceAccount
		}
		if len(auth.JWT.Audiences) == 0 {
			return settings{}, ErrMissingAudiences
		}
		s.saLogin = &serviceAccountLogin{
			jwt:            true,
			mountPath:      auth.JWT.MountPath,
			role:           auth.JWT.Role,
			serviceAccount: &objectRef{namespace: ns, name: auth.JWT.ServiceAccountRef.Name},
			audiences:      auth.JWT.Audiences,
		}
	}
	return s, nil
}

func clusterSettings(c *corev1alpha1.ClusterOpenBaoConnection) settings {
	secret := func(sel corev1alpha1.NamespacedSecretKeySelector) keyRef {
		return keyRef{kind: kindSecret, namespace: sel.Namespace, name: sel.Name, key: sel.Key}
	}
	account := func(ref *corev1alpha1.NamespacedObjectReference) *objectRef {
		if ref == nil {
			return nil
		}
		return &objectRef{namespace: ref.Namespace, name: ref.Name}
	}
	injected := func(id *corev1alpha1.InjectedIdentity) string {
		if id == nil {
			return ""
		}
		return id.TokenPath
	}
	s := settings{spec: c.Spec.ConnectionSpec}
	auth := c.Spec.Auth
	switch {
	case auth.Token != nil && auth.Token.Filesystem != nil:
		s.tokenFile = auth.Token.Filesystem.Path
	case auth.Token != nil && auth.Token.SecretRef != nil:
		ref := secret(*auth.Token.SecretRef)
		s.token = &ref
	case auth.AppRole != nil:
		s.appRole = &appRoleLogin{
			mountPath: auth.AppRole.MountPath,
			roleID:    secret(auth.AppRole.RoleIDSecretRef),
			secretID:  secret(auth.AppRole.SecretIDSecretRef),
		}
	case auth.Kubernetes != nil:
		s.saLogin = &serviceAccountLogin{
			mountPath:      auth.Kubernetes.MountPath,
			role:           auth.Kubernetes.Role,
			serviceAccount: account(auth.Kubernetes.ServiceAccountRef),
			audiences:      auth.Kubernetes.Audiences,
			tokenPath:      injected(auth.Kubernetes.InjectedIdentity),
		}
	case auth.JWT != nil:
		s.saLogin = &serviceAccountLogin{
			jwt:            true,
			mountPath:      auth.JWT.MountPath,
			role:           auth.JWT.Role,
			serviceAccount: account(auth.JWT.ServiceAccountRef),
			audiences:      auth.JWT.Audiences,
			tokenPath:      injected(auth.JWT.InjectedIdentity),
		}
	}
	return s
}

func serverCARef(server *corev1alpha1.OpenBaoServer) *keyRef {
	ca := server.Spec.CABundle
	switch {
	case ca == nil:
		return nil
	case ca.SecretRef != nil:
		return caRef(kindSecret, *ca.SecretRef)
	case ca.ConfigMapRef != nil:
		return caRef(kindConfigMap, *ca.ConfigMapRef)
	}
	return nil
}

func caRef(kind string, ref corev1alpha1.NamespacedCABundleReference) *keyRef {
	return &keyRef{kind: kind, namespace: ref.Namespace, name: ref.Name,
		key: cmp.Or(ref.Key, corev1alpha1.DefaultCABundleKey)}
}
