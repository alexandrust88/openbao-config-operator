// Command obc-import reads a live OpenBao server and writes the openbao-config-operator resources that describe
// it, with managementPolicy Observe and creationPolicy Adopt. See docs/guides/import.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/importer"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "obc-import:", err)
		}
		os.Exit(2)
	}
}

// config holds the parsed command line.
type config struct {
	address        string
	caCert         string
	caNamespace    string
	token          string
	namespace      string
	recursive      bool
	connection     string
	connectionKind string
	server         string
	k8sNamespace   string
	includeKV      bool
	output         string
	timeout        time.Duration
}

func newFlagSet(c *config, stderr io.Writer, getenv func(string) string) *flag.FlagSet {
	fs := flag.NewFlagSet("obc-import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.address, "address", getenv("BAO_ADDR"), "OpenBao address (env BAO_ADDR)")
	fs.StringVar(&c.caCert, "ca-cert", getenv("BAO_CACERT"), "PEM file with the CA bundle (env BAO_CACERT)")
	fs.StringVar(&c.caNamespace, "ca-namespace", "",
		"platform namespace of the CA bundle Secret of the -server (required with -server and -ca-cert)")
	fs.StringVar(&c.token, "token", "",
		"token used to read (env BAO_TOKEN; prefer the env, a flag shows in the process list)")
	fs.StringVar(&c.namespace, "namespace", getenv("BAO_NAMESPACE"),
		"OpenBao namespace to import: the namespace the connection points at (env BAO_NAMESPACE)")
	fs.BoolVar(&c.recursive, "recursive", false, "also import every child namespace")
	fs.StringVar(&c.connection, "connection", "", "name of the connection the resources reference (required)")
	fs.StringVar(&c.connectionKind, "connection-kind", string(commonv1alpha1.ConnectionKindNamespaced),
		"BaoConnection or ClusterBaoConnection")
	fs.StringVar(&c.server, "server", "",
		"also emit a BaoServer of this name for -address and the connection that references it")
	fs.StringVar(&c.k8sNamespace, "k8s-namespace", "", "Kubernetes namespace of the resources (required)")
	fs.BoolVar(&c.includeKV, "include-kv-paths", false, "emit a KVSecret skeleton per KV path; values are never read")
	fs.StringVar(&c.output, "o", "-", "a directory for one file per kind, or - for one stream on stdout")
	fs.DurationVar(&c.timeout, "timeout", 30*time.Second, "timeout of each OpenBao request")
	return fs
}

func parse(args []string, stderr io.Writer, getenv func(string) string) (config, error) {
	var c config
	fs := newFlagSet(&c, stderr, getenv)
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if c.token == "" {
		c.token = getenv("BAO_TOKEN")
	}
	switch {
	case fs.NArg() > 0:
		return c, fmt.Errorf("unexpected arguments %v", fs.Args())
	case c.address == "":
		return c, errors.New("set -address or BAO_ADDR")
	case c.token == "":
		return c, errors.New("set BAO_TOKEN or -token")
	case c.connection == "":
		return c, errors.New("-connection is required")
	case c.k8sNamespace == "":
		return c, errors.New("-k8s-namespace is required")
	case c.server != "" && c.caCert != "" && c.caNamespace == "":
		return c, errors.New("-ca-namespace is required with -server and -ca-cert")
	case c.connectionKind != string(commonv1alpha1.ConnectionKindNamespaced) &&
		c.connectionKind != string(commonv1alpha1.ConnectionKindCluster):
		return c, fmt.Errorf("-connection-kind must be %s or %s", commonv1alpha1.ConnectionKindNamespaced,
			commonv1alpha1.ConnectionKindCluster)
	}
	return c, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	c, err := parse(args, stderr, getenv)
	if err != nil {
		return err
	}
	cfg := openbao.Config{
		Address:   c.address,
		Namespace: c.namespace,
		Timeout:   c.timeout,
		Auth: openbao.TokenAuth{
			CredentialKey: "obc-import",
			Token:         func(context.Context) (string, error) { return c.token, nil },
		},
	}
	if c.caCert != "" {
		if cfg.CABundle, err = os.ReadFile(c.caCert); err != nil {
			return fmt.Errorf("read CA bundle: %w", err)
		}
	}
	bao, err := openbao.NewPool().Client(ctx, "obc-import", cfg)
	if err != nil {
		return fmt.Errorf("connect to OpenBao: %w", err)
	}
	conn := commonv1alpha1.ConnectionReference{Kind: commonv1alpha1.ConnectionKind(c.connectionKind), Name: c.connection}
	opts := importer.Options{
		Connection:     conn,
		Namespace:      c.k8sNamespace,
		Recursive:      c.recursive,
		IncludeKVPaths: c.includeKV,
	}
	if c.server != "" {
		opts.Server = &importer.Server{Name: c.server, Address: c.address, Namespace: c.namespace, CABundle: c.caCert != "",
			CANamespace: c.caNamespace}
	}
	result, err := importer.Import(ctx, bao, opts)
	if err != nil {
		return err
	}
	if c.output == "-" {
		err = importer.WriteStream(stdout, result.Objects)
	} else {
		_, err = importer.WriteDir(c.output, result.Objects)
	}
	if err != nil {
		return fmt.Errorf("write resources: %w", err)
	}
	return summarize(stderr, result)
}

// summarize reports on stderr what the resources cannot carry: write-only inputs and objects that need attention.
func summarize(w io.Writer, r *importer.Result) error {
	var b strings.Builder
	fmt.Fprintf(&b, "imported %d resources\n", len(r.Objects))
	for _, obj := range r.Objects {
		if server, ok := obj.(*corev1alpha1.BaoServer); ok && server.Spec.AllowInsecureHTTP {
			fmt.Fprintf(&b, "warning: BaoServer/%s sets allowInsecureHTTP: true for %s; tokens and secret inputs "+
				"would cross the network in clear text. Set an https address before you apply or commit it\n",
				server.Name, server.Spec.Address)
		}
	}
	if len(r.Placeholders) > 0 {
		b.WriteString("write-only inputs OpenBao never returns; create these Secrets before managementPolicy Manage:\n")
		for _, p := range r.Placeholders {
			fmt.Fprintf(&b, "  %s %s: Secret %s", p.Object, p.Field, p.Secret)
			if p.Key != "" {
				b.WriteString(" key " + p.Key)
			}
			b.WriteString("\n")
		}
	}
	if len(r.Notes) > 0 {
		b.WriteString("notes:\n")
		for _, n := range r.Notes {
			b.WriteString("  " + n + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
