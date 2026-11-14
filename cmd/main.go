/*
Copyright 2026 The openbao-config-operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/alexandrust88/openbao-config-operator/internal/connection"
	corecontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/core"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	version = "dev"
	commit  = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	for _, add := range apiSchemes {
		utilruntime.Must(add(scheme))
	}
}

func main() {
	if err := run(); err != nil {
		setupLog.Error(err, "Operator failed")
		os.Exit(1)
	}
}

func run() error {
	var (
		metricsAddr, probeAddr, pprofAddr                string
		metricsCertPath, metricsCertName, metricsCertKey string
		watchNamespaces, hashKeySecret                   string
		apiServerAudiences, clusterID                    string
		enableLeaderElection, secureMetrics, enableHTTP2 bool
		enablePluginKinds                                bool
		driftCritical, driftStandard                     time.Duration
		maxConcurrentReconciles, maxConcurrentHighVolume int
		writeBudgetPerMinute                             int
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0",
		"The address the metrics endpoint binds to. Use :8443 for HTTPS or :8080 for HTTP, or 0 to disable it.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&pprofAddr, "pprof-bind-address", "0",
		"The address the pprof endpoint binds to, such as 127.0.0.1:8082, or 0 to disable it.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for the controller manager; creates are serialised per process, so several replicas need it.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true, "Serve the metrics endpoint over HTTPS with authn/authz.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "Enable HTTP/2 for the metrics server.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "",
		"Comma-separated namespaces to watch for namespaced resources and Secrets. Empty watches all namespaces.")
	flag.DurationVar(&driftCritical, "drift-interval-critical", engine.DefaultDriftIntervalCritical,
		"Base interval between drift checks of kinds that grant access (policies, auth, groups); jittered by +-20%.")
	flag.DurationVar(&driftStandard, "drift-interval-standard", engine.DefaultDriftIntervalStandard,
		"Base interval between drift checks of every other kind; jittered by +-20%.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 4, "Maximum concurrent reconciles per kind.")
	flag.IntVar(&maxConcurrentHighVolume, "max-concurrent-reconciles-high-volume", 8,
		"Maximum concurrent reconciles of the kinds that usually have many objects (roles, policies, entities, groups).")
	flag.IntVar(&writeBudgetPerMinute, "write-budget-per-minute", 60,
		"Maximum rewrites per minute per OpenBaoServer of objects the operator already owns with an unchanged "+
			"spec; creates, adoptions and spec changes are exempt. -1 disables.")
	flag.BoolVar(&enablePluginKinds, "enable-plugin-kinds", false,
		"Run the controllers of kinds that need an external OpenBao plugin (cloud, GitHub, Okta, LDAP, Kerberos, "+
			"RADIUS, Consul, Nomad, Quay, Terraform Cloud, MongoDB Atlas); their CRDs must be installed.")
	flag.StringVar(&clusterID, "cluster-id", "",
		"Identifies this cluster in the ownership markers written to OpenBao. Empty uses the UID of the "+
			"kube-system namespace.")
	flag.StringVar(&apiServerAudiences, "apiserver-audiences", "",
		"Comma-separated audiences the Kubernetes API server accepts besides its discovered service account issuer; "+
			"namespaced connections are never issued tokens for them.")
	flag.StringVar(&hashKeySecret, "hash-key-secret", "openbao-config-operator-hash-key",
		"Secret in the operator namespace that holds the key of status.writeOnlyHash; created when absent.")
	zapOpts := zap.Options{}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	var tlsOpts []func(*tls.Config)
	// HTTP/2 stays off by default because of the stream cancellation and rapid reset CVEs
	// (GHSA-qppj-fm5r-hxr3, GHSA-4374-p667-p6c8).
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) { c.NextProtos = []string{"http/1.1"} })
	}
	metricsOptions := metricsserver.Options{BindAddress: metricsAddr, SecureServing: secureMetrics, TLSOpts: tlsOpts}
	if secureMetrics {
		metricsOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	if metricsCertPath != "" {
		metricsOptions.CertDir = metricsCertPath
		metricsOptions.CertName = metricsCertName
		metricsOptions.KeyName = metricsCertKey
	}

	operatorNamespace, err := podNamespace()
	if err != nil {
		return err
	}
	restConfig := ctrl.GetConfigOrDie()
	cacheOpts := cacheOptions(watchNamespaces, operatorNamespace)
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsOptions,
		HealthProbeBindAddress:  probeAddr,
		PprofBindAddress:        pprofAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "openbao-config-operator.openbaoconfig.io",
		LeaderElectionNamespace: operatorNamespace,
		Cache:                   cacheOpts,
		Client:                  clientOptions(),
	})
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}

	ctx := ctrl.SetupSignalHandler()
	hashKey, err := engine.LoadOrCreateHashKey(ctx, mgr.GetAPIReader(), mgr.GetClient(),
		operatorNamespace, hashKeySecret)
	if err != nil {
		return fmt.Errorf("load hash key: %w", err)
	}

	if clusterID == "" {
		if clusterID, err = kubeSystemUID(ctx, mgr.GetAPIReader()); err != nil {
			return fmt.Errorf("cluster id: set --cluster-id: %w", err)
		}
	}
	audiences, err := connection.APIServerAudiences(ctx, restConfig, splitList(apiServerAudiences))
	if err != nil {
		setupLog.Error(err, "Failed to discover the API server issuer; denying the built-in and configured audiences")
	}
	pool := openbao.NewPool()
	conns, err := corecontroller.SetupConnectionControllers(mgr, pool, audiences)
	if err != nil {
		return fmt.Errorf("set up connection controllers: %w", err)
	}
	opts := engine.Options{
		DriftIntervalCritical: driftCritical,
		DriftIntervalStandard: driftStandard,
		HashKey:               hashKey,
		WriteBudgetPerMinute:  writeBudgetPerMinute,
		ClusterID:             clusterID,
		Namespaces:            slices.Sorted(maps.Keys(cacheOpts.DefaultNamespaces)),
	}
	controllers := kindControllers
	if enablePluginKinds {
		if err := requireCRDs(mgr.GetRESTMapper(), pluginKindControllers); err != nil {
			return err
		}
		controllers = slices.Concat(kindControllers, pluginKindControllers)
	} else {
		setupLog.Info("Plugin kinds disabled", "kinds", len(pluginKindControllers))
	}
	for _, c := range controllers {
		opts.MaxConcurrentReconciles = maxConcurrentReconciles
		if c.highVolume {
			opts.MaxConcurrentReconciles = maxConcurrentHighVolume
		}
		if err := c.setup(mgr, conns, opts); err != nil {
			return fmt.Errorf("set up %s controller: %w", c.name, err)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("add health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("add ready check: %w", err)
	}

	setupLog.Info("Starting manager", "version", version, "commit", commit)
	return mgr.Start(ctx)
}

// cacheOptions limits the cache to the watched namespaces; the operator namespace stays cached for the
// hash-key Secret. Cached Secrets and ConfigMaps keep only metadata: their watches need change events, not values.
func cacheOptions(watchNamespaces, operatorNamespace string) cache.Options {
	opts := cache.Options{ByObject: map[client.Object]cache.ByObject{
		&corev1.Secret{}:    {Transform: stripData},
		&corev1.ConfigMap{}: {Transform: stripData},
	}}
	for _, ns := range splitList(watchNamespaces) {
		if opts.DefaultNamespaces == nil {
			opts.DefaultNamespaces = map[string]cache.Config{operatorNamespace: {}}
		}
		opts.DefaultNamespaces[ns] = cache.Config{}
	}
	return opts
}

// requireCRDs fails fast when a kind's CRD is missing; the manager would otherwise die later on a cache sync timeout.
func requireCRDs(mapper meta.RESTMapper, controllers []kindController) error {
	for _, c := range controllers {
		group, kind, _ := strings.Cut(c.name, "/")
		gk := schema.GroupKind{Group: group + ".openbaoconfig.io", Kind: kind}
		if _, err := mapper.RESTMapping(gk, "v1alpha1"); err != nil {
			return fmt.Errorf("--enable-plugin-kinds: the CRD of %s is not installed (chart value pluginKinds.enabled): %w",
				c.name, err)
		}
	}
	return nil
}

// kubeSystemUID returns the UID of the kube-system namespace, which is stable for the life of a cluster.
func kubeSystemUID(ctx context.Context, reader client.Reader) (string, error) {
	var ns corev1.Namespace
	if err := reader.Get(ctx, client.ObjectKey{Name: metav1.NamespaceSystem}, &ns); err != nil {
		return "", fmt.Errorf("read namespace %s: %w", metav1.NamespaceSystem, err)
	}
	return string(ns.UID), nil
}

// splitList returns the non-empty, trimmed items of a comma-separated flag value.
func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// clientOptions makes Secret and ConfigMap reads live GETs, because the cache holds them without their data.
func clientOptions() client.Options {
	return client.Options{Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}, &corev1.ConfigMap{}}}}
}

func stripData(obj any) (any, error) {
	switch o := obj.(type) {
	case *corev1.Secret:
		o.Data, o.StringData, o.ManagedFields = nil, nil, nil
	case *corev1.ConfigMap:
		o.Data, o.BinaryData, o.ManagedFields = nil, nil, nil
	}
	return obj, nil
}

// podNamespace returns POD_NAMESPACE, or the namespace of the mounted ServiceAccount.
func podNamespace() (string, error) {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns, nil
	}
	b, err := os.ReadFile(serviceAccountNamespaceFile)
	if err != nil {
		return "", fmt.Errorf("operator namespace: set POD_NAMESPACE: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
