{
  description = "Ichor toolchain";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          # shortcut: prebuilt go.dev binary instead of the nixpkgs build, because nixpkgs-unstable still has go_1_27 at 1.27.3
          # (GO-2026-6859 is fixed in 1.27.4, nixpkgs#586269); drop it and use pkgs.go_1_27 once nixpkgs carries 1.27.4.
          goArch = { x86_64-linux = "amd64"; aarch64-linux = "arm64"; }.${system};
          goHash = {
            x86_64-linux = "sha256-ciZzbCM5cZzaKPvzpU1xqmO87+N26YzvAANCaNqySEA=";
            aarch64-linux = "sha256-M5JYKYiic6h6tQQ+4bLJwXosPABYC/kybEei4KqurJI=";
          }.${system};
          go = pkgs.stdenvNoCC.mkDerivation {
            pname = "go";
            version = "1.27.4";
            src = pkgs.fetchurl {
              url = "https://go.dev/dl/go1.27.4.linux-${goArch}.tar.gz";
              hash = goHash;
            };
            dontBuild = true;
            dontFixup = true;
            installPhase = "cp -r . $out";
          };
        in
        {
          default = pkgs.mkShell {
            packages = with pkgs; [
              go
              kubebuilder
              cobra-cli
              talosctl
              kubectl
              kubernetes-helm
              cilium-cli # installs Cilium as a Helm release for the foreign-cluster hardware test
              buf
              protoc-gen-go
              protoc-gen-connect-go
              goreleaser
              ko # builds the three images without a container runtime (.ko.yaml)
              go-containerregistry # crane: pushes the images ko lays out, and logs in to the registry
              govulncheck
              cosign # keyless signing of the release images and chart
              syft # SBOMs of the release images
              actionlint # syntax check of .github/workflows
              shellcheck # lints hack/*.sh and the shell steps actionlint finds in the workflows
              yq-go # hack/bump-cilium-patch.sh reads the Cilium Helm index and chart values with it
              qemu
              cni-plugins
              nodejs
              chromium # headless screenshots of the UI (hack/ui-shots.mjs, make ui-shots)
              drawio # exports the docs diagrams to SVG (make docs-diagrams)
              xvfb-run # draw.io is an Electron app and wants a display
              gnumake
              openssl # self-signed ichor-ui cert for the UI demo (test/harness/ui-demo/README.md)
              git
              asciinema # records a terminal session
              asciinema-agg # turns an asciinema recording into a GIF
            ];

            # agg finds no monospace font on NixOS, so point it at one:
            # agg --font-dir $ICHOR_FONT_DIR --font-family "DejaVu Sans Mono"
            ICHOR_FONT_DIR = "${pkgs.dejavu_fonts}/share/fonts/truetype";

            # shortcut: controller-gen, kustomize, setup-envtest and
            # golangci-lint come from the Makefile, which fetches
            # them from the Go module proxy and, for setup-envtest, downloads
            # envtest's assets too — a fresh checkout and an offline build
            # both need the network. Revisit if offline CI is ever needed.
          };
        });
    };
}
