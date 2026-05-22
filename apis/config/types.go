package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GpuTopologyArgs struct {
	metav1.TypeMeta `json:",inline"`

	PermitWaitingTimeSeconds *int64 `json:"permitWaitingTimeSeconds,omitempty"`
	ScoreSameNode            *int64 `json:"scoreSameNode,omitempty"`
	ScoreSameNVLinkDomain    *int64 `json:"scoreSameNVLinkDomain,omitempty"`
	ScoreSameRack            *int64 `json:"scoreSameRack,omitempty"`
	ScoreDifferentRack       *int64 `json:"scoreDifferentRack,omitempty"`
}
