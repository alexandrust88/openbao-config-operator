package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=approlesecretids,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=approlesecretids/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=approlesecretids/finalizers,verbs=update
// +kubebuilder:rbac:groups=auth.openbaoconfig.io,resources=approles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

// Keys of the AppRoleSecretID payload that are not sent to OpenBao: the role the secret ID belongs to, and the
// accessor of the secret ID that the output Secret holds (desired) or that OpenBao holds (observed).
const (
	secretIDRolePathKey = "role_path"
	secretIDAccessorKey = "secret_id_accessor"
)

// maxUndeliveredSecretIDs caps the retired secret IDs that never reached the output Secret: while delivery keeps
// failing, every retry generates one more live secret ID.
const maxUndeliveredSecretIDs = 3

// SetupAppRoleSecretID registers the AppRoleSecretID controller with the manager.
func SetupAppRoleSecretID(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*authv1alpha1.AppRoleSecretID](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("approlesecretid-controller"),
		conns,
		AppRoleSecretIDAdapter{},
		func() *authv1alpha1.AppRoleSecretID { return &authv1alpha1.AppRoleSecretID{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// AppRoleSecretIDAdapter generates a secret ID at auth/<mount>/role/<name>/secret-id and keeps the Secret named by
// writeSecretRef current. A new secret ID is generated when none is recorded, when OpenBao no longer has it, when
// it is due for rotation, or when the Secret does not hold it; the replaced ones are destroyed after the Secret
// is updated.
type AppRoleSecretIDAdapter struct {
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

var (
	_ engine.Adapter[*authv1alpha1.AppRoleSecretID]              = AppRoleSecretIDAdapter{}
	_ engine.DriftTier                                           = AppRoleSecretIDAdapter{}
	_ engine.Observer[*authv1alpha1.AppRoleSecretID]             = AppRoleSecretIDAdapter{}
	_ engine.Writer[*authv1alpha1.AppRoleSecretID]               = AppRoleSecretIDAdapter{}
	_ engine.OutputWriter[*authv1alpha1.AppRoleSecretID]         = AppRoleSecretIDAdapter{}
	_ engine.StatusWriter[*authv1alpha1.AppRoleSecretID]         = AppRoleSecretIDAdapter{}
	_ engine.OutputsDeliveredHook[*authv1alpha1.AppRoleSecretID] = AppRoleSecretIDAdapter{}
	_ engine.Deleter[*authv1alpha1.AppRoleSecretID]              = AppRoleSecretIDAdapter{}
	_ engine.SecretReferencer[*authv1alpha1.AppRoleSecretID]     = AppRoleSecretIDAdapter{}
	_ engine.SharedPath                                          = AppRoleSecretIDAdapter{}
	_ engine.PathResolver[*authv1alpha1.AppRoleSecretID]         = AppRoleSecretIDAdapter{}

	_ engine.OutputSecretNamer[*authv1alpha1.AppRoleSecretID] = AppRoleSecretIDAdapter{}
)

// SharedPath implements engine.SharedPath: every secret ID of a role is generated at the role's secret-id path and
// is told apart by the accessor in its status, so several AppRoleSecretIDs may name one role.
func (AppRoleSecretIDAdapter) SharedPath() bool { return true }

// Path implements engine.Adapter. With roleRef and no recorded role path it is empty; ResolvePath reads the
// AppRole instead.
func (AppRoleSecretIDAdapter) Path(s *authv1alpha1.AppRoleSecretID) string {
	if s.Status.RolePath != "" {
		return s.Status.RolePath + "/secret-id"
	}
	if s.Spec.RoleRef != nil {
		return ""
	}
	return literalSecretIDRolePath(s) + "/secret-id"
}

// ResolvePath implements engine.PathResolver: the recorded role path, else the referenced AppRole's mount and role
// name, so the engine never claims a path the secret ID is not generated at.
func (a AppRoleSecretIDAdapter) ResolvePath(ctx context.Context, r engine.Resolver, _ openbao.Client,
	s *authv1alpha1.AppRoleSecretID) (string, error) {
	if path := a.Path(s); path != "" {
		return path, nil
	}
	rolePath, err := resolveSecretIDRolePath(ctx, r, s)
	if err != nil {
		return "", err
	}
	return rolePath + "/secret-id", nil
}

// Desired implements engine.Adapter. Besides the secret ID parameters it carries the resolved role path and the
// accessor found in the output Secret ("" when the Secret or its secret_id is missing).
func (AppRoleSecretIDAdapter) Desired(ctx context.Context, r engine.Resolver, s *authv1alpha1.AppRoleSecretID) (engine.Payload, error) {
	rolePath, err := resolveSecretIDRolePath(ctx, r, s)
	if err != nil {
		return nil, err
	}
	delivered, err := deliveredSecretIDAccessor(ctx, r, s)
	if err != nil {
		return nil, err
	}
	p, err := secretIDParams(s)
	if err != nil {
		return nil, err
	}
	p[secretIDRolePathKey] = rolePath
	p[secretIDAccessorKey] = delivered
	return p, nil
}

// Fields implements engine.Adapter.
func (AppRoleSecretIDAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// DriftTier implements engine.DriftTier.
func (AppRoleSecretIDAdapter) DriftTier() engine.Tier { return engine.TierAccessCritical }

// SecretRefs implements engine.SecretReferencer, so deleting the output Secret triggers a new secret ID.
func (AppRoleSecretIDAdapter) SecretRefs(s *authv1alpha1.AppRoleSecretID) []string {
	return []string{s.Spec.WriteSecretRef.Name}
}

// Observe implements engine.Observer. A secret ID cannot change, so its parameters are reported as the spec has
// them; only the accessor is compared.
func (a AppRoleSecretIDAdapter) Observe(ctx context.Context, c openbao.Client, s *authv1alpha1.AppRoleSecretID) (engine.Payload, bool, error) {
	if s.Status.ID == "" || s.Status.RolePath == "" {
		return nil, false, nil
	}
	data, err := lookupSecretID(ctx, c, s.Status.RolePath, s.Status.ID)
	if err != nil || data == nil {
		return nil, false, err
	}
	if a.rotationDue(s, data) {
		return nil, false, nil
	}
	observed, err := secretIDParams(s)
	if err != nil {
		return nil, false, err
	}
	observed[secretIDRolePathKey] = s.Status.RolePath
	observed[secretIDAccessorKey] = s.Status.ID
	observed["expiration_time"] = data["expiration_time"]
	return observed, true, nil
}

// Write implements engine.Writer. It generates a new secret ID and records the replaced accessors in the status;
// OutputsDelivered destroys them once the engine has written the output Secret.
func (AppRoleSecretIDAdapter) Write(ctx context.Context, c openbao.Client, s *authv1alpha1.AppRoleSecretID, desired engine.Payload, _ bool) (engine.Payload, error) {
	rolePath, _ := desired[secretIDRolePathKey].(string)
	if rolePath == "" {
		return nil, errors.New("role path is not resolved")
	}
	roleIDData, err := c.Read(ctx, rolePath+"/role-id")
	if err != nil {
		return nil, fmt.Errorf("read %s/role-id: %w", rolePath, err)
	}
	roleID, _ := roleIDData["role_id"].(string)
	params, err := secretIDParams(s)
	if err != nil {
		return nil, err
	}
	retired := slices.Clone(s.Status.RetiredAccessors)
	if s.Status.RolePath == rolePath {
		retired = append(retired, s.Status.ID)
	}
	delivered, _ := desired[secretIDAccessorKey].(string)
	retired, err = destroyUndeliveredOverflow(ctx, c, rolePath, uniqueAccessors(append(retired, delivered)), delivered)
	if err != nil {
		return nil, err
	}
	resp, err := c.Write(ctx, rolePath+"/secret-id", params)
	if err != nil {
		return nil, fmt.Errorf("write %s/secret-id: %w", rolePath, err)
	}
	secretID, _ := resp["secret_id"].(string)
	accessor, _ := resp[secretIDAccessorKey].(string)
	if secretID == "" || accessor == "" {
		return nil, fmt.Errorf("write %s/secret-id: response has no secret_id or secret_id_accessor", rolePath)
	}

	s.Status.RetiredAccessors = slices.DeleteFunc(retired, func(v string) bool { return v == accessor })
	s.Status.ID = accessor
	s.Status.RolePath = rolePath
	s.Status.ExpirationTime = nil
	return engine.Payload{"role_id": roleID, "secret_id": secretID, secretIDAccessorKey: accessor}, nil
}

// Outputs implements engine.OutputWriter.
func (AppRoleSecretIDAdapter) Outputs(s *authv1alpha1.AppRoleSecretID, response engine.Payload) (string, map[string][]byte, bool) {
	secretID, _ := response["secret_id"].(string)
	if secretID == "" {
		return "", nil, false
	}
	roleID, _ := response["role_id"].(string)
	accessor, _ := response[secretIDAccessorKey].(string)
	return s.Spec.WriteSecretRef.Name, map[string][]byte{
		"role_id":           []byte(roleID),
		"secret_id":         []byte(secretID),
		secretIDAccessorKey: []byte(accessor),
	}, true
}

// OutputSecretName implements engine.OutputSecretNamer.
func (AppRoleSecretIDAdapter) OutputSecretName(s *authv1alpha1.AppRoleSecretID) string {
	return s.Spec.WriteSecretRef.Name
}

// WriteStatus implements engine.StatusWriter: it records the expiry.
func (AppRoleSecretIDAdapter) WriteStatus(_ context.Context, _ openbao.Client, s *authv1alpha1.AppRoleSecretID, observed engine.Payload) error {
	s.Status.ExpirationTime = nil
	if exp, ok := secretIDExpiry(observed["expiration_time"]); ok {
		s.Status.ExpirationTime = &metav1.Time{Time: exp}
	}
	return nil
}

// OutputsDelivered implements engine.OutputsDeliveredHook: it destroys the retired secret IDs, which is safe because
// the engine calls it only in Manage, once the output Secret holds the current secret ID.
func (AppRoleSecretIDAdapter) OutputsDelivered(ctx context.Context, c openbao.Client, s *authv1alpha1.AppRoleSecretID) error {
	var kept []string
	var errs []error
	for _, accessor := range s.Status.RetiredAccessors {
		if err := destroySecretID(ctx, c, s.Status.RolePath, accessor); err != nil {
			kept = append(kept, accessor)
			errs = append(errs, err)
		}
	}
	s.Status.RetiredAccessors = kept
	return errors.Join(errs...)
}

// Delete implements engine.Deleter: it destroys the current and any retired secret IDs.
func (AppRoleSecretIDAdapter) Delete(ctx context.Context, c openbao.Client, s *authv1alpha1.AppRoleSecretID) error {
	if s.Status.RolePath == "" {
		return nil
	}
	var errs []error
	for _, accessor := range append(slices.Clone(s.Status.RetiredAccessors), s.Status.ID) {
		if accessor != "" {
			errs = append(errs, destroySecretID(ctx, c, s.Status.RolePath, accessor))
		}
	}
	return errors.Join(errs...)
}

func (a AppRoleSecretIDAdapter) rotationDue(s *authv1alpha1.AppRoleSecretID, lookup engine.Payload) bool {
	rotation := s.Spec.Rotation
	if rotation != nil && rotation.Enabled != nil && !*rotation.Enabled {
		return false
	}
	ttlSeconds, ok := credInt64(lookup["secret_id_ttl"])
	if !ok || ttlSeconds <= 0 {
		return false
	}
	exp, ok := secretIDExpiry(lookup["expiration_time"])
	if !ok {
		return false
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	ttl := time.Duration(ttlSeconds) * time.Second
	return !now().Before(exp.Add(-renewBefore(rotation, ttl)))
}

// renewBefore returns the rotation window; a window that is not shorter than the TTL falls back to a third of
// the TTL, otherwise every new secret ID would be due at once.
func renewBefore(rotation *authv1alpha1.SecretIDRotation, ttl time.Duration) time.Duration {
	fallback := ttl / 3
	if rotation == nil || rotation.RenewBefore == "" {
		return fallback
	}
	var window time.Duration
	if percent, ok := strings.CutSuffix(rotation.RenewBefore, "%"); ok {
		n, err := strconv.Atoi(percent)
		if err != nil {
			return fallback
		}
		window = ttl * time.Duration(n) / 100
	} else {
		d, err := time.ParseDuration(rotation.RenewBefore)
		if err != nil {
			return fallback
		}
		window = d
	}
	if window <= 0 || window >= ttl {
		return fallback
	}
	return window
}

func secretIDExpiry(v any) (time.Time, bool) {
	text, ok := v.(string)
	if !ok || text == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || t.Year() <= 1 {
		return time.Time{}, false
	}
	return t, true
}

// secretIDParams is the body of the secret-id create request.
func secretIDParams(s *authv1alpha1.AppRoleSecretID) (engine.Payload, error) {
	p := engine.Payload{}
	if len(s.Spec.Metadata) > 0 {
		encoded, err := json.Marshal(s.Spec.Metadata)
		if err != nil {
			return nil, fmt.Errorf("encode metadata: %w", err)
		}
		p["metadata"] = string(encoded)
	}
	putList(p, "cidr_list", s.Spec.CIDRList)
	putList(p, keyTokenBoundCIDRs, s.Spec.TokenBoundCIDRs)
	putSeconds(p, "ttl", s.Spec.TTL)
	credPutInt32(p, "num_uses", s.Spec.NumUses)
	return p, nil
}

func literalSecretIDRolePath(s *authv1alpha1.AppRoleSecretID) string {
	return "auth/" + authMount(s.Spec.MountPath, "approle") + "/role/" + s.Spec.RoleName
}

func resolveSecretIDRolePath(ctx context.Context, r engine.Resolver, s *authv1alpha1.AppRoleSecretID) (string, error) {
	if s.Spec.RoleRef == nil {
		return literalSecretIDRolePath(s), nil
	}
	var role authv1alpha1.AppRole
	if err := r.Object(ctx, client.ObjectKey{Namespace: s.Namespace, Name: s.Spec.RoleRef.Name}, &role); err != nil {
		return "", fmt.Errorf("get AppRole %s: %w", s.Spec.RoleRef.Name, err)
	}
	if role.Spec.ConnectionRef != s.Spec.ConnectionRef || role.Spec.Namespace != s.Spec.Namespace {
		return "", fmt.Errorf("AppRole %s uses a different connectionRef or namespace", role.Name)
	}
	if !meta.IsStatusConditionTrue(role.Status.Conditions, commonv1alpha1.ConditionReady) {
		return "", fmt.Errorf("AppRole %s is not ready: %w", role.Name, engine.ErrReferenceNotReady)
	}
	return appRolePath(&role), nil
}

func deliveredSecretIDAccessor(ctx context.Context, r engine.Resolver, s *authv1alpha1.AppRoleSecretID) (string, error) {
	var secret corev1.Secret
	err := r.Object(ctx, client.ObjectKey{Namespace: s.Namespace, Name: s.Spec.WriteSecretRef.Name}, &secret)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get Secret %s: %w", s.Spec.WriteSecretRef.Name, err)
	}
	if len(secret.Data["secret_id"]) == 0 {
		return "", nil
	}
	return string(secret.Data[secretIDAccessorKey]), nil
}

// lookupSecretID returns nil when the role, the accessor or an unexpired secret ID is missing. The role is read
// first because a lookup under a missing role is a 500, not a 404.
func lookupSecretID(ctx context.Context, c openbao.Client, rolePath, accessor string) (engine.Payload, error) {
	if _, err := c.Read(ctx, rolePath); errors.Is(err, openbao.ErrNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("read %s: %w", rolePath, err)
	}
	data, err := c.Write(ctx, rolePath+"/secret-id-accessor/lookup", engine.Payload{secretIDAccessorKey: accessor})
	if errors.Is(err, openbao.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up secret ID accessor under %s: %w", rolePath, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// uniqueAccessors drops empty and repeated accessors and keeps the order, oldest first.
func uniqueAccessors(accessors []string) []string {
	var out []string
	for _, a := range accessors {
		if a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// destroyUndeliveredOverflow destroys the oldest retired secret IDs beyond maxUndeliveredSecretIDs, never the one
// the output Secret holds, and returns the remaining ones.
func destroyUndeliveredOverflow(ctx context.Context, c openbao.Client, rolePath string, retired []string,
	delivered string) ([]string, error) {
	undelivered := len(retired)
	if slices.Contains(retired, delivered) {
		undelivered--
	}
	for i := 0; undelivered > maxUndeliveredSecretIDs && i < len(retired); {
		if retired[i] == delivered {
			i++
			continue
		}
		if err := destroySecretID(ctx, c, rolePath, retired[i]); err != nil {
			return nil, err
		}
		retired = slices.Delete(retired, i, i+1)
		undelivered--
	}
	return retired, nil
}

// destroySecretID destroys a secret ID by accessor; a missing one counts as destroyed, because destroying an
// unknown accessor is a 500.
func destroySecretID(ctx context.Context, c openbao.Client, rolePath, accessor string) error {
	data, err := lookupSecretID(ctx, c, rolePath, accessor)
	if err != nil || data == nil {
		return err
	}
	if _, err := c.Write(ctx, rolePath+"/secret-id-accessor/destroy", engine.Payload{secretIDAccessorKey: accessor}); err != nil {
		return fmt.Errorf("destroy secret ID accessor under %s: %w", rolePath, err)
	}
	return nil
}
