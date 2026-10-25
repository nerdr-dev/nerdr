BINARY := nerdr
DIST := dist
LDFLAGS := -s -w

.PHONY: build test e2e vet fmt install clean all

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/nerdr

all: clean
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64  ./cmd/nerdr
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64  ./cmd/nerdr
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 ./cmd/nerdr
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 ./cmd/nerdr

test:
	go test -race ./...

# Drives the real binary in a pseudo-terminal with a fake agent.
e2e:
	go test -tags e2e ./internal/e2e/ -count=1 -v

vet:
	go vet ./...

fmt:
	gofmt -w .

install:
	CGO_ENABLED=0 go install ./cmd/nerdr

clean:
	rm -rf $(DIST) $(BINARY)
