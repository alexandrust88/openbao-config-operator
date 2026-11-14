package importer

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
)

// Server describes the OpenBaoServer to emit with the resources, and the connection to it that they reference.
type Server struct {
	// Name of the cluster-scoped OpenBaoServer.
	Name string
	// Address of the OpenBao API.
	Address string
	// Namespace is the OpenBao namespace the connection is based in, the one that was imported.
	Namespace string
	// CABundle references a Secret <Name>-ca, key ca.crt, in CANamespace for the server's CA bundle.
	CABundle bool
	// CANamespace holds the CA bundle Secret: a platform namespace, never the resources' namespace, whose Secret
	// writers would control TLS trust for every connection to the server.
	CANamespace string
}

// tokenAudience is the audience the emitted connection requests for its ServiceAccount token.
const tokenAudience = "openbao"

// namespaceNameLabel is set by Kubernetes on every Namespace to its name.
const namespaceNameLabel = "kubernetes.io/metadata.name"

// serverObjects returns the OpenBaoServer and the connection named by opts.Connection. The connection logs in with
// Kubernetes auth as the ServiceAccount named after it, with the OpenBao role of the same name; a note says so.
func (imp *importer) serverObjects() []client.Object {
	opts, srv := imp.opts, imp.opts.Server
	allowed := &metav1.LabelSelector{MatchLabels: map[string]string{namespaceNameLabel: opts.Namespace}}
	server := &corev1alpha1.OpenBaoServer{}
	server.SetGroupVersionKind(corev1alpha1.GroupVersion.WithKind("OpenBaoServer"))
	server.Name = srv.Name
	server.Spec.Address = srv.Address
	server.Spec.AllowInsecureHTTP = strings.HasPrefix(srv.Address, "http://")
	server.Spec.AllowedAudiences = []string{tokenAudience}
	if srv.CABundle {
		ref := &corev1alpha1.NamespacedCABundleReference{Namespace: srv.CANamespace, Name: srv.Name + "-ca",
			Key: corev1alpha1.DefaultCABundleKey}
		server.Spec.CABundle = &corev1alpha1.CABundleSource{SecretRef: ref}
		imp.result.Notes = append(imp.result.Notes, fmt.Sprintf("OpenBaoServer/%s trusts the CA bundle in Secret "+
			"%s/%s key %s: create it there, in a namespace only the platform team can write", srv.Name, ref.Namespace,
			ref.Name, ref.Key))
	}
	connSpec := corev1alpha1.ConnectionSpec{ServerRef: corev1alpha1.ServerReference{Name: srv.Name},
		Namespace: srv.Namespace}
	name := opts.Connection.Name
	imp.result.Notes = append(imp.result.Notes, fmt.Sprintf("%s/%s logs in with Kubernetes auth role %s as "+
		"ServiceAccount %s/%s (audience %s): create both, or edit spec.auth", opts.Connection.Kind, name, name,
		opts.Namespace, name, tokenAudience))

	if opts.Connection.Kind == commonv1alpha1.ConnectionKindCluster {
		conn := &corev1alpha1.ClusterOpenBaoConnection{}
		conn.SetGroupVersionKind(corev1alpha1.GroupVersion.WithKind("ClusterOpenBaoConnection"))
		conn.Name = name
		conn.Spec.ConnectionSpec = connSpec
		conn.Spec.AllowedNamespaces = allowed
		conn.Spec.Auth.Kubernetes = &corev1alpha1.ClusterKubernetesAuth{Role: name, Audiences: []string{tokenAudience},
			ServiceAccountRef: &corev1alpha1.NamespacedObjectReference{Namespace: opts.Namespace, Name: name}}
		return []client.Object{server, conn}
	}
	server.Spec.AllowedNamespaces = allowed
	conn := &corev1alpha1.OpenBaoConnection{}
	conn.SetGroupVersionKind(corev1alpha1.GroupVersion.WithKind("OpenBaoConnection"))
	conn.Name, conn.Namespace = name, opts.Namespace
	conn.Spec.ConnectionSpec = connSpec
	conn.Spec.Auth.Kubernetes = &corev1alpha1.KubernetesAuth{Role: name, Audiences: []string{tokenAudience},
		ServiceAccountRef: &commonv1alpha1.LocalObjectReference{Name: name}}
	return []client.Object{server, conn}
}
