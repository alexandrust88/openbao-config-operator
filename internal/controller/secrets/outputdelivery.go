package secrets

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// Key of the output Secret that names the generated key, so a Secret left over from an earlier key is not taken
// for the current one.
const outputKeyIDKey = "key_id"

// oneTimeOutput is a value OpenBao returned once and that only the output Secret holds.
type oneTimeOutput struct {
	// secret is the output Secret; "" means the resource expects no output.
	secret string
	// generated reports that this resource has had OpenBao generate the value.
	generated bool
	// valueKey holds the value; idKey, when the Secret has it, must equal id.
	valueKey, idKey, id string
	// what names the value in the condition message.
	what string
}

// check returns an error wrapping engine.ErrOutputNotDelivered when the output Secret does not hold the value of the
// object that exists in OpenBao, for example because the Secret write failed after OpenBao generated it. The value
// cannot be read again, so the resource stays non-Ready until the user has a new one generated.
func (o oneTimeOutput) check(ctx context.Context, secrets client.Reader, obj engine.Object) error {
	if o.secret == "" || !o.generated || obj.GetResourceStatus().Acquired == commonv1alpha1.AcquiredAdopted ||
		obj.GetResourceSpec().ManagementPolicy == commonv1alpha1.ManagementPolicyObserve {
		return nil
	}
	if secrets == nil {
		return errors.New("no Secret reader to confirm the one-time output")
	}
	var secret corev1.Secret
	err := secrets.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: o.secret}, &secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("read Secret %s: %w", o.secret, err)
	}
	if err == nil && metav1.IsControlledBy(&secret, obj) && len(secret.Data[o.valueKey]) > 0 {
		if stored, ok := secret.Data[o.idKey]; !ok || string(stored) == o.id {
			return nil
		}
	}
	return fmt.Errorf("%s was returned by OpenBao once and is not in Secret %s; OpenBao cannot return it again: "+
		"delete the object in OpenBao and this resource generates a new one, or set deletionPolicy Delete, delete "+
		"this resource, which deletes the object too, and create it again: %w", o.what, o.secret, engine.ErrOutputNotDelivered)
}
