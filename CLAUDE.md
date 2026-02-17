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
export LINEAR_API_KEY=<your-key>
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

## Deployment

```bash
miren deploy -s LINEAR_API_KEY=<key> -e LINEAR_TEAM_KEY=MIR
```

## Configuration

| Env Var | Description |
|---------|-------------|
| `PORT` | Listen port (set automatically by Miren) |
| `LINEAR_API_KEY` | Linear API key for GraphQL queries |
| `LINEAR_TEAM_KEY` | Issue prefix, e.g. `MIR` |
| `GITHUB_WEBHOOK_SECRET` | Enables `POST /webhook/github`; GitHub HMAC-SHA256 secret |

## Code Style

- Standard Go formatting
- Only add comments when they explain "why", not "what"
- Minimal dependencies -- stdlib where possible, goldmark for markdown
