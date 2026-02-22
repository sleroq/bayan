{
  description = "Bayan - Duplicate image and video detector bot";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        go = pkgs.go_1_24;
        bayan = pkgs.buildGoModule {
          pname = "bayan";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-/ucjMA675tNtscH5PxNOG+Tyh/4evpcoOTVwBHfukqY=";
          subPackages = [ "src" ];
          ldflags = [ "-s" "-w" ];
          buildInputs = with pkgs; [ sqlite ffmpeg ];
          nativeBuildInputs = with pkgs; [ pkg-config makeWrapper ];
          env.CGO_ENABLED = "1";
          modRoot = ".";
          postInstall = ''
            mv $out/bin/src $out/bin/bayan
            wrapProgram $out/bin/bayan --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.ffmpeg ]}
          '';
          meta = with pkgs.lib; {
            description = "Duplicate image and video detector bot for Telegram";
            homepage = "https://github.com/sleroq/bayan";
            license = licenses.gpl3Only;
            maintainers = [ ];
            platforms = platforms.linux ++ platforms.darwin;
          };
        };
      in {
        packages = { default = bayan; bayan = bayan; };
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [ go gopls go-tools ffmpeg sqlite pkg-config air delve ];
          shellHook = ''
            echo "🤖 Bayan development environment"
            echo "Go version: $(go version)"
            echo "FFmpeg version: $(ffmpeg -version | head -1)"
            echo ""
            echo "Available commands:"
            echo "  go run src/*.go          - Run the bot directly"
            echo "  ./scripts/build.bash     - Build the project"
            echo "  ./scripts/run.bash       - Build and run with env vars"
            echo ""
            echo "Don't forget to:"
            echo "1. Copy scripts/env.bash.example to scripts/env.bash"
            echo "2. Fill in your environment variables"
            
            export CGO_ENABLED=1
          '';
          CGO_ENABLED = "1";
        };
        formatter = pkgs.nixpkgs-fmt;
      });
} 