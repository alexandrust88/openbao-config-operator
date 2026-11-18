package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/importer"
)

const (
	litBaoAddr    = "BAO_ADDR"
	litConnection = "-connection"
	litBaoToken   = "BAO_TOKEN"
	litK8sNS      = "-k8s-namespace"
)

func TestParse(t *testing.T) {
	env := map[string]string{litBaoAddr: "https://bao.example", litBaoToken: "t", "BAO_NAMESPACE": "tenants"}
	getenv := func(key string) string { return env[key] }
	required := []string{litConnection, "openbao", litK8sNS, "obco"}

	c, err := parse(required, &bytes.Buffer{}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if c.address != "https://bao.example" || c.token != "t" || c.namespace != "tenants" || c.output != "-" ||
		c.connectionKind != "BaoConnection" {
		t.Errorf("defaults from env = %+v", c)
	}

	c, err = parse(append(required, "-address", "https://other", "-recursive", "-o", "dir", "-server", "openbao",
		"-connection-kind", "ClusterBaoConnection"), &bytes.Buffer{}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if c.address != "https://other" || !c.recursive || c.output != "dir" || c.server != "openbao" ||
		c.connectionKind != "ClusterBaoConnection" {
		t.Errorf("flags = %+v", c)
	}

	for _, tt := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{required, map[string]string{litBaoToken: "t"}, litBaoAddr},
		{required, map[string]string{litBaoAddr: "a"}, litBaoToken},
		{[]string{litK8sNS, "obco"}, env, litConnection},
		{[]string{litConnection, "openbao"}, env, litK8sNS},
		{append(required, "-connection-kind", "Other"), env, "-connection-kind"},
		{append(required, "extra"), env, "unexpected"},
		{append(required, "-server", "openbao", "-ca-cert", "ca.pem"), env, "-ca-namespace"},
	} {
		_, err := parse(tt.args, &bytes.Buffer{}, func(key string) string { return tt.env[key] })
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("parse(%v) error = %v, want it to mention %s", tt.args, err, tt.want)
		}
	}
}

func TestSummarize(t *testing.T) {
	var buf bytes.Buffer
	err := summarize(&buf, &importer.Result{
		Placeholders: []importer.Placeholder{
			{Object: "UserpassUser/alice", Field: "spec.passwordSecretRef", Secret: "alice", Key: "password"},
			{Object: "KVSecret/top", Field: "spec.dataFrom[0].secretRef", Secret: "top"},
		},
		Notes: []string{"ACLPolicy/x: Observe will report drift on policy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `imported 0 resources
write-only inputs OpenBao never returns; create these Secrets before managementPolicy Manage:
  UserpassUser/alice spec.passwordSecretRef: Secret alice key password
  KVSecret/top spec.dataFrom[0].secretRef: Secret top
notes:
  ACLPolicy/x: Observe will report drift on policy
`
	if buf.String() != want {
		t.Errorf("summary:\n%s\nwant:\n%s", buf.String(), want)
	}

	buf.Reset()
	insecure := &corev1alpha1.BaoServer{ObjectMeta: metav1.ObjectMeta{Name: "openbao"},
		Spec: corev1alpha1.BaoServerSpec{Address: "http://127.0.0.1:8200", AllowInsecureHTTP: true}}
	if err := summarize(&buf, &importer.Result{Objects: []client.Object{insecure}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "warning: BaoServer/openbao sets allowInsecureHTTP: true for http://127.0.0.1:8200") {
		t.Errorf("summary without the insecure-address warning:\n%s", buf.String())
	}
}

func TestTokenNeverInOutput(t *testing.T) {
	const sentinel = "sentinel-not-a-real-token"
	getenv := func(key string) string {
		if key == litBaoToken {
			return sentinel
		}
		return ""
	}
	for _, args := range [][]string{{"-h"}, {"-no-such-flag"}, {"-timeout", "soon"}} {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), args, &stdout, &stderr, getenv)
		if err == nil {
			t.Fatalf("run(%v) succeeded", args)
		}
		if out := stdout.String() + stderr.String() + err.Error(); strings.Contains(out, sentinel) {
			t.Errorf("run(%v) printed the BAO_TOKEN value:\n%s", args, out)
		}
	}
}

func TestNoFlagDefaultFromCredentialEnv(t *testing.T) {
	var c config
	fs := newFlagSet(&c, &bytes.Buffer{}, func(key string) string { return "env:" + key })
	fs.VisitAll(func(f *flag.Flag) {
		key, ok := strings.CutPrefix(f.DefValue, "env:")
		if !ok {
			return
		}
		for _, word := range []string{"TOKEN", "SECRET", "PASSWORD", "KEY", "CRED"} {
			if strings.Contains(key, word) {
				t.Errorf("-%s defaults to credential env %s; flag defaults are printed by -h", f.Name, key)
			}
		}
	})
}
