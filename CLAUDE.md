# CLAUDE.md

## What is this?

Linear Issue Bridge serves public-facing pages for Linear issues tagged with a `public` label. It fetches issues from Linear's GraphQL API and renders clean HTML pages.

Live at: `linear.miren.garden`

## Build & Test

```bash
make build    # Build the binary
make test     # Run all tests
make lint     # Run golangci-lint
```

## Running Locally

```bash
export LINEAR_OAUTH_CLIENT_ID=<your-client-id>
export LINEAR_OAUTH_CLIENT_SECRET=<your-client-secret>
export LINEAR_TEAM_KEY=MIR
export PORT=8080
go run .
```

Then visit `http://localhost:8080/MIR-42`

## Project Structure

- `main.go` -- Server entrypoint, routing, config
- `internal/linearapi/` -- GraphQL client for Linear API
- `internal/cache/` -- In-memory TTL cache wrapping the Linear client
- `internal/page/` -- HTML template rendering + static assets
- `internal/github/` -- GitHub webhook handling (Phase 2) + MIR-\d+ scanner
- `internal/roadmap/` -- Public roadmap: Linear visibility policy, bucketing, and its two endpoints
- `internal/votes/` -- Roadmap heart counts + rate limiting, backed by Valkey
- `internal/workloadauth/` -- Verifies Miren workload identity tokens on the vote endpoint

## Deployment

```bash
miren deploy
```

Miren remembers env vars and secrets from previous deploys, so you only need to pass `-s` or `-e` flags when setting them for the first time or changing them.

## Configuration

| Env Var | Description |
|---------|-------------|
| `PORT` | Listen port (set automatically by Miren) |
| `LINEAR_OAUTH_CLIENT_ID` | OAuth app client ID (from Linear Settings > API > Applications) |
| `LINEAR_OAUTH_CLIENT_SECRET` | OAuth app client secret |
| `LINEAR_TEAM_KEY` | Issue prefix, e.g. `MIR` |
| `GITHUB_WEBHOOK_SECRET` | Enables `POST /webhook/github`; GitHub HMAC-SHA256 secret |
| `FATHOM_SITE_ID` | Fathom Analytics site ID; omit to disable tracking |
| `BASE_URL` | Public origin for absolute OpenGraph URLs; defaults to `https://linear.miren.garden` |
| `REDIS_URL` / `VALKEY_URL` | Roadmap vote counts and rate limiting; omit and voting is inert |
| `ROADMAP_TRUSTED_ISSUERS` | Comma-separated exact cluster issuer URLs allowed to vote; omit and `POST /api/roadmap/vote` is not mounted |
| `ROADMAP_VOTE_AUDIENCE` | Audience callers must mint tokens for; defaults to `BASE_URL` |
| `ROADMAP_REQUIRE_ORGANIZATION` | Optional `organization_id` the caller must match (defence in depth) |

## Code Style

- Standard Go formatting
- Only add comments when they explain "why", not "what"
- Minimal dependencies -- stdlib where possible, goldmark for markdown
