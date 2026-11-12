# 0117. `ichor init` reads a structured config file

Status: Accepted · Date: 2026-11-04 · Design section: Bootstrap · Touches: 0019, 0094, 0113

Decided: `ichor init -f init.yaml` reads the init settings from an `InitConfig` file (`apiVersion: ichor.maesh.dev/v1alpha1`) instead of a long flag line. The file is decoded strictly into one typed Go struct (unknown, duplicate and mistyped keys, and a wrong `apiVersion` or `kind`, are errors that name the file and the key) and every value is then set through the init flag it stands for, so the file adds no second code path: parsing, defaults and validation are the flags' own, and a file and the equivalent flags behave the same. A flag given on the command line overrides the file. The required flags (`--cluster`, `--endpoint-address`, `--node-endpoint`, `--install-disk`) are required unless the file gives them; the error names the flag and the file key. Relative paths in the file (`ui.tls.certFile`, `ui.tls.keyFile`) resolve against the file's directory. The file holds paths, never key material. `--print-config-template` prints the commented example.

## Context

A real first install needs a dozen or more flags (endpoint, subnets, mirrors, disk selectors, discovery, UI URL, TLS, kube API). The line is long, hard to review and easy to get subtly wrong between runs. The user asked for a file for v0.1.0.

## Decision

### 1. Format

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo                       # --cluster (required)
  namespace: ichor-system          # -n/--namespace
  preset: single                   # --preset
  endpoint:
    type: node                     # --endpoint-type
    address: 192.168.1.50          # --endpoint-address (required)
  network:
    podSubnets:                    # --pod-subnets
      - 10.248.0.0/16
    serviceSubnets:                # --service-subnets
      - 10.249.0.0/16
    cni:
      tunnel: geneve               # --cni-tunnel
  registryMirrors:                 # --registry-mirror HOST=URL, as a map
    ghcr.io: http://192.168.1.10:5000
firstNode:
  endpoint: 192.168.1.50:50000     # --node-endpoint (required)
  installDisk: /dev/sda            # --install-disk (required)
  disks:
    ephemeralSelector: 'disk.wwid == "..."'   # --ephemeral-disk-selector
    storage:                                  # --storage-disk-selector, --storage-volume-name
      selector: 'disk.wwid == "..."'
      volumeName: storage
    userVolume:                               # --user-volume-disk-selector, --user-volume-name
      selector: 'disk.wwid == "..."'
      volumeName: data
discovery:
  addresses:                       # --discover-address
    - 192.168.1.51
  cidrs:                           # --discover-cidr
    - 192.168.1.64/29
ui:                                # leave the whole block out for no sign-in (no --ui-url)
  url: https://192.168.1.60        # --ui-url
  adminUser: admin                 # --admin-user
  tls:                             # optional; leave out for Ichor's own CA
    certFile: ./ui.crt             # --ui-tls-cert
    keyFile: ./ui.key              # --ui-tls-key
    # secretName: ichor-ui-cert    # --ui-tls-secret, instead of certFile and keyFile
  kubeAPI:
    serverURL: https://192.168.1.60:6443   # --kube-api-server-url
    tlsServerName: 192.168.1.50            # --kube-api-tls-server-name
waitTimeout: 30m                   # --wait-timeout
```

Lists are written in block style (one `- item` per line) in everything Ichor shows; the parser accepts the inline form as well. The hidden and deprecated flags (`--cni`, `--talos-version`, `--kubernetes-version`, `--version-contract`) stay flag-only: the release set decides them (ADR 0113). An empty value in the file is the same as leaving the key out.

### 2. One code path

`bindInitFlags` registers the flags on any flag set; `applyInitFile` decodes the file and calls `Set` on the flag of each key it holds, skipping a flag whose `Changed` is already true. `initOptions` therefore needs no second constructor, and `validateInitUIExtras`, `validateInitUsers` and the plan validation see the same struct whichever way it was filled. `ui.tls.secretName` together with `certFile` fails in `validateInitUIExtras`, with the flag-form message.

### 3. Required settings

The four required flags were enforced by cobra before `RunE`, which cannot know about the file. They are now checked in `RunE` after the file is applied, with the same wording as cobra (`required flag(s) "cluster" (file key cluster.name), ... not set`). A setting is present if a flag set it or the file did. The check is not weaker: a value neither source gives is refused before anything happens. The hidden `--cni` flag was in the list by mistake although it has a default and cilium is the only CNI (ADR 0101); it is not required.

### 4. Paths and secrets

A relative `certFile` or `keyFile` resolves against the directory of the file, not the working directory, so a file and its certificate can be moved together. The file holds only paths: key bytes are read by the existing `readUITLSFiles` and never printed or put in an error.

### 5. Template

`ichor init --print-config-template` prints the example above with comments. The text lives in one constant, `initConfigTemplate`.

## Rejected

- A second options type with its own validation: two paths that would drift.
- A raw-YAML escape hatch for machine configuration: the file sets the init flags only; machine configuration stays typed (`TalosConfigPatch`).
- Key material in the file: a file that gets committed or pasted must not be a secret.
- Version fields in the file: ADR 0113 and 0115 keep the release set out of the install's inputs.
