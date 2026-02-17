# Linear Issue Bridge

Public-facing pages for Linear issues. Tag an issue with the `public` label in Linear and it becomes viewable at `linear.miren.garden/{identifier}`.

## How it works

1. A request comes in for e.g. `/MIR-42`
2. The bridge fetches the issue from Linear's GraphQL API (with a 5-minute cache)
3. If the issue has the `public` label, it renders the full issue page
4. If not, it shows a stub page indicating the issue isn't public yet
5. If the issue doesn't exist, it returns a 404

## Development

```bash
make build    # Build the binary
make test     # Run tests
make lint     # Run linter
```

## License

Apache 2.0 - See [LICENSE](LICENSE) for details.
