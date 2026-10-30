.PHONY: build test vet fmt tidy release image clean

BIN := bin/brain-connector
IMAGE := brain-connector:$(VERSION)
RELEASE_TARGETS := linux/amd64 linux/arm64 windows/amd64 darwin/arm64
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/brain-connector

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w -s .

tidy:
	go mod tidy

release:
	rm -rf dist
	mkdir -p dist
	for target in $(RELEASE_TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		if [ "$$os" = windows ]; then ext=.exe; else ext=; fi; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" \
			-o dist/brain-connector-$$os-$$arch$$ext ./cmd/brain-connector || exit 1; \
	done
	cd dist && sha256sum brain-connector-* > SHA256SUMS

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -rf bin dist
