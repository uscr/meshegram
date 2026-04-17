FROM --platform=$BUILDPLATFORM golang:1.22 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build -trimpath -ldflags="-s -w" -o /out/meshegram ./cmd/meshegram

FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS certs
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && update-ca-certificates \
 && rm -rf /var/lib/apt/lists/*

FROM scratch
COPY --from=certs   /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/meshegram /bin/meshegram
USER 65532:65532
ENTRYPOINT ["/bin/meshegram"]
