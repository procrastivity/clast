# `clast` — Repo Bootstrap

> Reference doc. New to clast? Start with
> [`explanation/what-is-clast.md`](../explanation/what-is-clast.md). This doc
> specs the repo layout, distribution channels, packaging, and CI for `clast`.

Models the hybrid of [direnv-session-loader](https://github.com/procrastivity/direnv-session-loader) (plugin scaffolding + Nix flake) and [xcind](https://github.com/scinddev/xcind) (CLI bins + lib + multi-channel distribution).

---

## Directory tree

```
clast/
├── .claude-plugin/
│   └── plugin.json                 # plugin manifest (ONLY this lives here)
├── skills/                         # sibling of .claude-plugin/, NOT inside it
│   ├── day-wakeup/
│   │   └── SKILL.md
│   ├── wakeup/
│   │   └── SKILL.md
│   └── breadcrumb/                 # OPTIONAL v1.1; can omit in v1
│       └── SKILL.md
├── hooks/
│   ├── hooks.json
│   └── snapshot.sh                 # SessionStart → backgrounds `clast-plumbing snapshot`
├── bin/
│   ├── clast                       # porcelain (LLM-aware): `clast wake` / `clast brief`
│   └── clast-plumbing              # plumbing dispatcher: sources libs, dispatches subcommand
├── lib/clast/
│   ├── clast-lib.bash              # shared: I/O, JSON, date math, path resolution
│   ├── clast-decode-lib.bash       # segment ↔ path decoder with collision logic
│   ├── clast-manifest-lib.bash     # manifest read/append/lookup/dedupe
│   ├── clast-registry-lib.bash     # registry read/write/resolve, alias handling
│   ├── clast-dismissed-lib.bash    # dismissed-session tracking (.dismissed.jsonl)
│   ├── clast-porcelain-lib.bash    # porcelain-only helpers (LLM call, prompts, preflight)
│   ├── prompts/                    # shared LLM prompt templates (plugin skills + porcelain)
│   │   ├── day-wakeup-draft-system.md
│   │   ├── day-wakeup-draft-user.md
│   │   ├── brief-system.md
│   │   └── brief-user.md
│   ├── clast-porcelain-subcommands/
│   │   ├── wake.bash               # `clast wake`
│   │   └── brief.bash              # `clast brief`
│   └── clast-subcommands/
│       ├── snapshot.bash
│       ├── projects.bash
│       ├── sessions.bash
│       ├── show.bash
│       ├── entries.bash
│       ├── breadcrumb.bash
│       ├── registry.bash
│       ├── stats.bash
│       ├── doctor.bash
│       └── whereami.bash
├── test/
│   ├── test-clast.sh               # core test runner
│   ├── test-decode.sh
│   ├── test-snapshot.sh
│   ├── test-registry.sh
│   ├── test-manifest.sh
│   ├── test-entries.sh
│   ├── test-breadcrumb.sh
│   ├── test-doctor.sh
│   ├── helpers.sh                  # test fixtures/setup
│   └── fixtures/                   # synthetic ~/.claude/projects/ trees
│       ├── simple/                 # one project, two sessions
│       ├── multi-project/          # three projects, varied activity
│       ├── ambiguous-decode/       # paths with literal dashes (collision test)
│       ├── worktree/               # one project, multiple worktree segments
│       ├── corrupt-manifest/       # for doctor tests
│       └── empty/                  # no projects at all
├── examples/
│   ├── cron/
│   │   ├── crontab.sample
│   │   ├── clast-snapshot.service
│   │   └── clast-snapshot.timer
│   ├── config/
│   │   └── config.toml.sample
│   └── workflows/
│       └── morning-briefing.md     # narrative example of a /day-wakeup transcript
├── docs/                           # Diátaxis layout (see docs/README.md)
│   ├── README.md                   # section index
│   ├── explanation/                # concepts: what-is-clast, architecture, data-model, conventions
│   ├── getting-started/            # install, first-snapshot, install-the-plugin
│   ├── guides/                     # task how-tos (curate, breadcrumbs, automate, repair, …)
│   ├── reference/                  # specs: cli, plugin, config, entry-frontmatter, repo-bootstrap, releasing
│   ├── build-steps.md              # step generation meta-doc (historical)
│   └── steps/                      # self-executing build prompts (historical)
│       ├── step-01-repo-scaffold.md
│       ├── step-02-core-libs.md
│       └── …
├── .github/
│   └── workflows/
│       ├── test.yml                # CI: shellcheck + tests on multiple bash versions
│       ├── release.yml             # build & publish on tag
│       └── nix.yml                 # nix flake check
├── flake.nix
├── flake.lock
├── package.json                    # for npm distribution
├── install.sh
├── uninstall.sh
├── Makefile
├── Dockerfile                      # OPTIONAL, skip in v1
├── cliff.toml                      # changelog generation config
├── .pre-commit-config.yaml
├── .envrc                          # direnv-friendly
├── .gitignore
├── .gitattributes
├── .editorconfig
├── README.md
├── CHANGELOG.md
├── LICENSE                         # MIT
├── AGENTS.md                       # for coding agents working on clast itself
└── CLAUDE.md                       # ditto
```

---

## Top-level file annotations

### `bin/clast` (porcelain) and `bin/clast-plumbing` (plumbing)

Two thin dispatcher scripts. `clast-plumbing` is the deterministic core
(snapshot/sessions/entries/…); `clast` is the LLM-aware porcelain
(`wake`/`brief`). Both source `lib/clast/` libs and dispatch to a
subcommand file by name.

```bash
#!/usr/bin/env bash
# clast-plumbing — main dispatcher
set -euo pipefail

CLAST_LIB="${CLAST_LIB:-$(dirname "$(realpath "$0")")/../lib/clast}"

# shellcheck source=lib/clast/clast-lib.bash
source "$CLAST_LIB/clast-lib.bash"

# Subcommand dispatch
case "${1:-}" in
  snapshot|projects|sessions|show|entries|breadcrumb|registry|stats|doctor|whereami)
    cmd="$1"; shift
    source "$CLAST_LIB/clast-subcommands/$cmd.bash"
    "clast_cmd_$cmd" "$@"
    ;;
  -h|--help|help|"")
    clast_usage; exit 0 ;;
  --version)
    echo "clast-plumbing $(clast_version)"; exit 0 ;;
  *)
    echo "clast-plumbing: unknown subcommand '$1'" >&2
    clast_usage >&2; exit 2 ;;
esac
```

The porcelain has the same shape but dispatches `wake` / `brief` from
`lib/clast/clast-porcelain-subcommands/<name>.bash` and errors on any
unknown verb — it does NOT proxy to plumbing.

All real logic lives in `lib/clast/`.

### `lib/clast/clast-lib.bash`

Common helpers, sourced by the dispatcher (and transitively by subcommands). Functions:

- `clast_journal_dir` — returns `$CLAST_JOURNAL_DIR` or default `~/.claude/journal`
- `clast_projects_dir` — same for `~/.claude/projects`
- `clast_today` — local date, respecting `day_cutoff`
- `clast_parse_date <input>` — handle ISO / `today` / `yesterday` / `-1d` etc.
- `clast_log_info`, `clast_log_warn`, `clast_log_error` — stderr logging
- `clast_json_get`, `clast_json_set` — wrappers around `jq` (declared dependency)
- `clast_atomic_write <path> <content>` — write via temp + rename

### `lib/clast/clast-decode-lib.bash`

The dash-substitution decoder. Has to handle:
- Straightforward: `-home-beau-code-xesapps` → `/home/beau/code/xesapps`
- Windows/WSL2: `C--Users-Beast-Documents-GitHub-HydraMCP` → `C:/Users/Beast/Documents/GitHub/HydraMCP`
- Ambiguous: `-home-beau-code-xesapps` could decode to `/home/beau/code/xesapps`, `/home/beau-code/xesapps`, `/home-beau/code/xesapps`. Resolution: try each candidate with `test -d`; if multiple match, consult `sessions-index.json`'s `projectPath` field, then `git rev-parse --show-toplevel` on each. If still ambiguous, surface to user.

Functions:
- `clast_decode_segment <segment>` → prints path or returns 1
- `clast_encode_path <path>` → prints segment (inverse, useful for testing)
- `clast_decode_candidates <segment>` → prints all candidate paths

### `lib/clast/clast-manifest-lib.bash`

Manifest = `~/.claude/journal/.manifest.jsonl`. Append-only.

Functions:
- `clast_manifest_append <session-id> <source-path> <snapshot-path> <source-mtime> <source-size> <day-bucket>`
- `clast_manifest_lookup <session-id>` → prints the most recent line or returns 1
- `clast_manifest_has_capture <session-id> <source-mtime>` → exit 0 if this exact capture exists
- `clast_manifest_iterate <filter>` → stream lines matching a jq filter
- `clast_manifest_rebuild_from_disk` → for `doctor --fix`

### `lib/clast/clast-registry-lib.bash`

Registry = `~/.claude/journal/projects.json` (JSONL despite the name).

Functions:
- `clast_registry_resolve <path-or-segment>` → prints slug
- `clast_registry_add <path> [--slug ...] [--remote ...]`
- `clast_registry_list_json` → prints registry as JSON array
- `clast_registry_match_remote <remote>` → returns existing slug if remote matches

### `lib/clast/clast-subcommands/<name>.bash`

Each file defines a single function `clast_cmd_<name>` that the dispatcher calls. The function handles argument parsing, calls into libs, prints output.

---

## Plugin files

### `.claude-plugin/plugin.json`

```json
{
  "name": "clast",
  "version": "0.1.0",
  "description": "Capture, curate, and surface Claude Code session history across all your projects.",
  "homepage": "https://github.com/procrastivity/clast",
  "author": {
    "name": "Beau",
    "url": "https://github.com/procrastivity"
  },
  "license": "MIT"
}
```

Per the direnv-session-loader reference, only `name` is strictly required. Other fields are good-citizen metadata.

### `hooks/hooks.json`

```json
{
  "hooks": [
    {
      "event": "SessionStart",
      "command": "${CLAUDE_PLUGIN_ROOT}/hooks/snapshot.sh"
    }
  ]
}
```

### `skills/*/SKILL.md`

Full content in [`plugin.md`](./plugin.md).

---

## Distribution

Match xcind's pattern. Multi-channel from day one.

### npm (`@procrastivity/clast`)

`package.json` outline:

```json
{
  "name": "@procrastivity/clast",
  "version": "0.1.0",
  "description": "Capture, curate, and surface Claude Code session history across all your projects.",
  "bin": {
    "clast": "bin/clast",
    "clast-plumbing": "bin/clast-plumbing"
  },
  "files": [
    "bin/",
    "lib/",
    ".claude-plugin/",
    "skills/",
    "hooks/",
    "examples/",
    "README.md",
    "LICENSE"
  ],
  "scripts": {
    "test": "test/test-clast.sh",
    "lint": "shellcheck bin/clast bin/clast-plumbing lib/clast/**/*.bash test/*.sh"
  },
  "repository": {
    "type": "git",
    "url": "https://github.com/procrastivity/clast.git"
  },
  "keywords": ["claude-code", "session", "journal", "history", "cli"],
  "author": "Beau",
  "license": "MIT",
  "engines": {
    "node": ">=18"
  }
}
```

`npm install -g @procrastivity/clast` should install `clast` to PATH and the plugin to a discoverable location. The `bin` field handles the binary; the plugin discovery depends on how Claude Code's plugin marketplace ingests npm packages — verify against current Claude Code docs at implementation time.

### Nix flake

**Staging note:** `flake.nix` is built in two stages across two steps:
- **Step 01** ships only `devShells.default` so contributors have a working dev environment from the first commit.
- **Step 15** adds `packages.default` and `overlays.default` for distribution.

The structure below is the final shape (after step 15). For the step-01-only version, omit `packages.default` and `overlays.default` and keep just the `devShells.default` block.

`flake.nix` outline (modeled on xcind's):

```nix
{
  description = "clast — Claude Code session journal";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in {
        packages.default = pkgs.stdenv.mkDerivation {
          pname = "clast";
          version = "0.1.0";
          src = ./.;

          nativeBuildInputs = [ pkgs.makeWrapper ];
          buildInputs = [ pkgs.bash pkgs.jq pkgs.coreutils pkgs.git ];

          installPhase = ''
            mkdir -p $out/bin $out/lib/clast $out/share/clast
            cp -r lib/clast/* $out/lib/clast/
            cp -r .claude-plugin $out/share/clast/
            cp -r skills $out/share/clast/
            cp -r hooks $out/share/clast/
            cp -r examples $out/share/clast/
            install -m755 bin/clast $out/bin/clast
            wrapProgram $out/bin/clast \
              --set CLAST_LIB "$out/lib/clast" \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.jq pkgs.coreutils pkgs.git ]}
          '';
        };

        packages.clast = self.packages.${system}.default;

        devShells.default = pkgs.mkShell {
          buildInputs = [ pkgs.bash pkgs.jq pkgs.shellcheck pkgs.bats ];
        };
      }
    ) // {
      overlays.default = final: prev: {
        clast = self.packages.${prev.system}.default;
      };
    };
}
```

`nix run github:procrastivity/clast` should work for any system. Beau's Home Manager flake adds an overlay reference; `pkgs.clast` becomes available.

### `install.sh` / `uninstall.sh`

Mirror xcind's pattern:

```bash
#!/usr/bin/env bash
# install.sh — install clast to PREFIX (default /usr/local)
set -euo pipefail

PREFIX="${1:-/usr/local}"
SRC="$(cd "$(dirname "$0")" && pwd)"

mkdir -p "$PREFIX/bin" "$PREFIX/lib/clast" "$PREFIX/share/clast"

install -m755 "$SRC/bin/clast" "$PREFIX/bin/clast"
cp -r "$SRC/lib/clast"/* "$PREFIX/lib/clast/"
cp -r "$SRC/.claude-plugin" "$PREFIX/share/clast/"
cp -r "$SRC/skills" "$PREFIX/share/clast/"
cp -r "$SRC/hooks" "$PREFIX/share/clast/"
cp -r "$SRC/examples" "$PREFIX/share/clast/"

echo "Installed clast to $PREFIX"
echo "  Binary: $PREFIX/bin/clast"
echo "  Plugin: $PREFIX/share/clast/.claude-plugin"
echo ""
echo "Add the plugin via:"
echo "  claude plugin install $PREFIX/share/clast"
```

`uninstall.sh` is the inverse.

### Claude Code plugin marketplace

For v1, users install the plugin from a local checkout or from the installed npm
package path. A centralized marketplace listing is a separate distribution
channel and is deliberately not part of the v1 release.

---

## CI

The actual workflows are:

- `.github/workflows/test.yml`: runs `make lint`, `make test`,
  `make check-version-sync`, and `make npm-pack-check` on pushes and pull
  requests.
- `.github/workflows/nix.yml`: runs `make nix-smoke`, `nix flake check`, and
  `nix build .#default` on pushes and pull requests.
- `.github/workflows/release.yml`: runs on `v*` tags, re-runs every gate,
  verifies the tag matches `package.json`, builds Nix, packs npm, publishes to
  npm with provenance, and creates a GitHub Release.

See [`releasing.md`](./releasing.md) for the release procedure and failure
recovery runbook.

---

## Tooling files

### `cliff.toml`

Use git-cliff for changelog generation (matches both reference projects). Standard config; `examples/changelog` from the cliff repo is a fine starting point. Convention commits.

### `.pre-commit-config.yaml`

```yaml
repos:
  - repo: https://github.com/koalaman/shellcheck-precommit
    rev: v0.9.0
    hooks:
      - id: shellcheck
        files: '\.(sh|bash)$'
        # plus bin/clast which is bash with no extension:
        # add a custom hook if needed
  - repo: https://github.com/pre-commit/pre-commit-hooks
    rev: v4.5.0
    hooks:
      - id: trailing-whitespace
      - id: end-of-file-fixer
      - id: check-yaml
      - id: check-json
      - id: check-added-large-files
```

### `Makefile`

```makefile
.PHONY: test lint install uninstall release

test:
	./test/test-clast.sh

lint:
	shellcheck bin/clast lib/clast/**/*.bash test/*.sh

install:
	./install.sh

uninstall:
	./uninstall.sh

clean:
	rm -rf .test-tmp

release:
	./contrib/release
```

### `.envrc`

```bash
# direnv: clast development environment
use flake
PATH_add bin
```

### `.gitignore`

```gitignore
# Local config / runtime
.test-tmp/
result
result-*
.direnv/

# Editor
*.swp
.idea/
.vscode/

# OS
.DS_Store
Thumbs.db
```

### `.editorconfig`

```ini
root = true

[*]
indent_style = space
indent_size = 2
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true

[Makefile]
indent_style = tab
```

---

## Test strategy

### Unit tests

Each lib gets a focused test file (`test/test-<libname>.sh`). Tests are bash scripts using a minimal harness (or [bats](https://github.com/bats-core/bats-core); both reference projects use bash + simple assertions).

### Integration tests

`test/test-clast.sh` runs each subcommand end-to-end against fixture trees in `test/fixtures/`. Each fixture is a self-contained synthetic `~/.claude/projects/` tree representing a specific scenario:

- `simple/` — one project, two sessions, ideal case
- `multi-project/` — three projects, varied activity
- `ambiguous-decode/` — paths with literal dashes for collision testing
- `worktree/` — one project, multiple worktree segments
- `corrupt-manifest/` — for `clast-plumbing doctor` tests
- `empty/` — no projects at all (edge case)

Tests set `CLAST_JOURNAL_DIR=.test-tmp/journal-$$` and `CLAST_PROJECTS_DIR=fixtures/<scenario>` so they don't touch the real `~/.claude/`.

### CI matrix

- bash 5.0, 5.1, 5.2 (xcind tests against this matrix; same approach here).
- Linux for primary; macOS bash 3.2 is **not** supported (bash 4+ features used).

---

## Dependencies

Runtime:
- bash 4.4+ (associative arrays, mapfile)
- `jq` (JSON manipulation)
- `coreutils` (date, stat, find, cp, mv)
- `git` (for remote detection)

Development:
- `shellcheck`
- `pre-commit`
- Optionally `bats` for nicer test output

All declared in `flake.nix`'s `devShells.default`.

---

## Open decisions specific to bootstrap

| # | Question | Default |
|---|---|---|
| 1 | Single dispatcher (`clast-plumbing snapshot`) vs separate bins (`clast-snapshot`) | **Single dispatcher** (resolved) |
| 2 | Single repo for CLI + plugin | **Single repo** (resolved) |
| 3 | Test framework: handwritten bash or `bats` | **Handwritten bash** for v1 (matches both reference projects) |
| 4 | Configuration format | **TOML** (`~/.config/clast/config.toml`), env vars override |
| 5 | License | **MIT** (matches references) |
| 6 | Initial version | **0.1.0** — pre-1.0 to signal API may change |
| 7 | Docker image | **Skip in v1** — less applicable than for xcind |
| 8 | Conventional commits + git-cliff | **Yes** — matches both references; enables automated changelog |
| 9 | First-class Windows support | **Defer.** WSL2 works (Beau's primary env); native Windows is future work |
| 10 | macOS bash 3.2 support | **No.** Requires bash 4.4+ for associative arrays and mapfile |
