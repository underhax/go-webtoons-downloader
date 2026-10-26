VERSION ?= dev

BINARY := wtd
ifeq ($(GOOS),windows)
	BINARY := wtd.exe
else ifeq ($(OS),Windows_NT)
	ifndef GOOS
		BINARY := wtd.exe
	endif
endif

.PHONY: check test coverage vulncheck docker-lint trivy verify build secrets-scan clean

check:
	@test -z "$$(gofmt -s -l .)" || (echo "Unformatted files found. Run 'gofmt -s -w .' to fix them." && false)
	golangci-lint run ./...
	go build ./...

test:
	go test -v -race ./...

coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

vulncheck:
	govulncheck ./...

docker-lint:
	hadolint Dockerfile

trivy:
	trivy fs --severity CRITICAL,HIGH .

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.AppVersion=$(VERSION)" -o $(BINARY) ./cmd/wtd

secrets-scan:
	gitleaks detect --source . --no-git --no-color

verify: check test vulncheck docker-lint trivy secrets-scan

clean:
	rm -rf coverage *.out
