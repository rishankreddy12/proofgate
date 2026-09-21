# ProofGate v1.0.0 Launch Go / No-Go Checklist

This checklist defines the empirical gate requirements prior to publishing ProofGate v1.0.0 and announcing to the public.

---

## Pre-Launch Verification Gate

| Gate Requirement | Status | Required Evidence / Verification Command |
| :--- | :---: | :--- |
| **Comprehensive Test Suite** | Passed | `make lint test test-integration e2e` runs 100% clean with `-race` and zero linter warnings. |
| **No Hand-Written Numbers** | Passed | `go run ./cmd/benchreport -check` exits 0; all published numbers match committed JSON files. |
| **Production Helm Chart** | Passed | `helm lint deploy/helm/proofgate` and `helm unittest deploy/helm/proofgate` pass cleanly. |
| **10-Minute Quickstart SLA** | Passed | `scripts/first_run_test.sh` completes in **{{bench:first-run.json:seconds}}s** (SLA $\le 600\text{s}$) from clean clone. |
| **Cryptographic Supply-Chain** | Passed | `bash scripts/check_pinned_actions.sh .` passes; all GitHub Actions pinned to 40-character SHAs. Keyless Cosign signing enabled. |
| **Machine-Checked Threat Model** | Passed | `go test ./internal/securitydocs/` verifies all 13 STRIDE threats against active test symbols. `SECURITY.md` present. |
| **Configuration Reference Drift** | Passed | `go test ./internal/configdocs/` reflects over `config.Config` and verifies all YAML tags are documented in `docs/configuration.md`. |
| **Documented Results Pages** | Passed | `docs/results/phase2-cache.md`, `docs/results/phase3-proof.md`, `docs/results/phase4.md`, and `docs/benchmarks.md` published. |
| **Explicit Scope & Limitations** | Passed | README and launch posts explicitly enumerate what ProofGate does *not* do. |
| **Trivy Vulnerability Scan** | Passed | Distroless container image scans with 0 HIGH and 0 CRITICAL CVEs. |
| **Zero Provider SDK Policy** | Passed | `bash scripts/check_no_provider_sdk.sh go.mod` confirms zero third-party AI SDKs. |

---

## Operational Readiness

- **Rollback Procedure:** In the event of an unexpected release regression, immediate rollback to `v0.5.0` tag is tested.
- **Monitoring & Alerts:** Grafana dashboard provisioned and scraping `/metrics` on port 9090.
- **Community Inbox:** Security reporting via `SECURITY.md` email contact.
