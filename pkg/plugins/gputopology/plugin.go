package gputopology

import (
	"context"
	"fmt"
	"gpu-topology-scheduler/apis/config"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	v1Listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	fwk "k8s.io/kube-scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
)

const (
	Name = "GpuTopology"

	RolePrefill = "prefill"
	RoleDecode  = "decode"
)

const (
	NVLinkDomainNodeLabelKey = "gpu-topology/nvlink-domain"
	RackNodeLabelKey         = "gpu-topology/rack"

	RolePodLabelKey  = "gpu-topology/role"
	GroupPodLabelKey = "gpu-topology/group"
)

type TopologyDistance int

const (
	Unknown          TopologyDistance = iota
	SameNode                          // Score: 100
	SameNVLinkDomain                  // Score: 80
	SameRack                          // Score: 50
	DifferentRack                     // Score: 10
)

var defaultDistanceToScore = map[TopologyDistance]int64{
	Unknown:          0,
	SameNode:         100,
	SameNVLinkDomain: 80,
	SameRack:         50,
	DifferentRack:    10,
}

type GpuTopologyPlugin struct {
	logger          klog.Logger
	podLister       v1Listers.PodLister
	nodeLister      v1Listers.NodeLister
	scheduleTimeout *time.Duration
	distanceToScore map[TopologyDistance]int64
}

var _ fwk.ScorePlugin = &GpuTopologyPlugin{}

func New(ctx context.Context, obj runtime.Object, handle fwk.Handle) (fwk.Plugin, error) {
	lh := klog.FromContext(ctx).WithValues("plugin", Name)
	lh.V(5).Info("creating new gputopology plugin")

	args := &config.GpuTopologyArgs{}
	if obj != nil {
		if a, ok := obj.(*config.GpuTopologyArgs); ok {
			args = a
		} else {
			if err := frameworkruntime.DecodeInto(obj, args); err != nil {
				return nil, fmt.Errorf("decoding plugin args: %w", err)
			}
		}
	}

	var scheduleTimeDuration time.Duration
	if args.PermitWaitingTimeSeconds != nil {
		scheduleTimeDuration = time.Duration(*args.PermitWaitingTimeSeconds) * time.Second
	}

	plugin := &GpuTopologyPlugin{
		logger:          lh,
		podLister:       handle.SharedInformerFactory().Core().V1().Pods().Lister(),
		nodeLister:      handle.SharedInformerFactory().Core().V1().Nodes().Lister(),
		scheduleTimeout: &scheduleTimeDuration,
		distanceToScore: buildScoreMap(args),
	}

	return plugin, nil
}

func buildScoreMap(args *config.GpuTopologyArgs) map[TopologyDistance]int64 {
	scores := map[TopologyDistance]int64{
		Unknown:          defaultDistanceToScore[Unknown],
		SameNode:         defaultDistanceToScore[SameNode],
		SameNVLinkDomain: defaultDistanceToScore[SameNVLinkDomain],
		SameRack:         defaultDistanceToScore[SameRack],
		DifferentRack:    defaultDistanceToScore[DifferentRack],
	}
	if args.ScoreSameNode != nil {
		scores[SameNode] = *args.ScoreSameNode
	}
	if args.ScoreSameNVLinkDomain != nil {
		scores[SameNVLinkDomain] = *args.ScoreSameNVLinkDomain
	}
	if args.ScoreSameRack != nil {
		scores[SameRack] = *args.ScoreSameRack
	}
	if args.ScoreDifferentRack != nil {
		scores[DifferentRack] = *args.ScoreDifferentRack
	}
	return scores
}

func (gt *GpuTopologyPlugin) Name() string {
	return Name
}

func (gt *GpuTopologyPlugin) Score(ctx context.Context, state fwk.CycleState, p *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	role := p.Labels[RolePodLabelKey]
	group := p.Labels[GroupPodLabelKey]

	if role == "" || group == "" {
		return gt.distanceToScore[Unknown], fwk.NewStatus(fwk.Success)
	}

	var peerRole string
	switch role {
	case RolePrefill:
		peerRole = RoleDecode
	case RoleDecode:
		peerRole = RolePrefill
	default:
		return gt.distanceToScore[Unknown], fwk.NewStatus(fwk.Success)
	}

	peerPods, err := gt.findPodsByGroupAndRole(group, peerRole)
	if err != nil {
		return gt.distanceToScore[Unknown], fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to find peer pods: %v", err))
	}

	if len(peerPods) == 0 {
		return gt.distanceToScore[SameRack], fwk.NewStatus(fwk.Success)
	}

	candidateNode := nodeInfo.Node()
	bestScore := gt.distanceToScore[DifferentRack]
	for _, peer := range peerPods {
		peerNode, err := gt.nodeLister.Get(peer.Spec.NodeName)
		if err != nil {
			continue
		}
		distance := calculateDistance(candidateNode, peerNode)
		score := gt.distanceToScore[distance]
		if score > bestScore {
			bestScore = score
		}
	}

	return bestScore, fwk.NewStatus(fwk.Success)
}

func (gt *GpuTopologyPlugin) ScoreExtensions() fwk.ScoreExtensions { return nil }

func (gt *GpuTopologyPlugin) findPodsByGroupAndRole(group, role string) ([]*v1.Pod, error) {
	selector := labels.NewSelector()

	reqGroup, err := labels.NewRequirement(GroupPodLabelKey, selection.Equals, []string{group})
	if err != nil {
		return nil, fmt.Errorf("invalid group requirement: %v", err)
	}

	reqRole, err := labels.NewRequirement(RolePodLabelKey, selection.Equals, []string{role})
	if err != nil {
		return nil, fmt.Errorf("invalid role requirement: %v", err)
	}

	selector = selector.Add(*reqGroup, *reqRole)

	pods, err := gt.podLister.List(selector)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	var result []*v1.Pod
	for _, pod := range pods {
		if pod.Spec.NodeName != "" && pod.Status.Phase == v1.PodRunning {
			result = append(result, pod)
		}
	}
	return result, nil
}

func calculateDistance(candidateNode, peerNode *v1.Node) TopologyDistance {
	if candidateNode.Name == peerNode.Name {
		return SameNode
	}
	if v := candidateNode.Labels[NVLinkDomainNodeLabelKey]; v != "" && v == peerNode.Labels[NVLinkDomainNodeLabelKey] {
		return SameNVLinkDomain
	}
	if v := candidateNode.Labels[RackNodeLabelKey]; v != "" && v == peerNode.Labels[RackNodeLabelKey] {
		return SameRack
	}
	return DifferentRack
}
