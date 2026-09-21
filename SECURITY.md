# Security Policy

## Reporting a Vulnerability

We take the security of ProofGate seriously. If you discover a security vulnerability, please report it responsibly:

- **Do NOT open a public GitHub issue.**
- Submit a **Private Vulnerability Report** via GitHub at `https://github.com/<owner>/proofgate/security/advisories/new`.
- Alternatively, email the maintainers directly with reproduction steps and impact details.
- We will acknowledge receipt within 48 hours and provide an estimated timeline for a patch.

---

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.5.x   | :white_check_mark: |
| 0.4.x   | :white_check_mark: |
| < 0.4.0 | :x:                |

---

## Verifying Release Artifacts

All container images published to GitHub Container Registry (GHCR) are signed keylessly using [Cosign](https://github.com/sigstore/cosign) with GitHub Actions OIDC identity, and include SPDX Software Bill of Materials (SBOM) and SLSA build provenance attestations.

### 1. Verify Image Signature
```bash
cosign verify ghcr.io/<owner>/proofgate:v0.5.0 \
  --certificate-identity-regexp 'https://github.com/<owner>/proofgate/\.github/workflows/release\.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

### 2. Verify Build Provenance Attestation
```bash
gh attestation verify oci://ghcr.io/<owner>/proofgate:v0.5.0 --owner <owner>
```

### 3. Verify Binary Checksums
```bash
sha256sum -c SHA256SUMS
```

---

## Repository Hardening Guidelines

For production deployments and fork maintainers, ensure the following GitHub repository settings are enforced:
1. **Branch Protection**: Enable branch protection on `main`.
   - Require pull request reviews before merging.
   - Require review from Code Owners (`.github/CODEOWNERS`).
   - Require status checks to pass before merging (`guard`, `test`, `image`).
   - Require branches to be up to date before merging.
2. **Secret Scanning**: Enable automated secret scanning and push protection.
3. **Dependabot**: Keep Dependabot enabled for security updates on Go modules, Docker, and GitHub Actions.
