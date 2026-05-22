FROM golang:1.26 AS builder
WORKDIR /workspace
COPY . .
RUN make build

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /workspace/bin/kube-scheduler /usr/local/bin/kube-scheduler
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kube-scheduler"]
