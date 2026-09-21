# What the LiteLLM Compromise Means for How You Build a Gateway

In March 2026, an adversary compromised a maintainer account on PyPI and published a backdoored package that found its way into dependencies powering thousands of AI deployments.

For traditional microservices, a dependency compromise is bad. For an **LLM Gateway**, it is catastrophic.

The LLM gateway is uniquely positioned at the exact junction of enterprise trust:
1. It holds every upstream master API key (OpenAI, Anthropic, Google, Azure).
2. It sees every employee prompt and customer message before redaction.
3. It has egress access to the open internet.

If an attacker achieves arbitrary code execution inside a typical gateway container, they can dump all upstream keys from environment variables and exfiltrate enterprise conversations in minutes.

---

## 1. Why Python Runtimes Are Structurally Vulnerable

Most open-source gateways are written in Python. Python's runtime design creates substantial attack surface:
- Dynamic import hooks (`__import__`, `sys.meta_path`) allow malicious packages to execute arbitrary shell payloads during startup.
- Python containers typically bundle complete Linux distributions (Debian, Ubuntu, Alpine) containing `/bin/sh`, `curl`, and standard utilities attackers use for staging reverse shells.
- Heavy dependency trees (hundreds of transitive packages) make full auditability virtually impossible.

---

## 2. The ProofGate Defensive Blueprint

When designing ProofGate, we assumed that one day an attacker would attempt a supply-chain attack. We engineered defense-in-depth layers to ensure that even a compromised process cannot compromise customer secrets:

### Layer 1: Zero External Provider SDKs
ProofGate has **zero provider SDKs** in `go.mod`. All interactions with OpenAI, Anthropic, Gemini, and Vault are written in pure Go using standard `net/http` and `encoding/json`. A pre-commit and CI guard script (`scripts/check_no_provider_sdk.sh`) scans dependencies and fails if any third-party AI package is introduced.

### Layer 2: Sealed Envelope Encryption
Provider API keys are never stored in plaintext environment variables or plain config files:
- Each credential is encrypted using **AES-256-GCM** with a per-record Data Encryption Key (DEK).
- The DEK is encrypted with a Key Encryption Key (KEK) managed locally or via HashiCorp Vault Transit.
- Stored sealed credentials in PostgreSQL cannot be decrypted by database administrators or SQL injection exploits without access to the KEK.

### Layer 3: Bounded In-Memory Cache (TTL $\le$ 60s)
Plaintext provider keys exist in process memory only for the duration of a request, or inside a strictly bounded cache with a maximum time-to-live of 60 seconds (`cache_ttl: 60s`). A dedicated control-plane endpoint (`POST /admin/secrets/purge`) flushes in-memory keys across the cluster instantly upon credential rotation.

### Layer 4: Distroless, Non-Root, Immutable Containers
ProofGate images are built from `gcr.io/distroless/static-debian12`:
- **No shell:** `/bin/sh`, `/bin/bash`, and `busybox` do not exist.
- **No interpreter:** Python, Node, and Perl do not exist.
- **No package manager:** `apt`, `yum`, and `apk` do not exist.
- **Read-Only Root Filesystem:** Containers run with `read_only: true` and `cap_drop: [ALL]`. An attacker cannot write a binary to disk or install a malicious library.
- **Non-Root User:** Runs strictly as non-root UID `65532:65532`.

### Layer 5: Cryptographic Supply-Chain Verification
Every release build is reproducible, signed, and attested:
- **Pinned Actions:** Every GitHub Action in CI is pinned to an immutable 40-character commit SHA verified by `scripts/check_pinned_actions.sh`.
- **Cosign Keyless Signatures:** Container images are signed keylessly via Sigstore / GitHub Actions OIDC.
- **SPDX Software Bill of Materials (SBOM):** Generated via Syft and attached to release artifacts.
- **SLSA Build Provenance:** Level 3 build provenance attestations generated for all binaries.

---

## 3. How to Verify a ProofGate Release

To cryptographically verify any ProofGate release before running it in production:

```bash
# Verify the container signature with Cosign
cosign verify ghcr.io/proofgate/proofgate:v1.0.0 \
  --certificate-identity-regexp="https://github.com/proofgate/proofgate" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com"

# Verify the attached SBOM
cosign verify-attestation --type spdxjson ghcr.io/proofgate/proofgate:v1.0.0
```

Trust in AI infrastructure must be earned mathematically, architectural by architectural layer.
