package gputopology

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	fwk "k8s.io/kube-scheduler/framework"
)

func (gt *GpuTopologyPlugin) CalculateScoreFromDistance(targetNode fwk.NodeInfo, targetPod *v1.Pod, targetLabel string) (int64, error) {

	podLabels := targetPod.Labels

	if _, ok := podLabels[RolePodLabelKey]; !ok {
		return distanceToScore[DifferentRack], nil
	}

	group := podLabels[RolePodLabelKey]
	selector := labels.NewSelector()

	requirement_group, err_group := labels.NewRequirement(GroupPodLabelKey, selection.Equals, []string{group})
	requirement_role, err_role := labels.NewRequirement(RolePodLabelKey, selection.Equals, []string{group})

	if err_group != nil {
		return distanceToScore[Unknown], fmt.Errorf("error happend", err_group)
	} else if err_role != nil {
		return distanceToScore[Unknown], fmt.Errorf("error happend", err_role)
	}

	selector.Add(*requirement_group, *requirement_role)

	pods, err := gt.podListner.List(selector)

	nodeLabels := targetNode.Node().Labels
	nodeNvlink := nodeLabels[NVLinkDomainNodeLabelKey]
	nodeRack := nodeLabels[RackNodeLabelKey]

	if err != nil {
		return distanceToScore[Unknown], fmt.Errorf("error happend", err)
	}

	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		node, err := gt.nodeLisner.Get(nodeName)

		if err != nil {
			return distanceToScore[Unknown], fmt.Errorf("error happend", err)
		}

		var score int64

		if nodeNvlink != "" && node.Labels[NVLinkDomainNodeLabelKey] == nodeNvlink {
			score = max(score, distanceToScore[SameNVLinkDomain])
		}

		if nodeRack != "" && node.Labels[RackNodeLabelKey] == nodeRack {
			score = max(score, distanceToScore[SameRack])
		}

		return score, nil

	}

	return distanceToScore[DifferentRack], nil
}
