/*
Copyright 2026.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
)

// +kubebuilder:validation:XValidation:rule="!has(self.openbaoNamespace) || self.openbaoNamespace == ''",message="RaftAutopilot exists only in the root namespace: leave openbaoNamespace empty"
// +kubebuilder:validation:XValidation:rule="!has(self.cleanupDeadServers) || !self.cleanupDeadServers || (has(self.minQuorum) && self.minQuorum >= 3)",message="cleanupDeadServers requires minQuorum of at least 3"
// +kubebuilder:validation:XValidation:rule="!has(self.lastContactThreshold) || duration(self.lastContactThreshold) >= duration('1s')",message="lastContactThreshold must be at least 1s"
// +kubebuilder:validation:XValidation:rule="!has(self.deadServerLastContactThreshold) || duration(self.deadServerLastContactThreshold) >= duration('1m')",message="deadServerLastContactThreshold must be at least 1m"
// +kubebuilder:validation:XValidation:rule="!has(self.serverStabilizationTime) || duration(self.serverStabilizationTime) >= duration('1s')",message="serverStabilizationTime must be at least 1s"

// RaftAutopilotSpec configures raft autopilot at sys/storage/raft/autopilot/configuration. It applies only to servers that use integrated (raft) storage and only in the root namespace; on other storage the resource reports that the server does not support it. A field that is not set is kept at its OpenBao default. Deleting the resource with deletionPolicy Delete restores the OpenBao defaults.
type RaftAutopilotSpec struct {
	commonv1alpha1.ResourceSpec `json:",inline"`

	// Whether dead servers are removed from the raft peer set periodically and when a new server joins. Maps to `cleanup_dead_servers`. Requires minQuorum of at least 3. OpenBao default: false. Example: true
	// +optional
	CleanupDeadServers *bool `json:"cleanupDeadServers,omitempty"`

	// How long a server can go without contact from the leader before it is considered unhealthy, as a duration of at least 1s. Maps to `last_contact_threshold`. OpenBao default: 10s. Example: 10s
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^([0-9]+(ns|us|ms|s|m|h))+$`
	// +optional
	LastContactThreshold string `json:"lastContactThreshold,omitempty"`

	// How long a server can go without contact from the leader before it is considered failed and removed, as a duration of at least 1m. Takes effect only with cleanupDeadServers. Maps to `dead_server_last_contact_threshold`. OpenBao default: 24h. Example: 24h
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^([0-9]+(ns|us|ms|s|m|h))+$`
	// +optional
	DeadServerLastContactThreshold string `json:"deadServerLastContactThreshold,omitempty"`

	// Number of raft log entries a server can lag behind the leader before it is considered unhealthy. Maps to `max_trailing_logs`. OpenBao default: 1000. Example: 1000
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxTrailingLogs *int64 `json:"maxTrailingLogs,omitempty"`

	// Minimum number of servers the cluster keeps before autopilot prunes dead servers; at least 3 with cleanupDeadServers. Maps to `min_quorum`. OpenBao default: 0 (unset). OpenBao applies a change back to 0 only after a restart, so the operator does not repair a running value it cannot lower. Example: 3
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinQuorum *int64 `json:"minQuorum,omitempty"`

	// How long a new server must be healthy before it is promoted to a voter, as a duration of at least 1s. Maps to `server_stabilization_time`. OpenBao default: 10s. Example: 10s
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^([0-9]+(ns|us|ms|s|m|h))+$`
	// +optional
	ServerStabilizationTime string `json:"serverStabilizationTime,omitempty"`
}

// RaftAutopilotStatus is the observed state of a RaftAutopilot.
type RaftAutopilotStatus struct {
	commonv1alpha1.ResourceStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=obco;obco-sys
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RaftAutopilot manages the raft autopilot settings at sys/storage/raft/autopilot/configuration: dead server cleanup, health thresholds and voter promotion. Only for integrated (raft) storage, root namespace only; a singleton that always exists, so use creationPolicy Adopt or CreateOrAdopt. Only the fields that are set are written. deletionPolicy defaults to Orphan; Delete restores the defaults except minQuorum.
//
// Example:
//
//	spec:
//	  connectionRef:
//	    name: openbao
//	  creationPolicy: CreateOrAdopt
//	  cleanupDeadServers: true
//	  deadServerLastContactThreshold: 24h
//	  minQuorum: 3
type RaftAutopilot struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Desired state of the raft autopilot settings; unset fields keep their OpenBao defaults.
	// +required
	Spec RaftAutopilotSpec `json:"spec"`

	// Observed state reported by the operator: conditions, OpenBao path and ownership.
	// +optional
	Status RaftAutopilotStatus `json:"status,omitzero"`
}

// GetResourceSpec implements commonv1alpha1.Managed.
func (r *RaftAutopilot) GetResourceSpec() *commonv1alpha1.ResourceSpec { return &r.Spec.ResourceSpec }

// GetResourceStatus implements commonv1alpha1.Managed.
func (r *RaftAutopilot) GetResourceStatus() *commonv1alpha1.ResourceStatus {
	return &r.Status.ResourceStatus
}

// +kubebuilder:object:root=true

// RaftAutopilotList contains a list of RaftAutopilot.
type RaftAutopilotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RaftAutopilot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RaftAutopilot{}, &RaftAutopilotList{})
}
