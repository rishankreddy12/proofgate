# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
FROM golang:1.27@sha256:e0174e51e81218523251d85d248a90d24c3d5e81543b4f07a5d66229397db190 AS build
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOFLAGS=-mod=readonly go build -trimpath -buildvcs=true \
    -ldflags "-s -w -X github.com/proofgate/proofgate/internal/version.Version=${VERSION}" \
    -o /out/ ./cmd/proofgate ./cmd/proofgatectl

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build --chmod=0755 /out/ /usr/local/bin/
COPY deploy/proofgate.yaml /etc/proofgate/proofgate.yaml
USER nonroot:nonroot
EXPOSE 8080 9090 9091
ENTRYPOINT ["/usr/local/bin/proofgate"]
