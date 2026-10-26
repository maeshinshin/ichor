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
              lua5_1 # runs dist/chart/files/argocd-health/*.lua in tests; 5.1 = Argo CD's gopher-lua
              buf
              protoc-gen-go
              protoc-gen-connect-go
              goreleaser
              govulncheck
              qemu
              cni-plugins
              nodejs
              gnumake
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
