# wtd Development Guide

This guide details the prerequisites, local development workflow, testing standards, build procedures, and verification steps for contributors and developers working on `wtd` (`go-webtoons-downloader`).

---

## Prerequisites

Ensure the following dependencies are installed in your local environment:

- **Git**
- **Go 1.27+** (matching `go.mod`)
- **GNU Make**
- **golangci-lint**: [https://golangci-lint.run/docs/welcome/install/](https://golangci-lint.run/docs/welcome/install/)
- **govulncheck**: `go install golang.org/x/vuln/cmd/govulncheck@latest`
- **trivy**: [https://trivy.dev/docs/latest/getting-started/installation/](https://trivy.dev/docs/latest/getting-started/installation/)
- **hadolint**: [https://github.com/hadolint/hadolint#install](https://github.com/hadolint/hadolint#install)
- **gitleaks**: [https://github.com/gitleaks/gitleaks#installing](https://github.com/gitleaks/gitleaks#installing)
- **Docker** & **Docker Compose** (optional, for container validation)

---

## Repository Initialization

Clone the repository:
```bash
git clone https://github.com/underhax/go-webtoons-downloader.git
cd go-webtoons-downloader
```

---

## Code Quality & Testing Workflow

All validation, testing, and linting commands are accessible via the `Makefile`.

### 1. Code Formatting, Linting & Compilation Check
```bash
make check
```

### 2. Unit Testing
```bash
make test
```

### 3. Code Coverage
```bash
make coverage
```

### 4. Vulnerability & Security Scanning
```bash
make vulncheck
make trivy
```

### 5. Secret Detection
```bash
make secrets-scan
```

### 6. Dockerfile Validation
```bash
make docker-lint
```

### 7. Full Pre-flight Verification
```bash
make verify
```

---

## Build Workflow

`wtd` is written in pure Go with no CGO dependencies (`CGO_ENABLED=0`).

### 1. Standard Local Build
```bash
make build
```

### 2. Custom Version Injection
```bash
make build VERSION=v0.0.1
```

### 3. Cross-Compilation

```bash
# Linux x86_64 (amd64):
GOOS=linux GOARCH=amd64 make build

# Linux ARM64 (arm64):
GOOS=linux GOARCH=arm64 make build

# macOS Apple Silicon (arm64):
GOOS=darwin GOARCH=arm64 make build

# macOS Intel (amd64):
GOOS=darwin GOARCH=amd64 make build

# Windows x64 (amd64):
GOOS=windows GOARCH=amd64 make build

# Windows ARM64 (arm64):
GOOS=windows GOARCH=arm64 make build
```

---

## Docker Build & Deployment

### 1. Build the Docker Image Locally
Build the multi-stage `scratch`-based container image:
```bash
docker build -t wtd:dev --build-arg VERSION=dev -f Dockerfile .
```

To cross-compile for another architecture using Docker Buildx:
```bash
# For ARM64:
docker buildx build --platform linux/arm64 -t wtd:dev --build-arg VERSION=dev -f Dockerfile .

# For AMD64:
docker buildx build --platform linux/amd64 -t wtd:dev --build-arg VERSION=dev -f Dockerfile .
```

### 2. Run with Docker Compose
Run interactive commands or batch synchronization using the root compose file:
```bash
# Validate compose configuration:
docker compose config -q

# Run help command:
docker compose --profile tools run --rm wtd --help
```

---

## Makefile Targets Reference

| Target | Description |
| --- | --- |
| `make check` | Formats code with `gofmt -s -w`, runs `golangci-lint`, and checks compilation. |
| `make test` | Executes unit tests with race detection (`-race`) and verbose output (`-v`). |
| `make coverage` | Generates statement coverage reports (`coverage.out`) and displays function metrics. |
| `make vulncheck` | Analyzes dependencies for known vulnerabilities using `govulncheck`. |
| `make docker-lint` | Lints `Dockerfile` using `hadolint`. |
| `make trivy` | Scans the repository filesystem for `CRITICAL` and `HIGH` severity vulnerabilities. |
| `make secrets-scan` | Runs `gitleaks` to detect hardcoded secrets or credentials. |
| `make verify` | Executes the full verification suite (`check`, `test`, `vulncheck`, `docker-lint`, `trivy`, `secrets-scan`). |
| `make build` | Compiles a production binary with `CGO_ENABLED=0`, `-trimpath`, and version injection. |
| `make clean` | Removes test coverage output files. |
