package gputopology

import (
	"context"
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	v1Listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	framework "k8s.io/kubernetes/pkg/scheduler/framework"
)

// fakePodLister implements v1Listers.PodLister for testing.
type fakePodLister struct {
	pods []*v1.Pod
}

func (f *fakePodLister) List(selector labels.Selector) ([]*v1.Pod, error) {
	var result []*v1.Pod
	for _, p := range f.pods {
		if selector.Matches(labels.Set(p.Labels)) {
			result = append(result, p)
		}
	}
	return result, nil
}

func (f *fakePodLister) Pods(namespace string) v1Listers.PodNamespaceLister {
	return nil
}

// fakeNodeLister implements v1Listers.NodeLister for testing.
type fakeNodeLister struct {
	indexer cache.Indexer
}

func newFakeNodeLister(nodes []*v1.Node) *fakeNodeLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, n := range nodes {
		_ = indexer.Add(n)
	}
	return &fakeNodeLister{indexer: indexer}
}

func (f *fakeNodeLister) List(selector labels.Selector) ([]*v1.Node, error) {
	var result []*v1.Node
	for _, obj := range f.indexer.List() {
		n := obj.(*v1.Node)
		if selector.Matches(labels.Set(n.Labels)) {
			result = append(result, n)
		}
	}
	return result, nil
}

func (f *fakeNodeLister) Get(name string) (*v1.Node, error) {
	obj, exists, err := f.indexer.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("node %s not found", name)
	}
	return obj.(*v1.Node), nil
}

func makeNode(name string, nodeLabels map[string]string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: nodeLabels,
		},
	}
}

func makeNodeInfo(node *v1.Node) *framework.NodeInfo {
	ni := framework.NewNodeInfo()
	ni.SetNode(node)
	return ni
}

func makePod(name, group, role, nodeName string, phase v1.PodPhase) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				RolePodLabelKey:  role,
				GroupPodLabelKey: group,
			},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
		Status: v1.PodStatus{
			Phase: phase,
		},
	}
}

func newTestPlugin(pods []*v1.Pod, nodes []*v1.Node) *GpuTopologyPlugin {
	return &GpuTopologyPlugin{
		podLister:       &fakePodLister{pods: pods},
		nodeLister:      newFakeNodeLister(nodes),
		distanceToScore: defaultDistanceToScore,
	}
}

func TestCalculateDistance(t *testing.T) {
	tests := []struct {
		name     string
		cNode    *v1.Node
		pNode    *v1.Node
		expected TopologyDistance
	}{
		{
			name:     "same node",
			cNode:    makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
			pNode:    makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
			expected: SameNode,
		},
		{
			name:     "same nvlink domain",
			cNode:    makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
			pNode:    makeNode("node-2", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
			expected: SameNVLinkDomain,
		},
		{
			name:     "same rack",
			cNode:    makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
			pNode:    makeNode("node-3", map[string]string{NVLinkDomainNodeLabelKey: "d2", RackNodeLabelKey: "r1"}),
			expected: SameRack,
		},
		{
			name:     "different rack",
			cNode:    makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
			pNode:    makeNode("node-5", map[string]string{NVLinkDomainNodeLabelKey: "d3", RackNodeLabelKey: "r2"}),
			expected: DifferentRack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateDistance(tt.cNode, tt.pNode)
			if got != tt.expected {
				t.Errorf("calculateDistance() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestScore_NoLabel(t *testing.T) {
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "no-label-pod", Labels: map[string]string{}}}
	node := makeNode("node-1", map[string]string{RackNodeLabelKey: "r1"})
	plugin := newTestPlugin(nil, []*v1.Node{node})

	score, status := plugin.Score(context.Background(), nil, pod, makeNodeInfo(node))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 0 {
		t.Errorf("expected score 0, got %d", score)
	}
}

func TestScore_NoPeer(t *testing.T) {
	pod := makePod("decode-1", "group-1", RoleDecode, "", v1.PodPending)
	node := makeNode("node-1", map[string]string{RackNodeLabelKey: "r1"})
	plugin := newTestPlugin(nil, []*v1.Node{node})

	score, status := plugin.Score(context.Background(), nil, pod, makeNodeInfo(node))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 50 {
		t.Errorf("expected score 50, got %d", score)
	}
}

func TestScore_SameNode(t *testing.T) {
	node := makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"})
	peerPod := makePod("prefill-1", "group-1", RolePrefill, "node-1", v1.PodRunning)
	targetPod := makePod("decode-1", "group-1", RoleDecode, "", v1.PodPending)
	plugin := newTestPlugin([]*v1.Pod{peerPod}, []*v1.Node{node})

	score, status := plugin.Score(context.Background(), nil, targetPod, makeNodeInfo(node))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 100 {
		t.Errorf("expected score 100, got %d", score)
	}
}

func TestScore_SameNVLinkDomain(t *testing.T) {
	nodes := []*v1.Node{
		makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
		makeNode("node-2", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
	}
	peerPod := makePod("prefill-1", "group-1", RolePrefill, "node-1", v1.PodRunning)
	targetPod := makePod("decode-1", "group-1", RoleDecode, "", v1.PodPending)
	plugin := newTestPlugin([]*v1.Pod{peerPod}, nodes)

	score, status := plugin.Score(context.Background(), nil, targetPod, makeNodeInfo(nodes[1]))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 80 {
		t.Errorf("expected score 80, got %d", score)
	}
}

func TestScore_SameRack(t *testing.T) {
	nodes := []*v1.Node{
		makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
		makeNode("node-3", map[string]string{NVLinkDomainNodeLabelKey: "d2", RackNodeLabelKey: "r1"}),
	}
	peerPod := makePod("prefill-1", "group-1", RolePrefill, "node-1", v1.PodRunning)
	targetPod := makePod("decode-1", "group-1", RoleDecode, "", v1.PodPending)
	plugin := newTestPlugin([]*v1.Pod{peerPod}, nodes)

	score, status := plugin.Score(context.Background(), nil, targetPod, makeNodeInfo(nodes[1]))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 50 {
		t.Errorf("expected score 50, got %d", score)
	}
}

func TestScore_DifferentRack(t *testing.T) {
	nodes := []*v1.Node{
		makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
		makeNode("node-5", map[string]string{NVLinkDomainNodeLabelKey: "d3", RackNodeLabelKey: "r2"}),
	}
	peerPod := makePod("prefill-1", "group-1", RolePrefill, "node-1", v1.PodRunning)
	targetPod := makePod("decode-1", "group-1", RoleDecode, "", v1.PodPending)
	plugin := newTestPlugin([]*v1.Pod{peerPod}, nodes)

	score, status := plugin.Score(context.Background(), nil, targetPod, makeNodeInfo(nodes[1]))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 10 {
		t.Errorf("expected score 10, got %d", score)
	}
}

func TestScore_MultiplePeers_BestScore(t *testing.T) {
	nodes := []*v1.Node{
		makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
		makeNode("node-2", map[string]string{NVLinkDomainNodeLabelKey: "d2", RackNodeLabelKey: "r2"}),
		makeNode("node-3", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"}),
	}
	peer1 := makePod("prefill-1", "group-1", RolePrefill, "node-1", v1.PodRunning)
	peer2 := makePod("prefill-2", "group-1", RolePrefill, "node-2", v1.PodRunning)
	targetPod := makePod("decode-1", "group-1", RoleDecode, "", v1.PodPending)
	plugin := newTestPlugin([]*v1.Pod{peer1, peer2}, nodes)

	// node-3 is same nvlink domain as node-1 → best score 80
	score, status := plugin.Score(context.Background(), nil, targetPod, makeNodeInfo(nodes[2]))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 80 {
		t.Errorf("expected score 80, got %d", score)
	}
}

func TestScore_PeerNotRunning_Ignored(t *testing.T) {
	node := makeNode("node-1", map[string]string{NVLinkDomainNodeLabelKey: "d1", RackNodeLabelKey: "r1"})
	peerPod := makePod("prefill-1", "group-1", RolePrefill, "node-1", v1.PodPending)
	targetPod := makePod("decode-1", "group-1", RoleDecode, "", v1.PodPending)
	plugin := newTestPlugin([]*v1.Pod{peerPod}, []*v1.Node{node})

	score, status := plugin.Score(context.Background(), nil, targetPod, makeNodeInfo(node))
	if !status.IsSuccess() {
		t.Fatalf("unexpected error: %v", status.Message())
	}
	if score != 50 {
		t.Errorf("expected score 50 (no running peer), got %d", score)
	}
}
