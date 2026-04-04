APP_NAME := iac-studio
VERSION  := 0.1.0
BUILD    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-s -w -X main.AppVersion=$(VERSION)-$(BUILD)"

.PHONY: all build dev test clean deps docker install release

all: build

## ─── Development ───

deps:
	go mod tidy
	cd web && npm install

dev:
	@echo "Starting backend + frontend in dev mode..."
	@trap 'kill 0' INT; \
		cd web && npm run dev & \
		go run $(LDFLAGS) ./cmd/server -port 3001 & \
		wait

## ─── Build ───

build: build-frontend embed-frontend build-backend

build-frontend:
	cd web && npm run build

embed-frontend:
	rm -rf cmd/server/frontend/dist
	cp -r web/dist cmd/server/frontend/dist

build-backend:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/server

## ─── Test ───

test:
	go test ./... -v -race -cover

test-frontend:
	cd web && npm test

lint:
	golangci-lint run ./...

## ─── Docker ───

docker:
	docker build -t $(APP_NAME):$(VERSION) .

docker-run:
	docker run -it --rm \
		-p 3000:3000 \
		-v "$$HOME/.iac-studio:/data" \
		-v "$$HOME/iac-projects:/projects" \
		$(APP_NAME):$(VERSION)

## ─── Install ───

install: build
	cp bin/$(APP_NAME) /usr/local/bin/$(APP_NAME)
	@echo "Installed to /usr/local/bin/$(APP_NAME)"

## ─── Release (cross-compile) ───

release: build-frontend embed-frontend
	@mkdir -p dist
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/$(APP_NAME)-linux-amd64   ./cmd/server
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/$(APP_NAME)-linux-arm64   ./cmd/server
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/$(APP_NAME)-darwin-amd64  ./cmd/server
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/$(APP_NAME)-darwin-arm64  ./cmd/server
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/$(APP_NAME)-windows-amd64.exe ./cmd/server
	@echo "Binaries in dist/"

## ─── Clean ───

clean:
	rm -rf bin/ dist/ web/dist/
	go clean

## ─── Help ───

help:
	@echo "IaC Studio - Build targets:"
	@echo ""
	@echo "  make deps      Install Go + Node dependencies"
	@echo "  make dev       Run in development mode (hot reload)"
	@echo "  make build     Build production binary"
	@echo "  make test      Run all tests"
	@echo "  make docker    Build Docker image"
	@echo "  make install   Install to /usr/local/bin"
	@echo "  make release   Cross-compile for all platforms"
	@echo "  make clean     Remove build artifacts"
