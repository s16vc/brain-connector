# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/brain-connector ./cmd/brain-connector

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/brain-connector /usr/local/bin/brain-connector
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/brain-connector"]
