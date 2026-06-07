# Install

`clast` is a bash CLI plus an optional Claude Code plugin. You can install
either or both.

## Requirements

- **bash 4.4+** (associative arrays, `mapfile`). macOS bash 3.2 is not
  supported.
- **`jq`** — JSON manipulation.
- **`coreutils`** — `date`, `stat`, `find`, `cp`, `mv`.
- **`git`** — used for remote detection when registering projects.

Run `make deps-check` from a checkout to verify these are on PATH.

## Pick a channel

### npm

```sh
npm install -g @procrastivity/clast
clast --version
```

Or run once without installing:

```sh
npx -p @procrastivity/clast clast --version
```

### Nix

With flakes enabled:

```sh
nix run github:procrastivity/clast -- whereami
nix profile install github:procrastivity/clast
```

For Home Manager or nix-darwin users, `overlays.default` exposes `pkgs.clast`.

### From a checkout (`install.sh`)

```sh
git clone https://github.com/procrastivity/clast
cd clast
./install.sh ~/.local        # or: ./install.sh /usr/local (default)
```

`make install` wraps the same script. Use `./uninstall.sh ~/.local`
(or `make uninstall` for the default prefix) to remove the installed files.

## Verify

```sh
clast --version
clast whereami
clast doctor
```

`whereami` shows the journal path and current `pwd` resolution. `doctor`
sanity-checks the journal (no errors on a fresh install — the journal directory
just doesn't exist yet, which is fine).

## Next steps

- [First snapshot](./first-snapshot.md) — a five-minute tour of the CLI.
- [Install the plugin](./install-the-plugin.md) — add `/day-wakeup` and
  `/wakeup` to Claude Code.
