# Contributing to mc-conn-poller

Thank you for considering a contribution!

## Getting Started

1. Fork the repository and clone your fork
2. Create a branch for your changes
3. Make your changes and test them
4. Open a pull request against `main`

## Forking this Repository

Grep for `miikkak` to find references to the canonical repository. Typically:

- `renovate.json` extends `github>miikkak/renovate-config`; point it at your own preset or
  drop it
- `.github/workflows/*.yml` call reusable workflows from `miikkak/workflows`; keep them or
  replace them with your own CI
- `SECURITY.md` links to this repository's Security Advisories page

## Development Requirements

- Go (the version pinned in `go.mod`)
- Git
- [pre-commit](https://pre-commit.com/#installation)

Register the hooks with `pre-commit install`. They cover formatting, file hygiene, linting
and secret scanning (gitleaks). All commits must pass them.

## Building and Testing

```shell
make build
go test ./... -race -cover
```

New behavior should come with a test; bug fixes should come with a test that fails before
the fix and passes after.

## Commit Messages

[Conventional Commits](https://www.conventionalcommits.org/):

```text
<type>(<scope>): <description>
```

Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`.

## Design Constraints

- The daemon only ever pings the Healthchecks.io check on success and never calls `/fail`;
  see the README for why. Changes that report failures need a strong justification.
- Platform support is Linux with OpenRC. Please don't add systemd-specific code.

## Pull Request Process

1. Keep PRs focused; don't mix unrelated changes
2. Describe the change and how you tested it
3. Wait for CI (lint, tests, secret scan, security scan, AI review) to pass
4. Address review feedback; a maintainer merges once checks pass

For larger changes, please open an issue first to discuss the approach.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
