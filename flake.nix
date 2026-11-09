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
        in
        {
          default = pkgs.mkShell {
            packages = with pkgs; [
              go_1_27
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
            ];

            # shortcut: controller-gen, kustomize, setup-envtest and
            # golangci-lint come from the Makefile, which fetches
            # them from the Go module proxy and, for setup-envtest, downloads
            # envtest's assets too — a fresh checkout and an offline build
            # both need the network. Revisit if offline CI is ever needed.
          };
        });
    };
}
