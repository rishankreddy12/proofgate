# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
FROM golang:1.25@sha256:699337d620559a59b4a2bb298ad59611e535d2ee755a34cf2d2a98f37578dc80 AS build
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
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/proofgate"]
