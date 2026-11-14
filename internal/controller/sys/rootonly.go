package sys

import "github.com/alexandrust88/openbao-config-operator/internal/engine"

// The kinds below answer 400 "operation only available in root namespace" or "operation unavailable in namespaces"
// (raft), or 404 (plugin catalog) in every child namespace of OpenBao 2.7.2; test/nsmatrix holds the evidence.
var (
	_ engine.RootOnly = AuditAdapter{}
	_ engine.RootOnly = AuditRequestHeaderAdapter{}
	_ engine.RootOnly = RateLimitQuotaAdapter{}
	_ engine.RootOnly = QuotaConfigAdapter{}
	_ engine.RootOnly = PluginAdapter{}
	_ engine.RootOnly = CORSConfigAdapter{}
	_ engine.RootOnly = UIHeaderAdapter{}
	_ engine.RootOnly = LoggerAdapter{}
	_ engine.RootOnly = RaftAutopilotAdapter{}
)

// RootOnly implements engine.RootOnly.
func (AuditAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (AuditRequestHeaderAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (RateLimitQuotaAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (QuotaConfigAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (PluginAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (CORSConfigAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (UIHeaderAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (LoggerAdapter) RootOnly() bool { return true }

// RootOnly implements engine.RootOnly.
func (RaftAutopilotAdapter) RootOnly() bool { return true }
