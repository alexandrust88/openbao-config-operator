//go:build conformance

// Package conformance runs every built-in kind through the v2 CI gates against envtest, a local
// `bao server -dev` and the manager binary built from ./cmd: the no-write test (examples applied, a further
// drift pass writes nothing) and the default test (minimal spec, then each optional field set and removed).
// It writes a coverage matrix to .tmp/night/conformance.md (CONF_OUT).
package conformance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/alexandrust88/openbao-config-operator/test/testenv"
)

// Matrix cell prefixes.
const (
	confPass = "pass"
	confFail = "FAIL"
)

const confSkipNoExample = "no example"

// confEnv is everything the phases share.
type confEnv struct {
	root          string
	cluster       confCluster
	bao           *confBao
	roleID        string
	secretID      string
	managerBin    string
	managerSource string
	managerArgs   []string
	serverKind    bool
	drift         time.Duration
	logDir        string
	exceptions    map[string]string
	naUsed        sync.Map
}

// na returns the matrix cell for a test that could not be measured for r's kind: n/a when exceptions.yaml declares
// the test for the kind, otherwise a failure, so a regression cannot remove its own coverage.
func (e *confEnv) na(r *confResult, test, why string) string {
	key := r.kind.Kind + "/" + test
	if reason, ok := e.exceptions[key]; ok {
		e.naUsed.Store(key, true)
		r.note("%s n/a declared: %s", test, reason)
		return "n/a (" + why + ")"
	}
	r.fail("("+test+")", "%s without a test: %s entry in exceptions.yaml", why, test)
	return confFail + " (" + why + ")"
}

// confResult is one row of the coverage matrix.
type confResult struct {
	mu       sync.Mutex
	kind     *confKind
	skip     string
	example  string
	noWrite  string
	defaults string
	failing  []string
	notes    []string
}

func (r *confResult) note(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, confSprintf(format, args...))
}

func (r *confResult) fail(field, format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failing = append(r.failing, field+": "+confSprintf(format, args...))
}

// confConfig is the run configuration read from the environment.
type confConfig struct {
	root, baoBin, assets, crdDir, logDir, out string
	managerBin, managerSource                 string
	managerArgs                               []string
	drift                                     time.Duration
	filter                                    *regexp.Regexp
	phases                                    string
}

func confLoadConfig(t *testing.T) confConfig {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfg := confConfig{
		root:       root,
		baoBin:     testenv.RequireBao(t),
		assets:     testenv.RequireAssets(t, root),
		crdDir:     confEnvOr("CONF_CRD_DIR", filepath.Join(root, "config", "crd", "bases")),
		logDir:     confEnvOr("CONF_LOG_DIR", t.TempDir()),
		out:        confEnvOr("CONF_OUT", filepath.Join(root, ".tmp", "night", "conformance.md")),
		managerBin: os.Getenv("CONF_MANAGER_BIN"),
		drift:      4 * time.Second,
		filter:     regexp.MustCompile(os.Getenv("CONF_KINDS")),
		phases:     confEnvOr("CONF_PHASES", "nowrite,defaults"),
	}
	if v := os.Getenv("CONF_DRIFT_INTERVAL"); v != "" {
		if cfg.drift, err = time.ParseDuration(v); err != nil || cfg.drift <= 0 {
			t.Fatalf("CONF_DRIFT_INTERVAL=%q: want a positive duration", v)
		}
	}
	cfg.managerSource = cfg.managerBin
	if cfg.managerBin == "" {
		cfg.managerSource = "built from ./cmd"
		cfg.managerBin = filepath.Join(t.TempDir(), "manager")
		build := exec.Command("go", "build", "-o", cfg.managerBin, "./cmd/")
		build.Dir = root
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build manager: %v\n%s", err, output)
		}
	}
	// The write budget would throttle the fast drift interval and the set/remove steps of the default test.
	help, _ := exec.Command(cfg.managerBin, "--help").CombinedOutput()
	if strings.Contains(string(help), "write-budget-per-minute") {
		cfg.managerArgs = append(cfg.managerArgs, "--write-budget-per-minute=-1")
	}
	return cfg
}

func confEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestConformance(t *testing.T) {
	ctrllog.SetLogger(logr.Discard())
	cfg := confLoadConfig(t)
	catalog := confLoadCatalog(t, cfg.root, cfg.crdDir)
	var results []*confResult
	for _, k := range catalog.kinds {
		if !cfg.filter.MatchString(k.Kind) {
			continue
		}
		r := &confResult{kind: k}
		switch {
		case k.plugin():
			r.skip = "plugin not available"
		case len(k.own(true)) == 0:
			r.skip = confSkipNoExample
		}
		results = append(results, r)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bao := confStartBao(t, cfg.baoBin, cfg.logDir)
	roleID, secretID, err := bao.prepareOperator()
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(cfg.crdDir, "core.openbaoconfig.io_openbaoservers.yaml"))
	e := &confEnv{
		root: cfg.root, cluster: confStartCluster(t, cfg.assets, cfg.crdDir, cfg.logDir), bao: bao,
		roleID: roleID, secretID: secretID, managerBin: cfg.managerBin, managerSource: cfg.managerSource,
		managerArgs: cfg.managerArgs, serverKind: err == nil, drift: cfg.drift, logDir: cfg.logDir,
		exceptions: confLoadExceptions(t, cfg.root),
	}
	if e.serverKind {
		if err := confCreateServer(ctx, e.cluster.k8s, bao.addr); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("logs in %s, drift interval %s", cfg.logDir, cfg.drift)

	if strings.Contains(cfg.phases, "nowrite") {
		t.Run("nowrite", func(t *testing.T) { confNoWrite(ctx, t, e, catalog, results) })
	}
	if strings.Contains(cfg.phases, "defaults") {
		t.Run("defaults", func(t *testing.T) {
			for _, r := range results {
				if r.skip != "" {
					continue
				}
				t.Run(r.kind.Kind, func(t *testing.T) {
					t.Parallel()
					confDefaults(ctx, t, e, catalog, r)
				})
			}
		})
	}

	measured := []string{confTestExamples}
	if strings.Contains(cfg.phases, "nowrite") {
		measured = append(measured, confTestNoWrite)
	}
	if strings.Contains(cfg.phases, "defaults") {
		measured = append(measured, confTestDefaults, confTestOptionalFields)
	}
	for _, r := range results {
		if r.skip == confSkipNoExample {
			r.example, r.noWrite, r.defaults = "-", e.na(r, confTestExamples, confSkipNoExample), "-"
			r.skip = ""
			continue
		}
		if r.skip != "" || strings.HasPrefix(r.defaults, "n/a") {
			continue
		}
		for _, test := range measured {
			key := r.kind.Kind + "/" + test
			if _, used := e.naUsed.Load(key); !used && e.exceptions[key] != "" {
				r.note("exceptions.yaml declares %s n/a but it was measured in this run", test)
			}
		}
	}
	confWriteReport(t, cfg.out, e, results)
	for _, r := range results {
		if r.skip == "" && (strings.HasPrefix(r.noWrite, confFail) || strings.HasPrefix(r.defaults, confFail)) {
			t.Errorf("%s: no-write %s, default %s; failing fields: %s", r.kind.Kind, r.noWrite, r.defaults,
				strings.Join(r.failing, "; "))
		}
	}
}
