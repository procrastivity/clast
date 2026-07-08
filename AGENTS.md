# AGENTS.md

Instructions for coding agents working on the `clast` repo itself.

The canonical way to do work here is by executing step files under [`docs/steps/`](./docs/steps/). See [`docs/build-steps.md`](./docs/build-steps.md) for how steps are structured and executed.

## Conventions

- **Dev shell**: run `direnv allow` to enter the dev shell with `bash`, `jq`, `shellcheck`, `git`, `pre-commit`. If Nix isn't installed, run `make deps-check` to verify the same tools are on PATH some other way.
- **Run `make test` and `make lint` before committing.**
- **Conventional commits**: `feat:`, `fix:`, `docs:`, `chore:`, `test:`, `refactor:`, `ci:`.
- **Don't modify files under `docs/` without explicit user request** — the planning docs are stable references that later steps point at.
- **If a step file (`docs/steps/step-NN-*.md`) is being executed**, follow [`docs/build-steps.md#execution-guidance`](./docs/build-steps.md#execution-guidance): read referenced docs first, verify dependencies, do not improvise scope expansions.

## Linear

This project is tracked in Linear as **clast** (team `beausimensen` / `BDS`). Associate any issues, status updates, or work logged from this repo with that project.

- Project ID: `35626b3c-35c2-4825-9135-b63d073802e7`
- URL: https://linear.app/beausimensen/project/clast-8d5cf5456d6d
