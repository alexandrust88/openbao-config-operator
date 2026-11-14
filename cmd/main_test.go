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
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

func TestCacheOptionsKeepsOperatorNamespace(t *testing.T) {
	if got := cacheOptions("", "operator").DefaultNamespaces; got != nil {
		t.Fatalf("all namespaces: DefaultNamespaces = %v, want nil", got)
	}
	got := cacheOptions(" team-a, ,team-b", "operator").DefaultNamespaces
	for _, ns := range []string{"operator", "team-a", "team-b"} {
		if _, ok := got[ns]; !ok {
			t.Errorf("DefaultNamespaces = %v, missing %q", got, ns)
		}
	}
	if len(got) != 3 {
		t.Errorf("DefaultNamespaces = %v, want 3 entries", got)
	}
}

func TestCacheHoldsNoValuesAndSeesRotation(t *testing.T) {
	env := &envtest.Environment{BinaryAssetsDirectory: testenv.RequireAssets(t, "..")}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Cache:                  cacheOptions("", "default"),
		Client:                 clientOptions(),
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	events := make(chan string, 16)
	for name, obj := range map[string]client.Object{"secret-events": &corev1.Secret{}, "configmap-events": &corev1.ConfigMap{}} {
		err = ctrl.NewControllerManagedBy(mgr).Named(name).For(obj, builder.OnlyMetadata).
			Complete(reconcile.Func(func(_ context.Context, req reconcile.Request) (reconcile.Result, error) {
				events <- req.Name
				return reconcile.Result{}, nil
			}))
		if err != nil {
			t.Fatalf("controller: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = mgr.Start(ctx) }()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache did not sync")
	}

	c := mgr.GetClient()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("fake-v1")},
	}
	if err := c.Create(ctx, secret); err != nil {
		t.Fatalf("create: %v", err)
	}
	waitForEvent(t, events, "creds")

	secret.Data["password"] = []byte("fake-v2")
	if err := c.Update(ctx, secret); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	waitForEvent(t, events, "creds")

	var live corev1.Secret
	if err := c.Get(ctx, client.ObjectKeyFromObject(secret), &live); err != nil {
		t.Fatalf("client get: %v", err)
	}
	if string(live.Data["password"]) != "fake-v2" {
		t.Errorf("client get returned %q, want the live value", live.Data["password"])
	}
	var cached corev1.Secret
	if err := mgr.GetCache().Get(ctx, client.ObjectKeyFromObject(secret), &cached); err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if cached.Data != nil || cached.StringData != nil {
		t.Errorf("cached Secret holds data: %v", cached.Data)
	}

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "default"},
		Data: map[string]string{"ca.crt": "ca-v1"}}
	if err := c.Create(ctx, cm); err != nil {
		t.Fatalf("create: %v", err)
	}
	waitForEvent(t, events, "ca")
	cm.Data["ca.crt"] = "ca-v2"
	if err := c.Update(ctx, cm); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	waitForEvent(t, events, "ca")
	var liveCM corev1.ConfigMap
	if err := c.Get(ctx, client.ObjectKeyFromObject(cm), &liveCM); err != nil {
		t.Fatalf("client get: %v", err)
	}
	if liveCM.Data["ca.crt"] != "ca-v2" {
		t.Errorf("client get returned %q, want the live value", liveCM.Data["ca.crt"])
	}
	var cachedCM corev1.ConfigMap
	if err := mgr.GetCache().Get(ctx, client.ObjectKeyFromObject(cm), &cachedCM); err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if cachedCM.Data != nil || cachedCM.BinaryData != nil {
		t.Errorf("cached ConfigMap holds data: %v", cachedCM.Data)
	}
}

func waitForEvent(t *testing.T, events <-chan string, name string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case got := <-events:
			if got == name {
				return
			}
		case <-timeout:
			t.Fatalf("no reconcile for %q", name)
		}
	}
}

func TestPluginKindsMatchCatalogueAndChart(t *testing.T) {
	doc, err := os.ReadFile("../docs/kinds.md")
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile("(?m)^\\| `([A-Za-z0-9]+)` \\|[^|]*\\| external-plugin").FindAllSubmatch(doc, -1)
	catalogue := make([]string, 0, len(matches))
	for _, m := range matches {
		catalogue = append(catalogue, string(m[1]))
	}
	crds, err := os.ReadFile("../hack/chart/plugin-crds.txt")
	if err != nil {
		t.Fatal(err)
	}
	chart := strings.Fields(string(crds))

	kinds := make([]string, 0, len(pluginKindControllers))
	files := make([]string, 0, len(pluginKindControllers))
	for _, c := range pluginKindControllers {
		group, kind, _ := strings.Cut(c.name, "/")
		kinds = append(kinds, kind)
		files = append(files, group+".openbaoconfig.io_"+strings.ToLower(kind)+"s.yaml")
	}
	slices.Sort(catalogue)
	slices.Sort(kinds)
	slices.Sort(files)
	if !slices.Equal(kinds, catalogue) {
		t.Errorf("pluginKindControllers = %v, docs/kinds.md external-plugin kinds = %v", kinds, catalogue)
	}
	if !slices.Equal(files, chart) {
		t.Errorf("plugin CRD files = %v, hack/chart/plugin-crds.txt = %v", files, chart)
	}
	for _, c := range kindControllers {
		if slices.Contains(kinds, strings.SplitN(c.name, "/", 2)[1]) {
			t.Errorf("%s is both built in and a plugin kind", c.name)
		}
	}
}

func TestRequireCRDs(t *testing.T) {
	gv := schema.GroupVersion{Group: "auth.openbaoconfig.io", Version: "v1alpha1"}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	mapper.Add(gv.WithKind("LDAPAuthConfig"), meta.RESTScopeNamespace)
	present := []kindController{{name: "auth/LDAPAuthConfig"}}
	if err := requireCRDs(mapper, present); err != nil {
		t.Fatalf("installed CRD: %v", err)
	}
	err := requireCRDs(mapper, []kindController{{name: "auth/LDAPAuthConfig"}, {name: "auth/LDAPAuthGroup"}})
	if err == nil || !strings.Contains(err.Error(), "auth/LDAPAuthGroup") {
		t.Fatalf("missing CRD: err = %v, want one naming auth/LDAPAuthGroup", err)
	}
}

func TestKubeSystemUID(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "c1d2"}}
	got, err := kubeSystemUID(context.Background(), fake.NewClientBuilder().WithObjects(ns).Build())
	if err != nil || got != "c1d2" {
		t.Fatalf("kubeSystemUID = %q, %v; want c1d2", got, err)
	}
	if _, err := kubeSystemUID(context.Background(), fake.NewClientBuilder().Build()); err == nil {
		t.Fatal("missing kube-system: want an error")
	}
}
