package sys

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=loggers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=loggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.openbaoconfig.io,resources=loggers/finalizers,verbs=update

// SetupLogger registers the Logger controller with the manager.
func SetupLogger(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.Logger](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("logger-controller"),
		conns,
		LoggerAdapter{},
		func() *sysv1alpha1.Logger { return &sysv1alpha1.Logger{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// LoggerAdapter maps a Logger onto sys/loggers/<name>. Deleting it resets the logger to the configured level.
type LoggerAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.Logger]  = LoggerAdapter{}
	_ engine.Observer[*sysv1alpha1.Logger] = LoggerAdapter{}
	_ engine.Singleton                     = LoggerAdapter{}
)

// Path implements engine.Adapter.
func (LoggerAdapter) Path(l *sysv1alpha1.Logger) string {
	return "sys/loggers/" + nameOrDefault(l.Spec.Name, l.Name)
}

// Desired implements engine.Adapter.
func (LoggerAdapter) Desired(_ context.Context, _ engine.Resolver, l *sysv1alpha1.Logger) (engine.Payload, error) {
	return engine.Payload{"level": string(l.Spec.Level)}, nil
}

// Fields implements engine.Adapter.
func (LoggerAdapter) Fields() engine.FieldSpec { return engine.FieldSpec{} }

// Observe implements engine.Observer. The read returns {<name>: <level>}, or no data for an unknown logger.
func (a LoggerAdapter) Observe(ctx context.Context, c openbao.Client, l *sysv1alpha1.Logger) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "Logger"); err != nil {
		return nil, false, err
	}
	data, err := c.Read(ctx, a.Path(l))
	if err != nil {
		return nil, false, err
	}
	level, ok := data[nameOrDefault(l.Spec.Name, l.Name)].(string)
	if !ok {
		return nil, false, nil
	}
	return engine.Payload{"level": level}, true, nil
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (LoggerAdapter) Singleton() bool { return true }
