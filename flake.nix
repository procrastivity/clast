{
  description = "clast — dev shell and package for the clast Go CLI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.05";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        # A flake sees rev/shortRev/dirtyRev and lastModified — never tags.
        # So the Nix path stamps the commit where the make path stamps the
        # release tag (C1.7): `nix build` reports `abc1234`, `make build`
        # at a tag reports `v0.1.0`. Both name the same commit, and for
        # `nix build github:procrastivity/clast/v0.1.0` the rev is the tag
        # resolved — a stricter identifier, not a looser one. Do not "fix"
        # this with a VERSION file: the tag is the single source of truth,
        # and a second copy would go stale in silence.
        version = self.shortRev or self.dirtyShortRev or "dev";

        clast = pkgs.buildGoModule {
          pname = "clast";
          inherit version;
          src = ./.;
          # Dependencies are vendored (vendor/ is committed), so there is
          # no fixed-output fetch and no hash to re-pin when go.mod
          # changes — run `go mod vendor` after a dependency change and
          # commit the result instead.
          vendorHash = null;

          env.CGO_ENABLED = 0;

          ldflags = [
            "-X main.version=${version}"
            "-X main.commit=${self.rev or self.dirtyRev or "unknown"}"
            # A fixed epoch, not an oversight: a real build date would make
            # the derivation unreproducible. `commit` above carries the
            # provenance the date would otherwise supply.
            "-X main.date=1970-01-01T00:00:00Z"
          ];

          subPackages = [ "cmd/clast" ];

          postInstall = ''
            mkdir -p $out/share/clast
            cp -r assets $out/share/clast/assets
            # assets.go only exists so `assets/` can embed itself as the
            # binary's last-resort fallback; it is source, not a shipped
            # asset, and must not appear in the installed share tree (C1.6).
            rm -f $out/share/clast/assets/assets.go
          '';

          meta = {
            description = "clast — capture agent sessions, curate them, resurface what mattered";
            license = pkgs.lib.licenses.mit;
            mainProgram = "clast";
          };
        };
      in {
        packages.default = clast;

        devShells.default = pkgs.mkShell {
          name = "clast";
          packages = with pkgs; [
            go
            golangci-lint
            gofumpt
            git-cliff
            gnumake
            pre-commit
            shellcheck
          ];

          # stderr, not stdout: `nix develop --command clast manifest
          # --json | jq` has to work, and anything this hook prints to
          # stdout lands in front of the document (C2.1 in spirit — the
          # tool owns its stdout, and so must its environment).
          shellHook = ''
            echo "clast dev shell — run 'make check' to lint+test, 'make hooks' to install pre-commit." >&2
          '';
        };
      }) // {
      # Downstream flakes consume `pkgs.clast` through this overlay (the
      # bash line exposed the same shape); keep it or their inputs break.
      overlays.default = final: prev: {
        clast = self.packages.${prev.system}.default;
      };
    };
}
