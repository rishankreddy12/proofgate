# ProofGate Helm Chart

Production-ready Kubernetes Helm chart for **ProofGate** — an enterprise-grade, high-performance LLM gateway with statistical verification, safe semantic caching, quality-verified model routing, and zero-trust key handling.

## Pod Security Standards (Hardened by Default)

The default configuration strictly complies with the Kubernetes **Restricted** Pod Security Standard:
- Non-root execution: `runAsUser: 65532`, `runAsGroup: 65532`, `fsGroup: 65532`
- Read-only root filesystem: `readOnlyRootFilesystem: true`
- All Linux capabilities dropped: `capabilities.drop: [ALL]`
- Privilege escalation disabled: `allowPrivilegeEscalation: false`
- Default seccomp profile: `RuntimeDefault`
- Topology spread constraints: anti-affinity across host nodes (`kubernetes.io/hostname`)
- Dual health probes:
  - Startup & Liveness: `/healthz` on client port `8080`
  - Readiness: `/readyz` on isolated admin port `9090` (checks Postgres and Redis before routing traffic)

---

## Deployment Architectures

### 1. Database-Sealed Keys with Local KEK Secret (Recommended Standard)

Provider keys are stored envelope-encrypted in PostgreSQL using per-record data keys (AES-256-GCM). The 32-byte Key Encryption Key (KEK) is mounted as a Kubernetes Secret.

```bash
# 1. Generate a 32-byte base64 KEK
go run ./cmd/proofgatectl kek generate > kek.b64

# 2. Create the Kubernetes secret
kubectl create secret generic proofgate-kek --from-file=local_kek=kek.b64

# 3. Install chart referencing the secret
helm install proofgate deploy/helm/proofgate \
  --set secrets.localKek.existingSecret=proofgate-kek
```

### 2. HashiCorp Vault Transit Engine with Kubernetes Auth (Zero Secrets in Pod)

ProofGate uses its Kubernetes ServiceAccount token (`/var/run/secrets/kubernetes.io/serviceaccount/token`) to authenticate against Vault (`/v1/auth/kubernetes/login`) and encrypts/decrypts per-record data keys using Vault Transit (`/v1/transit/encrypt/<key>`).

```bash
helm install proofgate deploy/helm/proofgate \
  --set vault.enabled=true \
  --set vault.addr="https://vault.corp.internal:8200" \
  --set vault.role="proofgate-workload"
```

### 3. Environment Variable Keys (Development / Simple Staging)

Keys resolved directly from environment variables passed via `env` or `envFrom`.

---

## Configuration Reference

| Parameter | Description | Default |
| :--- | :--- | :--- |
| `replicaCount` | Number of gateway replicas | `3` |
| `image.repository` | Container image repository | `proofgate` |
| `image.tag` | Container image tag | `""` (defaults to chart `appVersion`) |
| `image.digest` | Pinned image SHA256 digest | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `podSecurityContext` | Pod-level security context | Non-root 65532, RuntimeDefault |
| `securityContext` | Container-level security context | Read-only root FS, drop ALL caps |
| `service.httpPort` | Client traffic port | `8080` |
| `service.adminPort` | Admin & metrics port | `9090` |
| `resources.requests` | CPU and memory requests | `500m`, `512Mi` |
| `resources.limits` | CPU and memory limits | `2`, `2Gi` |
| `networkPolicy.enabled` | Enable egress and ingress filtering | `true` |
| `networkPolicy.dnsPort` | CoreDNS / KubeDNS port | `53` |
| `networkPolicy.egressCIDRs`| Allowed private CIDRs for DBs | `[10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16]` |
| `vault.enabled` | Enable HashiCorp Vault integration | `false` |
| `vault.addr` | Vault server API address | `""` |
| `vault.role` | Vault Kubernetes auth role | `"proofgate-role"` |
| `secrets.localKek.existingSecret` | Secret holding `local_kek` file | `""` |
| `autoscaling.enabled` | Enable Horizontal Pod Autoscaler | `false` |
| `pdb.enabled` | Enable Pod Disruption Budget | `true` |
| `pdb.minAvailable` | Minimum available pods during disruption | `1` |
| `serviceMonitor.enabled` | Prometheus Operator ServiceMonitor | `false` |
| `config` | Raw `proofgate.yaml` gateway configuration | (default mock route) |

---

## Verification & Testing

```bash
# Lint chart syntax
helm lint deploy/helm/proofgate

# Run unit tests
helm unittest deploy/helm/proofgate
```
