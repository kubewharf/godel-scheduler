# Define the builder stage
FROM golang:1.21 as builder

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Copy the go source
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY hack/ hack/
COPY vendor/ vendor/
COPY Makefile Makefile
COPY Makefile.expansion Makefile.expansion

RUN export GO_BUILD_PLATFORMS=linux/amd64 && make build

FROM debian:bookworm
RUN apt-get update && \
    apt-get install -y binutils && \
    apt-get clean && \
    ldd --version

WORKDIR /root
COPY --from=builder /workspace/bin/linux_amd64/* /usr/local/bin/
