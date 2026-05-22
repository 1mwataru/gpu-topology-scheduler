IMAGE ?= gpu-topology-scheduler
TAG ?= latest

.PHONY: all build test clean image

all: build

build:
	CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/kube-scheduler cmd/scheduler/main.go

test:
	go test ./... -v

clean:
	rm -rf bin/

image:
	docker build -t $(IMAGE):$(TAG) .
