package sys

import (
	"context"
	"encoding/json"
	"testing"

	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

func TestRaftAutopilotObserve(t *testing.T) {
	a := RaftAutopilotAdapter{}
	ctx := context.Background()
	r := &sysv1alpha1.RaftAutopilot{Spec: sysv1alpha1.RaftAutopilotSpec{
		CleanupDeadServers:             new(true),
		LastContactThreshold:           lit10s,
		DeadServerLastContactThreshold: "1h",
		MaxTrailingLogs:                new(int64(500)),
		MinQuorum:                      new(int64(3)),
		ServerStabilizationTime:        "30s",
	}}
	desired, _ := a.Desired(ctx, nil, r)
	c := newConfigClient(map[string]map[string]any{raftAutopilotPath: {
		"cleanup_dead_servers":               true,
		"last_contact_threshold":             lit10s,
		"dead_server_last_contact_threshold": "1h0m0s",
		"max_trailing_logs":                  json.Number("500"),
		"min_quorum":                         json.Number("3"),
		"server_stabilization_time":          "30s",
	}})
	observed, found, err := a.Observe(ctx, c, r)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	assertInSync(t, desired, observed, a.Fields())

	c.data[raftAutopilotPath]["max_trailing_logs"] = json.Number("1000")
	observed, _, _ = a.Observe(ctx, c, r)
	assertDiffers(t, desired, observed, a.Fields(), "max_trailing_logs")

	partial, _ := a.Desired(ctx, nil, &sysv1alpha1.RaftAutopilot{Spec: sysv1alpha1.RaftAutopilotSpec{MinQuorum: new(int64(5))}})
	if len(partial) != 6 || partial[raftMinQuorum] != int64(5) || partial["max_trailing_logs"] != int64(1000) {
		t.Errorf("partial desired = %v, want every field with the defaults", partial)
	}
}

// goldenRaftFresh is GET sys/storage/raft/autopilot/configuration of a fresh one-node raft server (OpenBao 2.7.2).
const goldenRaftFresh = `{"cleanup_dead_servers":false,"dead_server_last_contact_threshold":"24h0m0s","last_contact_threshold":"10s",` +
	`"max_trailing_logs":1000,"min_quorum":0,"server_stabilization_time":"10s"}`

func TestRaftAutopilotDefaults(t *testing.T) {
	a := RaftAutopilotAdapter{}
	ctx := context.Background()
	empty := &sysv1alpha1.RaftAutopilot{}
	desired, _ := a.Desired(ctx, nil, empty)
	c := newConfigClient(map[string]map[string]any{raftAutopilotPath: decodeRead(t, goldenRaftFresh)})
	observed, _, err := a.Observe(ctx, c, empty)
	if err != nil {
		t.Fatal(err)
	}
	assertInSync(t, desired, observed, a.Fields())

	c.data[raftAutopilotPath]["max_trailing_logs"] = json.Number("500")
	observed, _, _ = a.Observe(ctx, c, empty)
	assertDiffers(t, desired, observed, a.Fields(), "max_trailing_logs")

	// A running min_quorum stays until a restart after 0 is written; repairing it would rewrite forever.
	c.data[raftAutopilotPath]["max_trailing_logs"] = json.Number("1000")
	c.data[raftAutopilotPath][raftMinQuorum] = json.Number("3")
	observed, _, _ = a.Observe(ctx, c, &sysv1alpha1.RaftAutopilot{Spec: sysv1alpha1.RaftAutopilotSpec{MinQuorum: new(int64(0))}})
	assertInSync(t, desired, observed, a.Fields())
	observed, _, _ = a.Observe(ctx, c, &sysv1alpha1.RaftAutopilot{Spec: sysv1alpha1.RaftAutopilotSpec{MinQuorum: new(int64(5))}})
	five, _ := a.Desired(ctx, nil, &sysv1alpha1.RaftAutopilot{Spec: sysv1alpha1.RaftAutopilotSpec{MinQuorum: new(int64(5))}})
	assertDiffers(t, five, observed, a.Fields(), raftMinQuorum)
}

func TestRaftAutopilotWithoutRaft(t *testing.T) {
	a := RaftAutopilotAdapter{}
	ctx := context.Background()
	r := &sysv1alpha1.RaftAutopilot{}

	_, _, err := a.Observe(ctx, newConfigClient(nil), r)
	assertUnsupported(t, err)

	c := newConfigClient(nil)
	c.errs["read "+raftAutopilotPath] = badRequest(raftNotInUse)
	_, _, err = a.Observe(ctx, c, r)
	assertUnsupported(t, err)

	c.errs["write "+raftAutopilotPath] = &openbao.ResponseError{StatusCode: 404}
	if err := a.Delete(ctx, c, r); err != nil {
		t.Errorf("delete without raft: %v", err)
	}
	_, _, err = a.Observe(ctx, newConfigClient(nil).WithNamespace(testNamespace), r)
	assertUnsupported(t, err)
}

func TestRaftAutopilotDeleteResets(t *testing.T) {
	c := newConfigClient(nil)
	if err := (RaftAutopilotAdapter{}).Delete(context.Background(), c, &sysv1alpha1.RaftAutopilot{}); err != nil {
		t.Fatal(err)
	}
	written := c.writes[raftAutopilotPath]
	if written["cleanup_dead_servers"] != false || written["last_contact_threshold"] != lit10s {
		t.Errorf("reset payload = %v", written)
	}
	if written[raftMinQuorum] != int64(0) {
		t.Errorf("min_quorum = %v, want 0 (unset)", written[raftMinQuorum])
	}
}
