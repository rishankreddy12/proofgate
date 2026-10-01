# ProofGate Admin CLI & Control-Plane Guide

`proofgatectl` is the administrative command-line interface for the ProofGate LLM gateway. It operates in two modes:

1. **Remote Control-Plane Mode** (New): Connects over HTTPS to the gateway's dedicated admin interface (`127.0.0.1:9090` or reverse-proxied admin endpoint) to manage users, sessions, providers, configuration, and runtime health.
2. **Direct Database Mode** (Existing): Connects directly to PostgreSQL and ClickHouse to perform initial bootstrapping, key provisioning, and offline data operations.

---

## 1. Quickstart & First-Time Setup

### 1.1 Server Configuration

Enable admin authentication in your `proofgate.yaml`:

```yaml
admin_auth:
  enabled: true
  max_login_attempts: 5
  lockout_duration: 15m
  session_idle_timeout: 30m
  session_absolute_timeout: 12h
```

### 1.2 Bootstrap Initial Admin User

Before remote commands can be authenticated, you must create the initial administrator directly on the server machine using direct database access:

```bash
export DATABASE_URL="postgres://pg:pg@localhost:5432/pg?sslmode=disable"
proofgatectl bootstrap-admin --username admin
```

You will be prompted to enter and confirm a secure password (minimum 8 characters).

> **Security Note:** `bootstrap-admin` is only allowed when no administrative users exist in the database. Once created, all subsequent user management must be performed through the authenticated control-plane API.

### 1.3 Configure a Profile & Log In

Add a profile pointing to the gateway's admin API endpoint:

```bash
proofgatectl profile add --name local --server http://localhost:9090
proofgatectl profile use --name local

proofgatectl login
```

Enter your username and password. On successful authentication, a session token is securely persisted in your user configuration directory.

Verify your authentication:

```bash
proofgatectl whoami
```

---

## 2. Command Reference

### 2.1 Global Flags

The following flags are accepted before any command:

| Flag | Description |
|------|-------------|
| `--profile NAME` | Specify which server profile to use for this command |
| `--server URL` | Override the server URL for this invocation |
| `--output json` | Emit output formatted as JSON for scripting and automation |
| `--insecure` | Allow insecure HTTP connections or bypass TLS verification (warns to stderr) |

---

### 2.2 Authentication & Session Commands

#### `proofgatectl login [--server URL]`
Authenticates with the control-plane server via username and password prompted securely on standard input (no echo).
- Session tokens are opaque, high-entropy 256-bit random tokens prefixed with `pgadmin_`.
- Plaintext tokens are stored exclusively on the client with `0600` file permissions; the server only retains SHA-256 hashes in Redis.

#### `proofgatectl logout`
Revokes the active session token on the server and deletes the local session token file.

#### `proofgatectl whoami`
Displays the authenticated principal: user ID, username, assigned role, and session expiration timestamp.

#### `proofgatectl session list`
Lists active sessions.
- Users with `admin` role can see all active sessions across all users.
- Users with `operator` or `viewer` roles see their own active sessions.

#### `proofgatectl session revoke --id <SESSION_ID>`
Revokes a specific session identified by its session ID (hash hex).

#### `proofgatectl session revoke-all [--user <USERNAME>]`
Revokes all active sessions. Non-admin users may only revoke their own sessions; administrators may revoke sessions for any specified user or all users globally.

---

### 2.3 User Administration (`admin` role required)

#### `proofgatectl user create --username <NAME> --role <ROLE>`
Creates a new administrative user.
- Supported roles: `admin`, `operator`, `viewer`.
- Securely prompts for the user's password on stdin.
- Passwords are encrypted with bcrypt (cost 12).

#### `proofgatectl user list`
Lists all administrative users with their status, role, and creation timestamp.

#### `proofgatectl user get --username <NAME>`
Displays detailed information for a specified user.

#### `proofgatectl user enable --username <NAME>`
Enables a disabled user account.

#### `proofgatectl user disable --username <NAME>`
Disables a user account and immediately revokes all of their active sessions across the gateway cluster.

#### `proofgatectl user delete --username <NAME>`
Permanently deletes an administrative user and revokes all active sessions.

#### `proofgatectl user change-password [--username <NAME>]`
Changes a password. Administrators can target any user via `--username`; non-admin users can omit `--username` to change their own password.

---

### 2.4 Provider Management (`admin` & `operator` roles)

#### `proofgatectl provider list`
Displays all configured LLM providers, their provider type, base URL, and whether credentials are configured.
> **Security:** Provider API keys are never exposed in this listing.

#### `proofgatectl provider get --name <PROVIDER>`
Inspects details of a single configured provider.

#### `proofgatectl provider test --name <PROVIDER>`
Sends an end-to-end minimal test probe (`max_tokens: 1`) to verify provider upstream connectivity, returning status and latency in milliseconds.

#### `proofgatectl provider credential set --provider <PROVIDER>`
Stores or rotates an upstream provider API key.
- Prompts for the API key on stdin (or reads from pipe).
- The key is sent over HTTPS to the control plane, where it is envelope-encrypted using the gateway's Key Encryption Key (KEK) with provider-bound Authenticated Additional Data (AAD).
- Purges the gateway's key cache atomically.

#### `proofgatectl provider credential list`
Lists historical and active credential versions for configured providers.

---

### 2.5 Configuration Management

#### `proofgatectl config show`
Fetches and displays the active running configuration from memory.
- All secrets, API keys, tokens, and database passwords are automatically redacted.
- Supports `--output json` or default YAML format.

#### `proofgatectl config validate --file <PATH>`
Locally validates a `proofgate.yaml` file against the gateway schema and constraints without communicating with the server.

#### `proofgatectl config reload`
Triggers an atomic configuration reload on the running gateway (`config:reload` permission required). Re-evaluates routing, provider configurations, and policies with zero downtime.

---

### 2.6 Operational Commands

#### `proofgatectl status`
Shows running gateway version, uptime, listener bindings, active provider count, route count, and control-plane status.

#### `proofgatectl health`
Displays the real-time target tracker snapshot, provider health states, EWMA latencies, error rates, and circuit breaker status.

#### `proofgatectl cache purge [--route <ROUTE>] [--tenant <TENANT>]`
Purges exact and semantic response cache entries globally or filtered by route or tenant.

#### `proofgatectl chat [flags] [prompt]`
Starts an interactive terminal chat session or executes a one-shot prompt with real-time streaming tokens.
- **Interactive REPL**: Run `proofgatectl chat` without arguments to start an interactive chat session with multi-turn conversation memory. Type `exit` or `quit` to exit.
- **One-Shot Mode**: Run `proofgatectl chat "Your prompt here"` to stream a single answer to standard output.
- **Piped Mode**: Run `echo "prompt" | proofgatectl chat` to process standard input.
- **Flags**:
  - `--route <NAME>`: Specify the route or model to use (default: `default`).
  - `--system <PROMPT>`: Optional system instructions.
  - `--no-stream`: Disable token streaming and print the complete response.
  - `--key <KEY>`: Data-plane API key (`pg_live_...`) if connecting directly to public port `:8080`.
  - `--data-url <URL>`: Public data-plane URL (default: `http://localhost:8080`).

---

### 2.7 Profile Management (Client-Side)

Client profiles allow seamless switching between different gateway clusters (e.g. local, staging, production):

```bash
# List profiles
proofgatectl profile list

# Add a new profile
proofgatectl profile add --name prod --server https://proofgate-admin.internal:9090

# Switch active profile
proofgatectl profile use --name prod

# Delete a profile
proofgatectl profile delete --name old-env
```

Profile configurations are stored in:
- Linux / macOS: `~/.config/proofgatectl/config.yaml`
- Windows: `%APPDATA%\proofgatectl\config.yaml`

Session tokens are stored separately with `0600` permissions in:
- Linux / macOS: `~/.config/proofgatectl/sessions/<profile>.json`
- Windows: `%APPDATA%\proofgatectl\sessions\<profile>.json`

---

## 3. Role-Based Access Control (RBAC)

ProofGate enforces three administrative tiers:

| Permission | `admin` | `operator` | `viewer` | Description |
|------------|:-------:|:----------:|:--------:|-------------|
| `user:manage` | ✅ | ❌ | ❌ | Create, modify, disable, and delete admin users |
| `provider:manage` | ✅ | ✅ | ❌ | Set credentials, test provider connectivity |
| `config:reload` | ✅ | ✅ | ❌ | Trigger atomic configuration reloads |
| `config:view` | ✅ | ✅ | ✅ | Inspect running redacted configuration & providers |
| `cache:purge` | ✅ | ✅ | ❌ | Purge cache entries |
| `session:manage` | ✅ | ❌ | ❌ | Revoke other users' sessions (own sessions allowed) |
| `health:view` | ✅ | ✅ | ✅ | Inspect system health snapshots |
| `status:view` | ✅ | ✅ | ✅ | Inspect cluster status and uptime |

---

## 4. Security Considerations & Threat Mitigations

1. **Transport Security**:
   - The admin port (`9090`) should be bound to `127.0.0.1` or routed through a secure internal ingress/VPN with TLS enabled.
   - When using `--insecure` for development, `proofgatectl` prints a security warning to stderr.
2. **Session Storage & Hashing**:
   - Plaintext session tokens are never stored in the database or Redis. Redis holds only the SHA-256 hash `proofgate:session:<hash>`.
   - Redis TTLs strictly enforce absolute session timeouts.
   - Idle timeouts automatically terminate inactive sessions.
3. **Brute-Force Protection**:
   - Failed login attempts are tracked per username in Redis (`proofgate:login_fail:<username>`).
   - Exceeding `max_login_attempts` (default: 5) triggers a lockout duration (default: 15 minutes) returning HTTP 429 (`Retry-After`).
4. **Audit Trail**:
   - All authentication attempts, user lifecycle events, credential updates, and configuration reloads are recorded to the `admin_audit` table with timestamp, actor, action, IP, and outcome.

---

## 5. Migration Guide for Existing Deployments

ProofGate's control-plane implementation is 100% backward-compatible:

1. **Default Disabled**: When `admin_auth.enabled` is `false` (the default), existing endpoints on `:9090` (`/admin/reload`, `/admin/cache/purge`, `/metrics`, etc.) operate without authentication, exactly as in prior versions.
2. **Upgrading**:
   - Apply migrations: `0004_admin_users.sql` and `0005_admin_audit.sql` will be applied automatically on server startup.
   - Bootstrap the first admin user:
     ```bash
     proofgatectl bootstrap-admin --username admin
     ```
   - Enable authentication in `proofgate.yaml`:
     ```yaml
     admin_auth:
       enabled: true
     ```
   - The gateway will immediately require session tokens for all `/admin/cp/*` and destructive administrative endpoints, while keeping monitoring endpoints (`/readyz`, `/metrics`, `/admin/health`) accessible to monitoring agents.
