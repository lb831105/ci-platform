# Lightweight CI/CD Platform

[简体中文](README.md) | **English**

File-system storage, local toolchain builds, and SSH deployment. **No database required**, **frontend embedded**, **single-binary delivery**.

A pragmatic Jenkins-style workflow for small and medium teams:

```text
Job → manual / webhook / scheduled trigger → local build on the server → live logs
    → archived artifacts → SSH deployment → notifications / event subscriptions
```

The web console is built with Vue 3 + Element Plus and provides job management, build history, live logs, parameterized builds, deployment approvals, RBAC, credential management, dashboards, and system settings.

---

## Highlights

| Capability | Description |
|---|---|
| Jobs & triggers | Manual, webhook, and cron triggers; branch allowlists, event filtering, idempotent triggering, disabled jobs |
| Builds | Local shell steps; timeout, retry, allow-failure, and `when` conditions; per-step status and duration persisted |
| Parameterized builds | `string` / `bool` / `choice` / `text` / `password`; password values encrypted at rest and masked in logs |
| Artifacts & cache | Artifact archiving, downloads, cross-job promotion; explicit cache restore/save |
| Deployment | Multi-target SSH deployment, republish, deployment approval, deployment records, environment snapshots |
| Testing | JUnit report collection; failures/errors affect build status |
| Observability | Build dashboard, Prometheus `/metrics`, `/readyz`, `/openapi.json` |
| Security | API tokens with scopes, RBAC, AES-GCM encrypted settings, audit log, log masking |
| Integrations | Email, DingTalk, WeCom, Slack; global outbound event subscriptions with HMAC signatures |
| Migration | Jenkins `config.xml` import; platform config export/import |

---

## Quick Start

### 1. Get the binary

Run the published binary directly:

```bash
# Linux
./dist/ci-platform-linux-amd64

# Windows
dist\ci-platform-windows-amd64.exe
```

Or build it yourself. The frontend is embedded via `go:embed`:

```bash
./build.sh --single          # Linux / macOS / Git Bash
build.bat --single           # Windows cmd
```

See [Build & Verification](docs/build.md) for details. The detailed documentation is currently written in Chinese.

### 2. Start the server

The default listen address is `:8080`, and the default data directory is `./data`:

```bash
./dist/ci-platform-linux-amd64 \
  -auth-user admin \
  -auth-password 'change-me-now'
```

Set an administrator password during deployment. If omitted, a random password is generated and printed once in the startup log; **`admin/admin` is no longer used**.

For local debugging only, authentication can be disabled:

```bash
./dist/ci-platform-linux-amd64 -no-auth
```

See [Environment Variables](docs/configuration.md) and [Getting Started](docs/getting-started.md) for data directories, HTTPS, reverse proxies, and environment variables.

### 3. Open the console

```text
http://[IP]:8080
```

Recommended first steps:

1. Check JDK / Node / Maven tool paths under **Manage Jenkins → Global Tool Configuration**.
2. Add Git credentials under **Credentials**.
3. Create a job, or use **Quick create and build**.
4. If SSH deployment is needed, configure targets under **System Configuration → SSH Servers**.

### 4. Run your first build

Click **Build Now** on the job page, or use the CLI:

```bash
go build -o bin/ci ./cmd/ci
./bin/ci login --server http://[IP]:8080 --token cip_xxx
./bin/ci build demo-app --wait -param VERSION=1.2.3
./bin/ci logs demo-app 1 --tail 200
```

See [API & CLI](docs/api.md) for the full API and CLI usage.

---

## Local Development

Requirements:

- Go 1.24+
- Node.js 18+
- Git

```bash
# Backend
go run ./cmd/server

# Frontend dev server (/api and /ws proxy to :8080)
cd web
npm install
npm run dev
```

Common checks:

```bash
go test ./...
go vet ./...
cd web && npm run typecheck && npm test && npm run build
```

See [Build & Verification](docs/build.md) for full builds, cross-compilation, version injection, and UPX packaging.

---

## Documentation

The detailed documentation is currently in Chinese.

| Document | Contents |
|---|---|
| [Getting Started](docs/getting-started.md) | Single-binary run, tool configuration, logs, artifacts, local development |
| [Environment Variables](docs/configuration.md) | Startup flags and environment variables |
| [Build & Verification](docs/build.md) | `build.sh` / `build.bat`, cross-compilation, version injection, artifacts |
| [Architecture](docs/architecture.md) | Technology choices, file-system storage, execution flow, directory layout |
| [API & CLI](docs/api.md) | Response envelope, endpoint list, OpenAPI, CLI usage |
| [Pipeline & Deployment](docs/pipeline.md) | Parameters, step controls, SSH deployment, deployment records, outbound events |
| [Jenkins Compatibility](docs/jenkins.md) | System management, job pages, configuration pages, explicit non-goals |
| [Migration from Jenkins](docs/migration.md) | `config.xml` import, config export/import, build-history migration |
| [Validation](docs/validation.md) | Functional scenarios and e2e coverage |

Additional design and audit documents:

| Document | Contents |
|---|---|
| [DESIGN.md](docs/DESIGN.md) | Architecture layers, key mechanisms, sequences, and implementation trade-offs |
| [COMPARISON.md](docs/COMPARISON.md) | Capability comparison with Jenkins / GitLab CI / GitHub Actions / Drone |
| [FEATURE-GAPS.md](docs/FEATURE-GAPS.md) | Current capabilities, gaps, and priorities |
| [PERF-AUDIT.md](docs/PERF-AUDIT.md) | Vue 3 frontend performance audit |
| [DOCKER-IMAGE-DESIGN.md](docs/DOCKER-IMAGE-DESIGN.md) | Docker image packaging design (not implemented) |

---

## Security & Operations

- Back up the entire data directory, **especially `data/credentials.key`**. Without it, credentials and password parameters cannot be decrypted.
- Administrator passwords are stored as bcrypt hashes; API tokens are stored as SHA-256 hashes only.
- Use HTTPS in production and restrict access to `/metrics` and `/openapi.json` at the reverse proxy.
- Platform-known secrets are masked in build logs, but secrets generated inside steps or injected externally cannot be detected.
- The platform intentionally does not implement a plugin system, a built-in artifact repository, a Groovy sandbox, or full GitOps/blue-green/canary deployment. See [Jenkins Compatibility](docs/jenkins.md).
