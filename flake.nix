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
