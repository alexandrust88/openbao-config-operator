// Package connection resolves BaoConnection and ClusterBaoConnection objects, together with the
// BaoServer they name, into OpenBao clients, and enforces the tenant boundary.
package connection

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// DefaultTokenFile is the projected ServiceAccount token with a dedicated audience that the chart mounts into the
// operator pod, used by a ClusterBaoConnection injected identity without tokenPath. The kubelet rotates it, so it
// is read at every login.
const DefaultTokenFile = "/var/run/secrets/openbao/token"

// automountedTokenDir holds the operator's automounted ServiceAccount token, which the Kubernetes API server accepts.
// It must never reach OpenBao.
const automountedTokenDir = "/var/run/secrets/kubernetes.io/serviceaccount/"

const automountedTokenFile = automountedTokenDir + "token"

// ErrAutomountedToken is returned when a connection would send the operator's automounted ServiceAccount token, a
// Kubernetes API credential, to OpenBao.
var ErrAutomountedToken = errors.New("the automounted ServiceAccount token is a Kubernetes API credential and is " +
	"never sent to OpenBao; mount a projected token with a dedicated audience (chart value " +
	"injectedIdentity.projectedToken) and point tokenPath at it")

// tokenExpirationSeconds is the TokenRequest minimum; a requested token is used for a single login.
const tokenExpirationSeconds = 600

// ErrReferenceNotFound is returned when a Secret, ConfigMap or key a connection references is missing.
var ErrReferenceNotFound = errors.New("referenced object or key not found")

// ErrInsecureAddress is returned for a BaoServer address that is not https while allowInsecureHTTP is false.
var ErrInsecureAddress = errors.New("address must use https unless allowInsecureHTTP is true")

// ErrServerNotFound is returned, together with engine.ErrConnectionNotReady, when the BaoServer a connection
// names does not exist.
var ErrServerNotFound = errors.New("baoserver not found")

// Pool is the part of *openbao.Pool the resolver uses.
type Pool interface {
	Client(ctx context.Context, key string, cfg openbao.Config) (openbao.Client, error)
	Invalidate(key string)
}

// Resolver turns a connectionRef into an authenticated client. It implements engine.Connections.
type Resolver struct {
	Client client.Client
	Pool   Pool
	// TokenFile overrides DefaultTokenFile.
	TokenFile string
	// automountFile overrides automountedTokenFile in tests.
	automountFile string
	// APIServerAudiences are the audiences the Kubernetes API server accepts, such as its service account
	// issuer. A namespaced connection is denied a token for any of them or for a built-in API server audience,
	// even when the server's allowedAudiences lists it.
	APIServerAudiences []string

	// last holds the latest working configuration per pool key. ForCleanup keeps using it for a connection being
	// deleted, so the resources it still holds can clean up after its credentials are gone, as in a Namespace
	// deletion.
	last sync.Map
}

var (
	_ engine.Connections    = (*Resolver)(nil)
	_ engine.ServerResolver = (*Resolver)(nil)
)

// For returns the pooled client of the connection ref names, as seen from a resource in fromNamespace. The
// connection and its server's admission are checked on every call, also while the connection is being deleted.
func (r *Resolver) For(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference) (
	openbao.Client, error) {
	return r.resolve(ctx, fromNamespace, ref, false)
}

// ForCleanup is For for deleting what a resource acquired: a connection being deleted keeps serving its last
// working client, whatever its last check found, so the resources it still holds can clean up.
func (r *Resolver) ForCleanup(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference) (
	openbao.Client, error) {
	return r.resolve(ctx, fromNamespace, ref, true)
}

func (r *Resolver) resolve(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference,
	cleanup bool) (openbao.Client, error) {
	conn, err := r.object(ctx, fromNamespace, ref)
	if err != nil {
		return nil, err
	}
	key := PoolKey(conn)
	if cleanup && !conn.GetDeletionTimestamp().IsZero() {
		if cfg, ok := r.last.Load(key); ok {
			c, err := r.Pool.Client(ctx, key, cfg.(openbao.Config))
			return c, consumerError(conn, err)
		}
	}
	if err := ensureReady(conn, fromNamespace, ref); err != nil {
		return nil, err
	}
	cfg, err := r.Config(ctx, conn)
	if err != nil {
		if !conn.GetDeletionTimestamp().IsZero() {
			err = fmt.Errorf("%w: connection is being deleted: %w", engine.ErrConnectionNotReady, err)
		}
		return nil, consumerError(conn, err)
	}
	c, err := r.Pool.Client(ctx, key, cfg)
	if err != nil {
		return nil, consumerError(conn, err)
	}
	r.last.Store(key, cfg)
	return c, nil
}

// ClusterConnectionError is what a resource using a ClusterBaoConnection sees of the connection's configuration
// or login error: the platform's Secret, ServiceAccount and file names stay on the connection's own status. Err keeps
// the cause for errors.Is and errors.As.
type ClusterConnectionError struct {
	Name string
	Err  error
}

func (e *ClusterConnectionError) Error() string {
	return fmt.Sprintf("ClusterBaoConnection %s is not ready (see its status)", e.Name)
}

func (e *ClusterConnectionError) Unwrap() error { return e.Err }

func consumerError(conn client.Object, err error) error {
	if _, ok := conn.(*corev1alpha1.ClusterBaoConnection); !ok || err == nil {
		return err
	}
	return &ClusterConnectionError{Name: conn.GetName(), Err: err}
}

// Forget drops what the resolver keeps for the connection with pool key key.
func (r *Resolver) Forget(key string) {
	r.last.Delete(key)
}

// ServerFor returns the BaoServer behind a connection reference without logging in.
func (r *Resolver) ServerFor(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference) (
	engine.Server, error) {
	conn, err := r.object(ctx, fromNamespace, ref)
	if err != nil {
		return engine.Server{}, err
	}
	server, err := r.Server(ctx, conn)
	if err != nil {
		return engine.Server{}, err
	}
	return engine.Server{
		Key:    server.Name,
		Paused: server.Annotations[commonv1alpha1.AnnotationPaused] == "true",
	}, nil
}

func ensureReady(conn client.Object, fromNamespace string, ref commonv1alpha1.ConnectionReference) error {
	status := conn.(interface {
		GetConnectionStatus() *corev1alpha1.ConnectionStatus
	}).GetConnectionStatus()
	if !ready(conn, *status) {
		return fmt.Errorf("%w: %s is not ready", engine.ErrConnectionNotReady, engine.ConnectionKey(fromNamespace, ref))
	}
	return nil
}

// object returns the connection ref names if it may be used from fromNamespace.
func (r *Resolver) object(ctx context.Context, fromNamespace string, ref commonv1alpha1.ConnectionReference) (
	client.Object, error) {
	switch ref.Kind {
	case "", commonv1alpha1.ConnectionKindNamespaced:
		conn := &corev1alpha1.BaoConnection{}
		if err := r.get(ctx, client.ObjectKey{Namespace: fromNamespace, Name: ref.Name}, conn); err != nil {
			return nil, err
		}
		return conn, nil
	case commonv1alpha1.ConnectionKindCluster:
		conn := &corev1alpha1.ClusterBaoConnection{}
		if err := r.get(ctx, client.ObjectKey{Name: ref.Name}, conn); err != nil {
			return nil, err
		}
		if err := r.allowed(ctx, conn, fromNamespace); err != nil {
			return nil, err
		}
		return conn, nil
	default:
		return nil, fmt.Errorf("unsupported connection kind %q", ref.Kind)
	}
}

func (r *Resolver) get(ctx context.Context, key client.ObjectKey, conn client.Object) error {
	err := r.Client.Get(ctx, key, conn)
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: %v", engine.ErrConnectionNotReady, err)
	}
	return err
}

// ready reports a Ready condition for the connection's generation. Deletion bumps the generation, so a connection
// being deleted counts as ready while its last check succeeded: it stays usable for cleanup.
func ready(conn client.Object, status corev1alpha1.ConnectionStatus) bool {
	cond := meta.FindStatusCondition(status.Conditions, commonv1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return false
	}
	return cond.ObservedGeneration == conn.GetGeneration() || !conn.GetDeletionTimestamp().IsZero()
}

func (r *Resolver) allowed(ctx context.Context, conn *corev1alpha1.ClusterBaoConnection, fromNamespace string) error {
	if conn.Spec.AllowedNamespaces == nil {
		return fmt.Errorf("%w: ClusterBaoConnection %s allows no namespaces", engine.ErrConnectionDenied, conn.Name)
	}
	if err := r.selects(ctx, conn.Spec.AllowedNamespaces, fromNamespace); err != nil {
		return fmt.Errorf("ClusterBaoConnection %s: %w", conn.Name, err)
	}
	return nil
}

// ClusterAdmits reports whether conn's allowedNamespaces selects namespace.
func (r *Resolver) ClusterAdmits(ctx context.Context, conn *corev1alpha1.ClusterBaoConnection, namespace string) (
	bool, error) {
	return admitted(r.allowed(ctx, conn, namespace))
}

// ServerAdmits reports whether server's allowedNamespaces selects namespace for a BaoConnection.
func (r *Resolver) ServerAdmits(ctx context.Context, server *corev1alpha1.BaoServer, namespace string) (bool, error) {
	if server.Spec.AllowedNamespaces == nil {
		return false, nil
	}
	return admitted(r.selects(ctx, server.Spec.AllowedNamespaces, namespace))
}

// admitted turns ErrConnectionDenied into false and keeps any other error.
func admitted(err error) (bool, error) {
	if errors.Is(err, engine.ErrConnectionDenied) {
		return false, nil
	}
	return err == nil, err
}

// Server returns the BaoServer a connection names.
func (r *Resolver) Server(ctx context.Context, conn client.Object) (*corev1alpha1.BaoServer, error) {
	spec, err := connectionSpec(conn)
	if err != nil {
		return nil, err
	}
	server := &corev1alpha1.BaoServer{}
	err = r.Client.Get(ctx, client.ObjectKey{Name: spec.ServerRef.Name}, server)
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("%w: %w: %s", engine.ErrConnectionNotReady, ErrServerNotFound, spec.ServerRef.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("get baoserver %s: %w", spec.ServerRef.Name, err)
	}
	return server, nil
}

// Config builds the client configuration of a connection and the BaoServer it names. Credentials are read
// lazily by the auth closures.
func (r *Resolver) Config(ctx context.Context, conn client.Object) (openbao.Config, error) {
	server, err := r.Server(ctx, conn)
	if err != nil {
		return openbao.Config{}, err
	}
	return r.ConfigFor(ctx, conn, server)
}

// ConfigFor builds the client configuration of a connection to server. A namespaced connection that server does
// not admit gets engine.ErrConnectionDenied before any credential is read or requested.
func (r *Resolver) ConfigFor(ctx context.Context, conn client.Object, server *corev1alpha1.BaoServer) (
	openbao.Config, error) {
	s, err := settingsOf(conn)
	if err != nil {
		return openbao.Config{}, err
	}
	if s.tenant != "" {
		if err := r.admit(ctx, s, server); err != nil {
			return openbao.Config{}, err
		}
	} else if err := r.platformAudiences(s); err != nil {
		return openbao.Config{}, err
	}
	cfg, err := r.ServerConfig(ctx, server)
	if err != nil {
		return openbao.Config{}, err
	}
	cfg.Namespace, cfg.AuthNamespace = s.spec.Namespace, s.spec.Namespace
	if s.spec.AuthNamespace != nil {
		cfg.AuthNamespace = *s.spec.AuthNamespace
	}
	auth, err := r.auth(ctx, s)
	if err != nil {
		return openbao.Config{}, err
	}
	cfg.Auth = cachedAuth{Auth: auth, key: cacheKey(conn, server, auth)}
	return cfg, nil
}

// ServerConfig builds the part of a client configuration a BaoServer defines: address, CA bundle, request
// timeout and rate limit.
func (r *Resolver) ServerConfig(ctx context.Context, server *corev1alpha1.BaoServer) (openbao.Config, error) {
	if !strings.HasPrefix(server.Spec.Address, "https://") && !server.Spec.AllowInsecureHTTP {
		return openbao.Config{}, ErrInsecureAddress
	}
	cfg := openbao.Config{Address: server.Spec.Address}
	if server.Spec.RequestTimeout != nil {
		cfg.Timeout = server.Spec.RequestTimeout.Duration
	}
	if rl := server.Spec.RateLimit; rl != nil {
		cfg.RateLimit = openbao.RateLimit{QPS: float64(rl.QPS), Burst: int(cmp.Or(rl.Burst, rl.QPS))}
	}
	if ca := serverCARef(server); ca != nil {
		bundle, err := r.value(ctx, *ca)
		if err != nil {
			return openbao.Config{}, err
		}
		cfg.CABundle = bundle
	}
	return cfg, nil
}

// admit enforces the tenant boundary: the server must select the connection's namespace and allow every
// requested audience, and no audience may be one the Kubernetes API server accepts.
func (r *Resolver) admit(ctx context.Context, s settings, server *corev1alpha1.BaoServer) error {
	if server.Spec.AllowedNamespaces == nil {
		return fmt.Errorf("%w: BaoServer %s allows no namespaces", engine.ErrConnectionDenied, server.Name)
	}
	if err := r.selects(ctx, server.Spec.AllowedNamespaces, s.tenant); err != nil {
		return fmt.Errorf("BaoServer %s: %w", server.Name, err)
	}
	if s.saLogin == nil {
		return nil
	}
	for _, audience := range s.saLogin.audiences {
		if err := r.notAPIServerAudience(audience); err != nil {
			return err
		}
		if !slices.Contains(server.Spec.AllowedAudiences, audience) {
			return fmt.Errorf("%w: audience %q is not in the allowedAudiences of BaoServer %s",
				engine.ErrConnectionDenied, audience, server.Name)
		}
	}
	return nil
}

// platformAudiences applies the API server audience rules to a ClusterBaoConnection that requests a token for a
// named ServiceAccount: audiences are required, and none may be one the Kubernetes API server accepts.
func (r *Resolver) platformAudiences(s settings) error {
	if s.saLogin == nil || s.saLogin.serviceAccount == nil {
		return nil
	}
	if len(s.saLogin.audiences) == 0 {
		return ErrMissingAudiences
	}
	for _, audience := range s.saLogin.audiences {
		if err := r.notAPIServerAudience(audience); err != nil {
			return err
		}
	}
	return nil
}

func (r *Resolver) notAPIServerAudience(audience string) error {
	if slices.Contains(builtinAPIServerAudiences, audience) || slices.Contains(r.APIServerAudiences, audience) {
		return fmt.Errorf("%w: audience %q is accepted by the Kubernetes API server", engine.ErrConnectionDenied,
			audience)
	}
	return nil
}

// selects returns engine.ErrConnectionDenied unless selector matches the labels of namespace.
func (r *Resolver) selects(ctx context.Context, selector *metav1.LabelSelector, namespace string) error {
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return fmt.Errorf("%w: invalid allowedNamespaces: %v", engine.ErrConnectionDenied, err)
	}
	ns := &metav1.PartialObjectMetadata{}
	ns.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace"))
	if err := r.Client.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
		return fmt.Errorf("get namespace %s: %w", namespace, err)
	}
	if !sel.Matches(labels.Set(ns.Labels)) {
		return fmt.Errorf("%w: namespace %s is not in allowedNamespaces", engine.ErrConnectionDenied, namespace)
	}
	return nil
}

// cachedAuth replaces the credential's cache key with one that also covers the connection and server
// generations, so the pooled client of a connection is replaced after either object changes.
type cachedAuth struct {
	openbao.Auth
	key string
}

// CacheKey implements openbao.Auth.
func (a cachedAuth) CacheKey() string { return a.key }

func cacheKey(conn client.Object, server *corev1alpha1.BaoServer, auth openbao.Auth) string {
	credential := sha256.Sum256([]byte(auth.CacheKey()))
	return fmt.Sprintf("%s/%d/%d/%s", conn.GetUID(), conn.GetGeneration(), server.Generation,
		hex.EncodeToString(credential[:]))
}

func (r *Resolver) auth(ctx context.Context, s settings) (openbao.Auth, error) {
	switch {
	case s.token != nil:
		ref := *s.token
		token, err := r.value(ctx, ref)
		if err != nil {
			return nil, err
		}
		return openbao.TokenAuth{
			Token:         r.credential(ref),
			CredentialKey: ref.String() + "@" + digest(token),
		}, nil
	case s.tokenFile != "":
		if r.automounted(s.tokenFile) {
			return nil, ErrAutomountedToken
		}
		version, err := fileVersion(s.tokenFile)
		if err != nil {
			return nil, err
		}
		return openbao.TokenAuth{
			Token:         r.fileCredential(s.tokenFile, false),
			CredentialKey: "file:" + s.tokenFile + "@" + version,
		}, nil
	case s.appRole != nil:
		roleID, err := r.value(ctx, s.appRole.roleID)
		if err != nil {
			return nil, err
		}
		secretID, err := r.value(ctx, s.appRole.secretID)
		if err != nil {
			return nil, err
		}
		return openbao.AppRoleAuth{
			MountPath: s.appRole.mountPath,
			RoleID:    r.credential(s.appRole.roleID),
			SecretID:  r.credential(s.appRole.secretID),
			CredentialKey: s.appRole.roleID.String() + "@" + digest(roleID) + "," +
				s.appRole.secretID.String() + "@" + digest(secretID),
		}, nil
	case s.saLogin != nil:
		login := *s.saLogin
		jwt, key, err := r.serviceAccountToken(login)
		if err != nil {
			return nil, err
		}
		if login.jwt {
			return openbao.JWTAuth{MountPath: login.mountPath, Role: login.role, JWT: jwt, CredentialKey: key}, nil
		}
		return openbao.KubernetesAuth{MountPath: login.mountPath, Role: login.role, JWT: jwt, CredentialKey: key}, nil
	default:
		return nil, errors.New("connection has no auth method")
	}
}

func (r *Resolver) credential(ref keyRef) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		v, err := r.value(ctx, ref)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(v)), nil
	}
}

func (r *Resolver) serviceAccountToken(login serviceAccountLogin) (
	func(context.Context) (string, error), string, error) {
	if login.serviceAccount == nil {
		path := login.tokenPath
		if path == "" {
			path = r.TokenFile
		}
		if path == "" {
			path = DefaultTokenFile
		}
		if r.automounted(path) {
			return nil, "", ErrAutomountedToken
		}
		if _, err := fileVersion(path); err != nil {
			return nil, "", err
		}
		return r.fileCredential(path, true), "tokenfile:" + path, nil
	}
	sa := *login.serviceAccount
	audiences := login.audiences
	key := "serviceaccount:" + sa.namespace + "/" + sa.name + ":" + strings.Join(audiences, ",")
	return func(ctx context.Context) (string, error) {
		return r.requestToken(ctx, sa, audiences)
	}, key, nil
}

// automounted reports whether path is in the automounted token directory, or is the automounted token under
// another name (a symlink, /proc/self/root/...).
func (r *Resolver) automounted(path string) bool {
	if strings.HasPrefix(filepath.Clean(path)+"/", automountedTokenDir) {
		return true
	}
	want, err := os.Stat(cmp.Or(r.automountFile, automountedTokenFile))
	if err != nil {
		return false
	}
	got, err := os.Stat(path)
	return err == nil && os.SameFile(got, want)
}

// fileVersion returns the modification time of a credential file, so a rewritten file, such as a renewed
// vault-agent sink, replaces the pooled client without reading the credential.
func fileVersion(path string) (string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: file %s", ErrReferenceNotFound, path)
	}
	if err != nil {
		return "", fmt.Errorf("stat credential file %s: %w", path, err)
	}
	return strconv.FormatInt(info.ModTime().UnixNano(), 10), nil
}

// fileCredential reads path at every login. The automounted token is refused again, since the file a path names can
// change; any JWT whose aud claim names an API server audience is refused before it leaves the pod, and with
// serviceAccountJWT so is content without a readable aud claim.
func (r *Resolver) fileCredential(path string, serviceAccountJWT bool) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		if r.automounted(path) {
			return "", ErrAutomountedToken
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read credential file %s: %w", path, err)
		}
		token := strings.TrimSpace(string(b))
		audiences, ok := jwtAudiences(token)
		if serviceAccountJWT && !ok {
			return "", fmt.Errorf("%w: token file %s holds no JWT with an aud claim", engine.ErrConnectionDenied, path)
		}
		for _, audience := range audiences {
			if err := r.notAPIServerAudience(audience); err != nil {
				return "", fmt.Errorf("token file %s: %w", path, err)
			}
		}
		return token, nil
	}
}

// jwtAudiences returns the aud claim of a JWT without verifying it; ok is false when token is not a JWT with a
// non-empty aud claim.
func jwtAudiences(token string) ([]string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	// A map, not a tagged struct: encoding/json matches struct tags case-insensitively, so "Aud" could hide "aud".
	var claims map[string]json.RawMessage
	if json.Unmarshal(payload, &claims) != nil {
		return nil, false
	}
	raw, found := claims["aud"]
	if !found {
		return nil, false
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, one != ""
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return nil, false
	}
	return many, len(many) > 0
}

func (r *Resolver) requestToken(ctx context.Context, sa objectRef, audiences []string) (string, error) {
	account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: sa.namespace, Name: sa.name}}
	request := &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{
		Audiences:         audiences,
		ExpirationSeconds: ptr.To[int64](tokenExpirationSeconds),
	}}
	if err := r.Client.SubResource("token").Create(ctx, account, request); err != nil {
		return "", fmt.Errorf("request token for serviceaccount %s/%s: %w", sa.namespace, sa.name, err)
	}
	return request.Status.Token, nil
}

// credentialHashKey keys digest with a per-process secret, so a credential key that surfaces in a dump cannot be
// used to guess a low-entropy Secret value offline.
var credentialHashKey = func() []byte {
	key := make([]byte, sha256.Size)
	_, _ = rand.Read(key)
	return key
}()

// digest identifies credential bytes in a cache key: a change of the value replaces the pooled client, while a
// metadata-only edit of the object holding it does not.
func digest(data []byte) string {
	mac := hmac.New(sha256.New, credentialHashKey)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// value returns the referenced bytes.
func (r *Resolver) value(ctx context.Context, ref keyRef) ([]byte, error) {
	key := client.ObjectKey{Namespace: ref.namespace, Name: ref.name}
	var (
		data  []byte
		found bool
		err   error
	)
	switch ref.kind {
	case kindSecret:
		secret := &corev1.Secret{}
		err = r.Client.Get(ctx, key, secret)
		data, found = secret.Data[ref.key]
	case kindConfigMap:
		cm := &corev1.ConfigMap{}
		err = r.Client.Get(ctx, key, cm)
		if s, ok := cm.Data[ref.key]; ok {
			data, found = []byte(s), true
		} else {
			data, found = cm.BinaryData[ref.key]
		}
	default:
		return nil, fmt.Errorf("unsupported reference kind %q", ref.kind)
	}
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("%w: %s %s/%s", ErrReferenceNotFound, ref.kind, ref.namespace, ref.name)
	}
	if err != nil {
		return nil, fmt.Errorf("get %s %s/%s: %w", ref.kind, ref.namespace, ref.name, err)
	}
	if !found {
		return nil, fmt.Errorf("%w: key %q in %s %s/%s", ErrReferenceNotFound, ref.key, ref.kind, ref.namespace,
			ref.name)
	}
	return data, nil
}
