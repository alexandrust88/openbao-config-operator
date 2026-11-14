package secrets

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const pkiDefaultMount = "pki"

func pkiMount(mountPath string) string {
	if mountPath == "" {
		return pkiDefaultMount
	}
	return mountPath
}

func pkiOptList(p engine.Payload, key string, v []string) {
	if v != nil {
		p[key] = v
	}
}

// pkiOptDurationString is for PKI fields that OpenBao stores as duration strings rather than seconds.
func pkiOptDurationString(p engine.Payload, key string, d *metav1.Duration) {
	if d != nil {
		p[key] = d.Duration.String()
	}
}

// pkiIsMissing reports whether a read of an issuer or key reference failed because the reference does not
// resolve: OpenBao answers that with a 500 "unable to find PKI <what> for reference" instead of a 404.
func pkiIsMissing(err error, what string) bool {
	if errors.Is(err, openbao.ErrNotFound) {
		return true
	}
	var response *openbao.ResponseError
	if !errors.As(err, &response) || response.StatusCode != http.StatusInternalServerError {
		return false
	}
	for _, message := range response.Errors {
		if strings.Contains(message, "unable to find PKI "+what+" for reference") {
			return true
		}
	}
	return false
}

// pkiReadRef reads <mount>/<kind>/<ref> and reports found=false when the reference does not resolve.
func pkiReadRef(ctx context.Context, c openbao.Client, mount, kind, ref string) (engine.Payload, bool, error) {
	path := mount + "/" + kind + "/" + ref
	data, err := c.Read(ctx, path)
	if pkiIsMissing(err, kind) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return data, true, nil
}

// pkiDeleteRef deletes <mount>/<kind>/<ref>; a reference that no longer resolves counts as deleted.
func pkiDeleteRef(ctx context.Context, c openbao.Client, mount, kind, ref string) error {
	if err := c.Delete(ctx, mount+"/"+kind+"/"+ref); err != nil && !pkiIsMissing(err, kind) {
		return fmt.Errorf("delete %s/%s/%s: %w", mount, kind, ref, err)
	}
	return nil
}

// pkiReadyIssuer returns the referenced PKIIssuer once it is Ready and has an issuer ID.
func pkiReadyIssuer(ctx context.Context, r engine.Resolver, namespace, name string) (*secretsv1alpha1.PKIIssuer, error) {
	issuer := &secretsv1alpha1.PKIIssuer{}
	if err := r.Object(ctx, client.ObjectKey{Namespace: namespace, Name: name}, issuer); err != nil {
		return nil, fmt.Errorf("PKIIssuer %s/%s: %w", namespace, name, err)
	}
	if issuer.Status.ID == "" || !pkiReady(issuer.Status.Conditions) {
		return nil, fmt.Errorf("PKIIssuer %s/%s is not Ready: %w", namespace, name, engine.ErrReferenceNotReady)
	}
	return issuer, nil
}

// pkiIssuerRef resolves ref to an issuer reference (name or ID) that OpenBao accepts in mount. A PKIIssuer
// resource must live in the same PKI mount and OpenBao namespace as the referencing resource.
func pkiIssuerRef(ctx context.Context, r engine.Resolver, obj client.Object, spec *commonv1alpha1.ResourceSpec,
	mount string, ref secretsv1alpha1.PKIIssuerReference) (string, error) {
	if ref.IssuerRef == nil {
		return ref.IssuerName, nil
	}
	issuer, err := pkiReadyIssuer(ctx, r, obj.GetNamespace(), ref.IssuerRef.Name)
	if err != nil {
		return "", err
	}
	if got := pkiMount(issuer.Spec.MountPath); got != mount || issuer.Spec.Namespace != spec.Namespace {
		return "", fmt.Errorf("PKIIssuer %s is in mount %q of OpenBao namespace %q, want mount %q of namespace %q",
			issuer.Name, got, issuer.Spec.Namespace, mount, spec.Namespace)
	}
	return issuer.Status.ID, nil
}

func pkiReady(conditions []metav1.Condition) bool {
	return meta.IsStatusConditionTrue(conditions, commonv1alpha1.ConditionReady)
}

func pkiString(v any) string {
	s, _ := v.(string)
	return s
}
