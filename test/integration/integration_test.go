package integration

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	configv1 "k8s.io/kube-scheduler/config/v1"
	"k8s.io/kubernetes/pkg/scheduler"
	configtesting "k8s.io/kubernetes/pkg/scheduler/apis/config/testing"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	st "k8s.io/kubernetes/pkg/scheduler/testing"
	testutils "k8s.io/kubernetes/test/integration/util"
	"k8s.io/utils/ptr"

	"gpu-topology-scheduler/pkg/plugins/gputopology"
)

const schedulerName = "gpu-topology-scheduler"

func initTestScheduler(t *testing.T) *testutils.TestContext {
	cfg := configtesting.V1ToInternalWithDefaults(t, configv1.KubeSchedulerConfiguration{
		Profiles: []configv1.KubeSchedulerProfile{{
			SchedulerName: ptr.To(schedulerName),
			Plugins: &configv1.Plugins{
				Score: configv1.PluginSet{
					Enabled: []configv1.Plugin{
						{Name: gputopology.Name, Weight: ptr.To[int32](10)},
					},
				},
			},
		}},
	})

	outOfTreeRegistry := frameworkruntime.Registry{
		gputopology.Name: gputopology.New,
	}

	testCtx := testutils.InitTestSchedulerWithOptions(
		t,
		testutils.InitTestAPIServer(t, "gpu-topology", nil),
		0,
		scheduler.WithProfiles(cfg.Profiles...),
		scheduler.WithFrameworkOutOfTreeRegistry(outOfTreeRegistry),
	)
	testutils.SyncSchedulerInformerFactory(testCtx)
	go testCtx.Scheduler.Run(testCtx.SchedulerCtx)
	return testCtx
}

func createNode(t *testing.T, testCtx *testutils.TestContext, name, nvlinkDomain, rack string) {
	t.Helper()
	node := st.MakeNode().Name(name).
		Label("gpu-topology/nvlink-domain", nvlinkDomain).
		Label("gpu-topology/rack", rack).Obj()
	node.Status.Conditions = []v1.NodeCondition{
		{Type: v1.NodeReady, Status: v1.ConditionTrue},
	}
	if _, err := testCtx.ClientSet.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create node %s: %v", name, err)
	}
}

func createPod(t *testing.T, testCtx *testutils.TestContext, name, group, role string) *v1.Pod {
	t.Helper()
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testCtx.NS.Name,
			Labels: map[string]string{
				"gpu-topology/role":  role,
				"gpu-topology/group": group,
			},
		},
		Spec: v1.PodSpec{
			SchedulerName: schedulerName,
			Containers: []v1.Container{{
				Name:  "pause",
				Image: "registry.k8s.io/pause:3.9",
			}},
		},
	}
	created, err := testCtx.ClientSet.CoreV1().Pods(testCtx.NS.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create pod %s: %v", name, err)
	}
	return created
}

func waitForPodScheduled(t *testing.T, testCtx *testutils.TestContext, pod *v1.Pod) *v1.Pod {
	t.Helper()
	var scheduled *v1.Pod
	err := wait.PollUntilContextTimeout(testCtx.Ctx, 100*time.Millisecond, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		p, err := testCtx.ClientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if p.Spec.NodeName != "" {
			scheduled = p
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("pod %s was not scheduled: %v", pod.Name, err)
	}
	return scheduled
}

func markPodRunning(t *testing.T, testCtx *testutils.TestContext, pod *v1.Pod) {
	t.Helper()
	pod.Status.Phase = v1.PodRunning
	if _, err := testCtx.ClientSet.CoreV1().Pods(pod.Namespace).UpdateStatus(context.TODO(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to mark pod %s as running: %v", pod.Name, err)
	}
}

func TestSchedule_NoPeer_AnyNode(t *testing.T) {
	testCtx := initTestScheduler(t)

	createNode(t, testCtx, "node-1", "d1", "r1")
	createNode(t, testCtx, "node-2", "d2", "r2")
	waitForNodes(t, testCtx, 2)

	pod := createPod(t, testCtx, "decode-1", "group-1", "decode")
	scheduled := waitForPodScheduled(t, testCtx, pod)

	if scheduled.Spec.NodeName == "" {
		t.Fatal("pod was not assigned to any node")
	}
}

func TestSchedule_PeerOnSameNode(t *testing.T) {
	testCtx := initTestScheduler(t)

	createNode(t, testCtx, "node-1", "d1", "r1")
	createNode(t, testCtx, "node-2", "d1", "r1")
	createNode(t, testCtx, "node-3", "d2", "r2")
	waitForNodes(t, testCtx, 3)

	// Schedule prefill first
	prefill := createPod(t, testCtx, "prefill-1", "group-1", "prefill")
	prefill = waitForPodScheduled(t, testCtx, prefill)
	markPodRunning(t, testCtx, prefill)

	// Wait for informer to sync the running status
	time.Sleep(500 * time.Millisecond)

	// Schedule decode - should prefer same node as prefill (score 100)
	decode := createPod(t, testCtx, "decode-1", "group-1", "decode")
	decode = waitForPodScheduled(t, testCtx, decode)

	if decode.Spec.NodeName != prefill.Spec.NodeName {
		t.Errorf("expected decode on same node as prefill (%s), got %s", prefill.Spec.NodeName, decode.Spec.NodeName)
	}
}

func TestSchedule_PeerOnSameNVLinkDomain(t *testing.T) {
	testCtx := initTestScheduler(t)

	// node-1: d1/r1, node-2: d1/r1, node-3: d2/r2
	createNode(t, testCtx, "node-1", "d1", "r1")
	createNode(t, testCtx, "node-2", "d1", "r1")
	createNode(t, testCtx, "node-3", "d2", "r2")
	waitForNodes(t, testCtx, 3)

	// Create prefill on node-1 specifically
	prefill := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prefill-1",
			Namespace: testCtx.NS.Name,
			Labels: map[string]string{
				"gpu-topology/role":  "prefill",
				"gpu-topology/group": "group-1",
			},
		},
		Spec: v1.PodSpec{
			SchedulerName: schedulerName,
			NodeName:      "node-1",
			Containers:    []v1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.9"}},
		},
	}
	prefill, err := testCtx.ClientSet.CoreV1().Pods(testCtx.NS.Name).Create(context.TODO(), prefill, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create prefill pod: %v", err)
	}
	prefill.Status.Phase = v1.PodRunning
	prefill, err = testCtx.ClientSet.CoreV1().Pods(testCtx.NS.Name).UpdateStatus(context.TODO(), prefill, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update prefill status: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Schedule decode - should prefer node-1 (same node=100) or node-2 (same nvlink=80), not node-3 (diff rack=10)
	decode := createPod(t, testCtx, "decode-1", "group-1", "decode")
	decode = waitForPodScheduled(t, testCtx, decode)

	if decode.Spec.NodeName == "node-3" {
		t.Errorf("decode should not be on node-3 (different rack), got %s", decode.Spec.NodeName)
	}
}

func TestSchedule_NoTopologyLabels_Scheduled(t *testing.T) {
	testCtx := initTestScheduler(t)

	createNode(t, testCtx, "node-1", "d1", "r1")
	waitForNodes(t, testCtx, 1)

	// Pod without gpu-topology labels should still be scheduled
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "regular-pod",
			Namespace: testCtx.NS.Name,
		},
		Spec: v1.PodSpec{
			SchedulerName: schedulerName,
			Containers:    []v1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.9"}},
		},
	}
	pod, err := testCtx.ClientSet.CoreV1().Pods(testCtx.NS.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create pod: %v", err)
	}

	scheduled := waitForPodScheduled(t, testCtx, pod)
	if scheduled.Spec.NodeName != "node-1" {
		t.Errorf("expected pod on node-1, got %s", scheduled.Spec.NodeName)
	}
}

func waitForNodes(t *testing.T, testCtx *testutils.TestContext, count int) {
	t.Helper()
	err := wait.PollUntilContextTimeout(testCtx.Ctx, 100*time.Millisecond, 10*time.Second, false, func(context.Context) (bool, error) {
		return testCtx.Scheduler.Cache.NodeCount() >= count, nil
	})
	if err != nil {
		t.Fatalf("timed out waiting for %d nodes in cache", count)
	}
}
