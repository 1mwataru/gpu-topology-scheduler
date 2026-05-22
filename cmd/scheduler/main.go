package main

import (
	"os"

	"k8s.io/component-base/cli"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"

	"gpu-topology-scheduler/pkg/plugins/gputopology"
)

func main() {
	command := app.NewSchedulerCommand(
		app.WithPlugin(gputopology.Name, gputopology.New),
	)

	code := cli.Run(command)

	os.Exit(code)
}
