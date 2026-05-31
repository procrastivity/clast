---
step: 01
title: repo-scaffold
depends_on: []
size: small
references:
  - docs/overview.md
  - docs/repo-bootstrap.md#directory-tree
  - docs/repo-bootstrap.md#top-level-file-annotations
  - docs/repo-bootstrap.md#tooling-files
  - docs/repo-bootstrap.md#nix-flake
  - docs/repo-bootstrap.md#dependencies
---

# Step 01: Repo scaffold

## Context

This is the first build step. The repo currently contains only the planning docs under `docs/`. There is no `bin/`, `lib/`, `test/`, or any code yet. The goal of this step is to lay down the bones: directory structure, license, gitignore, conventions files, package metadata, and CI skeleton. No `clast` code is written here.

The planning docs (`overview.md`, `cli-contract.md`, `skill-prompts.md`, `repo-bootstrap.md`, `build-steps.md`) are the source of truth for everything that follows. They should already be committed under `docs/` before this step starts. If they are not, stop and ask the user to commit them first — every later step depends on them being addressable via `@docs/...`.

## Goal

Create the full directory structure and all non-code top-level files so subsequent steps have a stable scaffold to build into.

## References

Read before starting:

- `docs/overview.md` — full context for the project.
- `docs/repo-bootstrap.md#directory-tree` — the canonical directory layout. Mirror it exactly.
- `docs/repo-bootstrap.md#top-level-file-annotations` — file-by-file content sketches for `Makefile`, `package.json`, `.gitignore`, `.editorconfig`, `.envrc`, etc.
- `docs/repo-bootstrap.md#tooling-files` — content for `cliff.toml`, `.pre-commit-config.yaml`.

## Tasks

1. **Create the directory tree** per `docs/repo-bootstrap.md#directory-tree`. Empty directories that will hold code in later steps should contain a `.gitkeep` so they're tracked. Specifically create:
   - `bin/`
   - `lib/clast/clast-subcommands/`
   - `test/fixtures/`
   - `examples/cron/`
   - `examples/config/`
   - `examples/workflows/`
   - `hooks/`
   - `.claude-plugin/skills/`
   - `.github/workflows/`
   - `docs/steps/` (this file lives here)

2. **Write `LICENSE`** — MIT, with copyright `2026 Beau (procrastivity)`. Use the standard SPDX MIT text.

3. **Write `.gitignore`** per `docs/repo-bootstrap.md#gitignore`.

4. **Write `.gitattributes`**:
   ```
   * text=auto eol=lf
   *.sh text eol=lf
   *.bash text eol=lf
   bin/clast text eol=lf
   ```

5. **Write `.editorconfig`** per `docs/repo-bootstrap.md#editorconfig`.

6. **Write `.envrc`** per `docs/repo-bootstrap.md#envrc`.

7. **Write `flake.nix`** — **dev shell only at this stage.** Package + overlay outputs land in step 15. The dev shell provides every runtime dependency `clast` needs plus the dev tooling, so contributors can `direnv allow` and have everything immediately. Structure:

   ```nix
   {
     description = "clast — Claude Code session journal (dev shell)";

     inputs = {
       nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
       flake-utils.url = "github:numtide/flake-utils";
     };

     outputs = { self, nixpkgs, flake-utils }:
       flake-utils.lib.eachDefaultSystem (system:
         let pkgs = nixpkgs.legacyPackages.${system}; in {
           devShells.default = pkgs.mkShell {
             buildInputs = with pkgs; [
               bash          # 5.0+ for associative arrays + mapfile
               jq            # JSON manipulation (required runtime dep)
               coreutils     # date, stat, find, cp, mv
               git           # remote detection
               shellcheck    # linting
               pre-commit    # hook runner
             ];

             shellHook = ''
               echo "clast dev shell — bash $(bash --version | head -1 | grep -oE '[0-9]+\.[0-9]+')"
             '';
           };

           # packages.default and overlays.default land in step 15.
         }
       );
   }
   ```

   Commit `flake.nix`. Do not commit `flake.lock` yet — let it generate on first `nix flake update` or `direnv allow`.

8. **Run `direnv allow`** (or `nix develop` if Nix is installed but direnv isn't) to lock the flake and verify the shell loads. This generates `flake.lock`; commit that too. If Nix isn't installed on the executing machine, document the fallback in the next task and skip this one.

9. **Write `Makefile`** per `docs/repo-bootstrap.md#makefile`, **plus** add a `deps-check` target for users not using Nix:

   ```makefile
   deps-check:
   	@for tool in bash jq git shellcheck; do \
   		if ! command -v $$tool >/dev/null 2>&1; then \
   			echo "missing: $$tool" >&2; \
   			exit 1; \
   		fi; \
   	done
   	@echo "all required tools present"
   ```

   The `test` and `lint` targets will fail until later steps land — that's fine, the targets exist.

10. **Write `package.json`** per `docs/repo-bootstrap.md#npm-procrastivityclast`. Version `0.1.0`. The `bin` entry pointing to `bin/clast` is fine even though the file doesn't exist yet — `npm install -g` will error appropriately if anyone tries before step 03.

11. **Write `cliff.toml`** per `docs/repo-bootstrap.md#clifftoml`. Use the standard conventional-commits template.

12. **Write `.pre-commit-config.yaml`** per `docs/repo-bootstrap.md#pre-commit-configyaml`.

13. **Write `README.md`** — a stub for now. Sections:
    - Title + one-line description (pull from `package.json`).
    - "Status" callout: `🚧 Pre-release. APIs may change before v1.0.`
    - "What it does" — three bullets summarizing the CLI, the plugin, and the SessionStart hook.
    - "Development" — two short paragraphs:
      - **With Nix (recommended)**: `direnv allow` (or `nix develop`) loads the dev shell with all dependencies.
      - **Without Nix**: install `bash 5+`, `jq`, `git`, `shellcheck` manually; run `make deps-check` to verify.
    - "Documentation" — links to `docs/overview.md` and the other reference docs.
    - "License" — MIT.
    Keep it short. A real README lands in step 18.

14. **Write `CHANGELOG.md`** — just the header:
    ```markdown
    # Changelog

    All notable changes to this project will be documented here.
    Format follows [Keep a Changelog](https://keepachangelog.com/),
    generated via [git-cliff](https://git-cliff.org/).

    ## [Unreleased]
    ```

15. **Write `AGENTS.md`** — instructions for coding agents working on this repo. Reference `docs/build-steps.md` as the canonical way to do work. Include guidance:
    - "**Dev shell**: run `direnv allow` to enter the dev shell with `bash`, `jq`, `shellcheck`, `git`, `pre-commit`. If Nix isn't installed, run `make deps-check` to verify the same tools are on PATH some other way."
    - "Run `make test` and `make lint` before committing."
    - "Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`, `test:`)."
    - "Don't modify files under `docs/` without explicit user request — the planning docs are stable references."
    - "If a step file (`docs/steps/step-NN-*.md`) is being executed, follow `docs/build-steps.md#execution-guidance`: read referenced docs first, verify dependencies, do not improvise scope expansions."

16. **Write `CLAUDE.md`** — a one-line file that points at AGENTS.md:
    ```markdown
    See [AGENTS.md](./AGENTS.md).
    ```

17. **Write CI workflow skeletons** under `.github/workflows/`:
    - `test.yml` — runs shellcheck and `test/test-clast.sh`. Both will be no-ops in step 01 (since the targets don't exist yet); the workflow should detect missing `test/test-clast.sh` and exit 0 with a "no tests yet" message, so it doesn't fail on the first push. **Uses the dev shell**: install Nix via `cachix/install-nix-action@v25`, then run commands inside `nix develop -c …` so jq/shellcheck/bash are available.
    - `nix.yml` — runs `nix flake check`. With the dev-shell-only flake from task 7, this should pass (a dev shell is checkable). The `packages.default` check lands in step 15 when the package output exists.
    - `release.yml` — stub triggered on `v*` tag, no jobs yet beyond a placeholder echo. Will be filled in step 17.

18. **Verify the structure**: run `find . -type f | sort` and confirm the output matches what `docs/repo-bootstrap.md#directory-tree` describes (modulo files coming in later steps).

## Acceptance criteria

- `tree -a -I '.git' .` (or `find . -type f | sort`) shows the expected directory layout. All empty directories that will hold code have `.gitkeep`.
- `LICENSE` is MIT and contains a copyright line for the current year.
- `.gitignore`, `.gitattributes`, `.editorconfig`, `.envrc`, `Makefile`, `cliff.toml`, `.pre-commit-config.yaml` all exist and match the content sketches in `docs/repo-bootstrap.md`.
- `flake.nix` exists with a working `devShells.default`. Running `nix develop -c bash -c 'command -v jq && command -v shellcheck && command -v git'` succeeds with all three commands found.
- `flake.lock` exists and is committed.
- `make deps-check` exits 0 (verifies dev shell or PATH has all required tools).
- `package.json` is valid JSON, has `name`, `version`, `bin`, `files`, `scripts`, `license`, `engines`. `jq . package.json` validates.
- `README.md` exists, is under 60 lines, has Development section with both Nix and non-Nix paths.
- `CHANGELOG.md` exists with the unreleased header.
- `AGENTS.md` exists with the five-bullet conventions (including the dev-shell guidance).
- `CLAUDE.md` exists, one line, points to `AGENTS.md`.
- `.github/workflows/test.yml` and `nix.yml` exist and pass when pushed.
- `git status` is clean after committing.
- `git log --oneline` shows one or two commits with conventional-commits messages (one for the scaffold, optionally one for `flake.lock` if you committed it separately).

## Out of scope

Do not do any of the following in step 01; they belong to later steps:

- Do not write any `bash` code in `bin/clast` or under `lib/clast/`. (Step 02 starts the libs; step 03 starts the dispatcher.)
- Do not write tests. (Step 02 introduces `test/helpers.sh`; step 02+ adds test files.)
- Do not write the `packages.default` or `overlays.default` outputs of the flake — **only `devShells.default` belongs in step 01.** The package output is step 15.
- Do not write `install.sh` or `uninstall.sh`. (Step 14.)
- Do not write the plugin's `plugin.json`, hook, or skills. (Steps 11–13.)
- Do not write `Dockerfile`. (Skipped for v1 entirely.)
- Do not create test fixture trees yet beyond the `.gitkeep`. (Step 02 introduces `simple/` and starter fixtures.)
- Do not configure git-cliff to actually generate `CHANGELOG.md`. The config exists; the generation runs at release time (step 19).

If you find yourself reaching for any of the above to "complete" the scaffold, stop. The scaffold is *intentionally incomplete*; later steps fill it in.

## Verification

Run all of these:

```bash
# Structure check
find . -type f -not -path './.git/*' | sort

# JSON validity
jq . package.json

# Dev shell smoke test (Nix path)
nix develop -c bash -c 'command -v bash && command -v jq && command -v shellcheck && command -v git'

# Or, without Nix, verify deps are on PATH some other way
make deps-check

# Flake check
nix flake check

# Markdown link check (manual eyeball — no tooling required)
grep -r 'docs/' README.md AGENTS.md

# CI smoke test (push to a feature branch and confirm test.yml + nix.yml pass)

# git status clean
git status --porcelain
# (should output nothing after commit)
```

## Notes for the implementer

- **Don't overthink the README.** It's a stub. The user knows.
- **The `test.yml` "no tests yet" behavior** matters: the workflow must succeed on push, not fail. Without this, every later PR shows a red X until step 02 lands tests. A simple `if [ ! -f test/test-clast.sh ]; then echo "no tests yet" && exit 0; fi` at the top of the test step is enough.
- **The flake is intentionally staged across two steps.** Step 01 ships only `devShells.default` so contributors have a working dev environment from the first commit. The `packages.default` and `overlays.default` outputs land in step 15 when there's actually a built binary to package. Don't be tempted to "complete" the flake in step 01 — it would require either fake content or empty stubs that fail `nix build`.
- **CI uses Nix for the dev shell.** `test.yml` should install Nix and run all commands inside `nix develop -c …` rather than relying on a pre-baked image with jq/shellcheck. This keeps the dev environment and CI environment identical — what passes locally passes in CI, full stop. Adds ~30s of cold-start time per workflow run; well worth it for environmental fidelity.
- **`flake.lock` should be committed** after first generation. It pins nixpkgs to a specific commit so the dev shell is reproducible. Without it, contributors get different versions of tools and CI may behave differently than local.
- **`.gitkeep` files** are the standard way to track empty dirs in git. Don't use `.keep` or `placeholder.txt` — `.gitkeep` is universally recognized.
- **Commit message**: scaffold + flake can be a single commit (`chore: scaffold repo and dev shell`) or split (one for files, one for flake + lock). Conventional-commits either way.
- **The package.json `bin` field** referencing a not-yet-existent `bin/clast` is intentional. It documents the eventual install target; npm doesn't validate that the file exists at `npm install` time the way some package managers do for symlinks.
- **`AGENTS.md` vs `CLAUDE.md`**: AGENTS.md is the canonical file; CLAUDE.md just redirects to it. This matches Beau's existing convention (visible in `xcind`). Don't duplicate content.
- **Pre-commit hooks** won't run in CI yet (step 17 wires them up). The config exists so contributors who `pre-commit install` locally get the checks. Don't worry about the hook actually triggering until the test workflow uses it.
- **If Nix isn't installed on the executing machine**, the agent should: (a) skip the `direnv allow` task, (b) skip the flake-check verification, (c) commit the `flake.nix` anyway (it's still correct), (d) leave a note in the commit body that `flake.lock` was not generated locally. CI will generate it on first run.
