{
  description = "clast — Claude Code session journal";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let pkgs = nixpkgs.legacyPackages.${system}; in {
        packages.default = pkgs.stdenv.mkDerivation {
          pname = "clast";
          # Bump in lockstep with package.json.
          version = "0.0.3";
          src = ./.;

          nativeBuildInputs = [ pkgs.makeWrapper ];
          buildInputs = [
            pkgs.bash
            pkgs.curl
            pkgs.jq
            pkgs.coreutils
            pkgs.findutils
            pkgs.gawk
            pkgs.git
            pkgs.gnugrep
            pkgs.inetutils
          ];
          dontBuild = true;

          installPhase = ''
            mkdir -p \
              $out/bin \
              $out/lib/clast \
              $out/share/clast/.claude-plugin \
              $out/share/clast/hooks \
              $out/share/clast/examples

            cp -R lib/clast/. $out/lib/clast/
            cp -R .claude-plugin/. $out/share/clast/.claude-plugin/
            cp -R hooks/. $out/share/clast/hooks/
            # Belt-and-suspenders: ensure the SessionStart hook is executable.
            chmod +x $out/share/clast/hooks/snapshot.sh
            cp -R examples/. $out/share/clast/examples/
            install -m644 package.json $out/lib/clast/package.json
            install -m644 README.md $out/share/clast/README.md
            install -m644 LICENSE $out/share/clast/LICENSE
            install -m755 bin/clast $out/bin/clast
            wrapProgram $out/bin/clast \
              --set CLAST_LIB "$out/lib/clast" \
              --prefix PATH : ${pkgs.lib.makeBinPath [
                pkgs.jq
                pkgs.coreutils
                pkgs.findutils
                pkgs.gawk
                pkgs.git
                pkgs.gnugrep
                pkgs.inetutils
              ]}

            # Standalone LLM helpers. They call clast/curl/jq directly and
            # resolve prompts via dirname($0)/../lib/clast/prompts, so they need
            # $out/bin (for clast) and the toolset on PATH, but not CLAST_LIB.
            install -m755 bin/clast-wake $out/bin/clast-wake
            install -m755 bin/clast-brief $out/bin/clast-brief
            for helper in clast-wake clast-brief; do
              wrapProgram $out/bin/$helper \
                --prefix PATH : "$out/bin" \
                --prefix PATH : ${pkgs.lib.makeBinPath [
                  pkgs.curl
                  pkgs.jq
                  pkgs.coreutils
                  pkgs.findutils
                  pkgs.gawk
                  pkgs.git
                  pkgs.gnugrep
                  pkgs.inetutils
                ]}
            done
          '';
        };

        packages.clast = self.packages.${system}.default;

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            bash          # 5.0+ for associative arrays + mapfile
            jq            # JSON manipulation (required runtime dep)
            coreutils     # date, stat, find, cp, mv
            git           # remote detection
            shellcheck    # linting
            pre-commit    # hook runner
            git-cliff     # changelog generation for contrib/release
          ];

          shellHook = ''
            echo "clast dev shell — bash $(bash --version | head -1 | grep -oE '[0-9]+\.[0-9]+')"
          '';
        };
      }
    ) // {
      overlays.default = final: prev: {
        clast = self.packages.${prev.system}.default;
      };
    };
}
