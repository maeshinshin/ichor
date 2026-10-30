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
              cilium-cli # ADR 0103 A3: installs Cilium as a Helm release for the foreign-cluster hardware test
              buf
              protoc-gen-go
              protoc-gen-connect-go
              goreleaser
              go-containerregistry # crane: pushes images without a container runtime (test/harness/ui-demo)
              govulncheck
              qemu
              cni-plugins
              nodejs
              chromium # headless screenshots of the UI (hack/ui-shots.mjs, make ui-shots)
              gnumake
              openssl # self-signed ichor-ui cert for the UI demo (test/harness/ui-demo/README.md)
              git
            ];

            # shortcut: controller-gen, kustomize, setup-envtest and
            # golangci-lint come from the Makefile (ADR 0005), which fetches
            # them from the Go module proxy and, for setup-envtest, downloads
            # envtest's assets too — a fresh checkout and an offline build
            # both need the network. Revisit if offline CI is ever needed.
          };
        });
    };
}
