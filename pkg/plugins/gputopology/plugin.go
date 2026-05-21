package gputopology

import (
	"context"
	"fmt"
	"gpu-topology-scheduler/apis/config"
	"time"

	"k8s.io/klog/v2"
	fwk "k8s.io/kube-scheduler/framework"

	v1 "k8s.io/api/core/v1"
	v1Listers "k8s.io/client-go/listers/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
)

type GpuTopologyPlugin struct {
	logger          klog.Logger
	podListner      v1Listers.PodLister
	nodeLisner      v1Listers.NodeLister
	scheduleTimeout *time.Duration
}

var _ fwk.ScorePlugin = &GpuTopologyPlugin{}

const (
	Name = "GpuTopology"
)

type TopologyDistance int

const (
	Unknown          TopologyDistance = iota // Score: 0
	SameNode                                 // Score: 100
	SameNVLinkDomain                         // Score: 80
	SameRack                                 // Score: 50
	DifferentRack                            // Score: 10
)

var distanceToScore = map[TopologyDistance]int64{
	Unknown:          0,
	SameNode:         100,
	SameNVLinkDomain: 80,
	SameRack:         50,
	DifferentRack:    10,
}

const (
	NVLinkDomainNodeLabelKey = "gpu-topology/nvlink-domain"
	RackNodeLabelKey         = "gpu-topology/rack"
	GpuTypeNodeLabelKey      = "gpu-topology/gpu-type"
	GpuMemoryGBNodeLabelKey  = "gpu-topology/gpu-memory-gb"
)

const (
	RolePodLabelKey  = "gpu-topology/role"
	GroupPodLabelKey = "gpu-topology/group"
)

func New(ctx context.Context, obj runtime.Object, handle fwk.Handle) (fwk.Plugin, error) {
	lh := klog.FromContext(ctx).WithValues("plugin", Name)
	lh.V(5).Info("creating new gputopology plugin")

	args, ok := obj.(*config.GpuTopologyArgs)

	if !ok {
		return nil, fmt.Errorf("want args to be of type GpuTopologyArgs, got %T", obj)
	}

	var scheduleTimeDuration time.Duration
	if args.PermitWaitingTimeSeconds == nil {
		scheduleTimeDuration = time.Duration(*args.PermitWaitingTimeSeconds) * time.Second
	} else {
		scheduleTimeDuration = time.Duration(*args.PermitWaitingTimeSeconds) * time.Second
	}

	plugin := &GpuTopologyPlugin{
		logger:          lh,
		podListner:      handle.SharedInformerFactory().Core().V1().Pods().Lister(),
		nodeLisner:      handle.SharedInformerFactory().Core().V1().Nodes().Lister(),
		scheduleTimeout: &scheduleTimeDuration,
	}

	return plugin, nil
}

func (gt *GpuTopologyPlugin) Name() string {
	return Name
}

func (gt *GpuTopologyPlugin) Score(ctx context.Context, state fwk.CycleState, p *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {

	if _, ok := p.Labels[RolePodLabelKey]; !ok {
		return distanceToScore[Unknown], fwk.NewStatus(fwk.Success)
	}

	role := p.Labels[RolePodLabelKey]

	group := p.Labels[GroupPodLabelKey]
	pods := nodeInfo.GetPods()

	if role == "prefil" {

		for _, po := range pods {
			labels := po.GetPod().Labels
			if _, ok := labels[RolePodLabelKey]; !ok {
				continue
			}
			if labels[RolePodLabelKey] == "decode" {
				if labels[GroupPodLabelKey] == group {
					return distanceToScore[SameNode], fwk.NewStatus(fwk.Success)
				}
			}
		}
		score, err := gt.CalculateScoreFromDistance(nodeInfo, p, "decode")

		if err != nil {
			return distanceToScore[Unknown], fwk.NewStatus(fwk.Error, err.Error())
		}

		return score, fwk.NewStatus(fwk.Success)

	} else if role == "decode" {
		for _, po := range pods {
			labels := po.GetPod().Labels

			if _, ok := labels[RolePodLabelKey]; !ok {
				continue
			}

			if labels[RolePodLabelKey] == "prefil" {
				if labels[GroupPodLabelKey] == group {
					return distanceToScore[SameNode], fwk.NewStatus(fwk.Success)
				}
			}

		}

		score, err := gt.CalculateScoreFromDistance(nodeInfo, p, "prefil")

		if err != nil {
			return 0, fwk.NewStatus(fwk.Error, err.Error())
		}

		return score, fwk.NewStatus(fwk.Success)

	}

	return 0, fwk.NewStatus(fwk.Success)
}

func (gt *GpuTopologyPlugin) ScoreExtensions() fwk.ScoreExtensions { return nil }
