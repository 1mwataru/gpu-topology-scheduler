# GPU Topology-Aware Scheduler Plugin

Kubernetes Scheduler Score Plugin that places Prefill/Decode Pod pairs close together based on GPU topology, minimizing KV Cache transfer latency.

## How it works

When a Pod with `gpu-topology/role` and `gpu-topology/group` labels is scheduled, the plugin finds its peer Pods (same group, opposite role) and scores candidate nodes by physical proximity:

| Distance | Score |
|----------|-------|
| Same Node (NVLink) | 100 |
| Same NVLink Domain | 80 |
| Same Rack (InfiniBand 1-hop) | 50 |
| Different Rack | 10 |

If no peer exists yet, a neutral score of 50 is returned.

## Labels

### Node labels (set by cluster admin)

```yaml
gpu-topology/nvlink-domain: "rack01-domain01"
gpu-topology/rack: "rack01"
```

### Pod labels (set by user)

```yaml
gpu-topology/role: "prefill"   # or "decode"
gpu-topology/group: "llama70b-serving-01"
```

## Build

```bash
make build   # builds bin/kube-scheduler
make test    # runs unit tests
make image   # builds Docker image
```

## Deploy

```bash
kubectl apply -f deploy/configmap.yaml
kubectl apply -f deploy/deployment.yaml
```

Then set `schedulerName: gpu-topology-scheduler` on your Pods.

## Configuration

Scores are configurable via `GpuTopologyArgs` in the scheduler config:

```yaml
pluginConfig:
  - name: GpuTopology
    args:
      scoreSameNode: 100
      scoreSameNVLinkDomain: 80
      scoreSameRack: 50
      scoreDifferentRack: 10
```

## Directory Structure

```
├── cmd/scheduler/main.go          # Entrypoint
├── pkg/plugins/gputopology/
│   ├── plugin.go                  # Score plugin implementation
│   └── plugin_test.go             # Unit tests
├── apis/config/
│   ├── types.go                   # GpuTopologyArgs
│   └── config.go                  # Scheme registration
├── deploy/
│   ├── scheduler-config.yaml      # KubeSchedulerConfiguration
│   ├── configmap.yaml             # ConfigMap for scheduler config
│   └── deployment.yaml            # Deployment + RBAC
├── Dockerfile
└── Makefile
```
