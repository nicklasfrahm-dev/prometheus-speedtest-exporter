# Working in this repo

- Run `make test && make lint` before finishing any change.
- Tests use [`stretchr/testify`](https://github.com/stretchr/testify)'s `assert` (and `require` for fatal preconditions) instead of hand-rolled `if got != want { t.Errorf(...) }` checks.
- `golangci-lint` is configured in `.golangci.yml` with `linters.default: all` (minus deprecated linters, `depguard`, and `exhaustruct`). The pinned version lives in `Makefile`'s `GOLANGCI_LINT_VERSION`; CI reads that value at runtime rather than pinning it a second time, so the Makefile is the single source of truth.
- PR titles must follow Conventional Commits, restricted to `feat`/`fix` types only, with an optional alphabetic/hyphen-only scope and a lowercase description (enforced by the `check-pr-title` CI job).
