package sys

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	ctrl "sigs.k8s.io/controller-runtime"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// +kubebuilder:rbac:groups=sys.obco.dev,resources=raftautopilots,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=raftautopilots/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sys.obco.dev,resources=raftautopilots/finalizers,verbs=update

const (
	raftAutopilotPath = "sys/storage/raft/autopilot/configuration"
	raftNotInUse      = "raft storage is not in use"
	raftMinQuorum     = "min_quorum"
)

// SetupRaftAutopilot registers the RaftAutopilot controller with the manager.
func SetupRaftAutopilot(mgr ctrl.Manager, conns engine.Connections, opts engine.Options) error {
	r := engine.New[*sysv1alpha1.RaftAutopilot](
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetEventRecorder("raftautopilot-controller"),
		conns,
		RaftAutopilotAdapter{},
		func() *sysv1alpha1.RaftAutopilot { return &sysv1alpha1.RaftAutopilot{} },
		opts,
	)
	return r.SetupWithManager(mgr)
}

// RaftAutopilotAdapter maps a RaftAutopilot onto sys/storage/raft/autopilot/configuration. The path has no
// DELETE; deleting resets it.
type RaftAutopilotAdapter struct{}

var (
	_ engine.Adapter[*sysv1alpha1.RaftAutopilot]  = RaftAutopilotAdapter{}
	_ engine.Observer[*sysv1alpha1.RaftAutopilot] = RaftAutopilotAdapter{}
	_ engine.Deleter[*sysv1alpha1.RaftAutopilot]  = RaftAutopilotAdapter{}
	_ engine.Singleton                            = RaftAutopilotAdapter{}
)

// Path implements engine.Adapter.
func (RaftAutopilotAdapter) Path(*sysv1alpha1.RaftAutopilot) string { return raftAutopilotPath }

// Desired implements engine.Adapter. Every field is sent, an omitted one at its OpenBao default
// (raft_autopilot.go DefaultAutopilotConfig), so an emptied field returns to the default.
func (RaftAutopilotAdapter) Desired(_ context.Context, _ engine.Resolver, r *sysv1alpha1.RaftAutopilot) (engine.Payload, error) {
	return raftAutopilotPayload(r.Spec), nil
}

func raftAutopilotPayload(spec sysv1alpha1.RaftAutopilotSpec) engine.Payload {
	p := engine.Payload{
		"cleanup_dead_servers":               false,
		"last_contact_threshold":             "10s",
		"dead_server_last_contact_threshold": "24h",
		"max_trailing_logs":                  int64(1000),
		raftMinQuorum:                        int64(0),
		"server_stabilization_time":          "10s",
	}
	if spec.CleanupDeadServers != nil {
		p["cleanup_dead_servers"] = *spec.CleanupDeadServers
	}
	if spec.LastContactThreshold != "" {
		p["last_contact_threshold"] = spec.LastContactThreshold
	}
	if spec.DeadServerLastContactThreshold != "" {
		p["dead_server_last_contact_threshold"] = spec.DeadServerLastContactThreshold
	}
	if spec.MaxTrailingLogs != nil {
		p["max_trailing_logs"] = *spec.MaxTrailingLogs
	}
	if spec.MinQuorum != nil {
		p[raftMinQuorum] = *spec.MinQuorum
	}
	if spec.ServerStabilizationTime != "" {
		p["server_stabilization_time"] = spec.ServerStabilizationTime
	}
	return p
}

// Fields implements engine.Adapter.
func (RaftAutopilotAdapter) Fields() engine.FieldSpec {
	return engine.FieldSpec{Durations: []string{
		"last_contact_threshold", "dead_server_last_contact_threshold", "server_stabilization_time",
	}}
}

// Observe implements engine.Observer. The path is registered only on raft storage, so a 404 or the "not in
// use" 400 means the server cannot hold this object.
func (RaftAutopilotAdapter) Observe(ctx context.Context, c openbao.Client, r *sysv1alpha1.RaftAutopilot) (engine.Payload, bool, error) {
	if err := requireRootNamespace(c, "RaftAutopilot"); err != nil {
		return nil, false, err
	}
	observed, err := c.Read(ctx, raftAutopilotPath)
	if errors.Is(err, openbao.ErrNotFound) || badRequestContains(err, raftNotInUse) {
		return nil, false, fmt.Errorf("RaftAutopilot needs a server with raft storage: %w", engine.ErrUnsupported)
	}
	if err != nil {
		return nil, false, err
	}
	// A min_quorum of 0 is persisted but not applied to the running configuration (AutopilotConfig.Merge skips
	// zero values) until the server restarts, so rewriting it could never converge.
	if r.Spec.MinQuorum == nil || *r.Spec.MinQuorum == 0 {
		observed[raftMinQuorum] = int64(0)
	}
	return observed, true, nil
}

// Delete implements engine.Deleter by writing the OpenBao defaults; min_quorum 0 takes effect at the next
// restart of the server.
func (RaftAutopilotAdapter) Delete(ctx context.Context, c openbao.Client, _ *sysv1alpha1.RaftAutopilot) error {
	_, err := c.Write(ctx, raftAutopilotPath, raftAutopilotPayload(sysv1alpha1.RaftAutopilotSpec{}))
	if statusIs(err, http.StatusNotFound) || badRequestContains(err, raftNotInUse) {
		return nil
	}
	return err
}

// Singleton implements engine.Singleton: the setting always exists, so it is adopted whatever the creationPolicy.
func (RaftAutopilotAdapter) Singleton() bool { return true }
