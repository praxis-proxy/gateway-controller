# Contributing

Thank you for your interest in contributing! Start by
reading the [development conventions]. Submissions that
do not follow the conventions will be rejected.

[development conventions]: https://github.com/praxis-proxy/conventions/blob/main/docs/conventions.md

## Getting Started

1. Fork the repository and clone your fork
2. Build and test: `make build && make test`
3. Run linting locally before pushing: `make lint`

Requirements are listed in the [README.md].

[README.md]: README.md

## Picking Up an Issue

An issue is open for contributors to take once it has
been triaged, meaning it has a milestone and is on a
project board. If you assign yourself to an issue that
hasn't been triaged yet, a bot unassigns you and leaves a
comment pointing to this policy. Ask a maintainer to
triage it first.

## Larger Changes

Features that span multiple PRs, introduce new
architectural patterns, or affect the public interface
go through the [proposal process].

[proposal process]: https://github.com/praxis-proxy/enhancements/blob/main/docs/process.md

## Pull Request Gates

CI enforces reviewability on every PR:

- A maximum added lines count of production code (tests, docs, examples excluded)
- A real description of what and why
- `Signed-off-by` trailer on every commit (`git commit -s`)
- Cryptographically signed commits (GPG or SSH)
- Human authorship: commits authored or signed-off by tools are rejected
- Conventional commit subjects (`type(scope): summary`, ≤72 chars)

See the [development conventions] for details and override labels.

## Code Quality

All pull requests must:

- Pass `make lint` and `make test` locally
- Have meaningful commit messages following conventional format
- Include tests for new functionality
- Update documentation if applicable

## Commits

Sign every commit with your GPG or SSH key:

```console
git commit -s  # Adds Signed-off-by trailer
```

Commits must be cryptographically signed. See GitHub's
[commit signature verification] documentation for setup.

[commit signature verification]: https://docs.github.com/en/authentication/managing-commit-signature-verification

### Commit Message Format

Use conventional commits:

```
type(scope): summary

Optional body describing the change in more detail.
Wrap at 72 characters.

Resolves #issue-number
```

**Types:** build, chore, ci, docs, feat, fix, perf, refactor, test

**Example:**

```
feat(crd): add RateLimit policy field

Allow controllers to configure rate limit parameters
per policy. Adds new fields to the CRD and updates
validation rules.

Resolves #42
```
