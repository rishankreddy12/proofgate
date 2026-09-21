# Contributing to ProofGate

Thank you for your interest in contributing to ProofGate! As an enterprise-grade gateway providing mathematical guarantees, we maintain strict standards for correctness, security, and reproducibility.

---

## 1. Core Engineering Principles

1. **Correctness over Haste**: Implement the smallest robust change that satisfies requirements.
2. **Evidence-Based Reasoning**: Never claim a change works merely because the code looks plausible. All changes must be verified with tests.
3. **No Unverifiable Numbers**: No performance or latency number may ever be hand-written in documentation. All numbers must be generated from committed JSON result files and validated with `cmd/benchreport -check`.
4. **Zero Provider SDKs**: We strictly prohibit importing third-party provider SDKs (e.g. `github.com/sashabaranov/go-openai`, `anthropic-sdk-go`, Python packages). All upstream communication must use pure Go standard library `net/http` and `encoding/json`.

---

## 2. Development Setup & Testing

### Prerequisites
- Go 1.23+
- Docker & Docker Compose
- `bash` shell

### Local Verification Commands

```bash
# Run unit test suite with race detector
make test

# Run integration tests requiring Postgres and Redis
make test-integration

# Run linter and formatting
make lint

# Run zero-SDK guardrail verification
make guard

# Run end-to-end integration and security test suite
make e2e

# Verify benchmark markers and documentation parity
go run ./cmd/benchreport -check

# Verify threat model and configuration documentation parity
go test ./internal/securitydocs/ ./internal/configdocs/
```

---

## 3. Pull Request Guidelines

1. **Targeted Commits**: Write descriptive commit messages using the Conventional Commits specification (e.g. `feat(router): ...`, `fix(cache): ...`, `docs: ...`).
2. **Pinned Actions**: Any changes to `.github/workflows/` must pin GitHub Actions to full 40-character commit SHAs. Run `bash scripts/check_pinned_actions.sh .` before submitting.
3. **Documentation Parity**: If modifying configuration fields in `internal/config/config.go`, you must update `docs/configuration.md`. The `TestEveryConfigFieldIsDocumented` test will enforce this.
4. **Clean Git Tree**: Ensure all generated files are up-to-date and the working directory is clean.
