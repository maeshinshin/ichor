/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package foreign builds Talos clusters that Ichor did not create, on KubeVirt VMs, for the
// adopt and reclaim hardware cases ("Hardware test plan"). The configs come from
// `talosctl gen config`, are applied with `talosctl apply-config` and bootstrapped with
// `talosctl bootstrap`; nothing here renders a machine config through Ichor. Every object
// carries the label Label=<prefix>, and Down deletes only objects that still carry it.
package foreign

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	yaml "go.yaml.in/yaml/v4"

	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

const (
	// Label marks every outer-cluster object this package creates; its value is the prefix.
	Label = "ichor.maesh.dev/e2e-foreign"
	// PrefixRoot is what every prefix must start with, so Down cannot be pointed at another run's objects.
	PrefixRoot = "ichor-e2e-f"

	namespace = kube.Namespace
	schematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"
	// cachedVersion is the Talos version whose ISO is already cached as a DataVolume: the release set's.
	cachedVersion = "v1.14.5"
	// foreignK8sVersion is the Kubernetes a foreign cluster starts on when a test does not say: the latest 1.34
	// patch minus one. Cilium 1.19 is tested with 1.32 to 1.35 and Talos v1.14.5 supports 1.33 to 1.37. A test that
	// upgrades to the release set starts one minor below it (see TestA1Upgrade).
	foreignK8sVersion = "v1.34.12"
	// foreignCiliumVersion is the Cilium the foreign clusters start with: one minor below the embedded 1.20.4, so
	// adopt's takeover of a one-minor-older Cilium is what the cases exercise. The chart is
	// fetched by `make foreign-cilium-chart`.
	foreignCiliumVersion = "1.19.10"
	// CiliumChartPath is the chart archive (relative to the repository root) the foreign tests install.
	CiliumChartPath = "pkg/cni/cilium/chart/cilium-" + foreignCiliumVersion + ".tgz"
	maxWorkers      = 4
	maxCPs          = 3
	podSubnet       = "10.248.0.0/16"
	serviceSubnet   = "10.249.0.0/16"

	// CNI variants.
	CNIFlannel      = "talos-flannel"
	CNINone         = "none"
	CNICiliumInline = "cilium-inline"
	CNICiliumHelm   = "cilium-helm"
	CNICiliumCLI    = "cilium-cli" // `cilium install`: a Helm release written by the Cilium CLI
	CNICalico       = "calico"

	calicoManifest = "https://raw.githubusercontent.com/projectcalico/calico/v3.30.3/manifests/calico.yaml"
	ciliumName     = "cilium"
)

var prefixRe = regexp.MustCompile(`^ichor-e2e-f[a-z0-9-]{0,20}$`)

// Options describes a foreign cluster.
type Options struct {
	Prefix         string   // must start with PrefixRoot
	TalosVersion   string   // e.g. v1.14.3; empty means the cached one
	K8sVersion     string   // empty: talosctl's default for the contract
	ControlPlanes  int      // 1..3, default 1
	Workers        int      // 0..4, default 1
	CNI            string   // one of the CNI* constants, default talos-flannel
	NoKubeProxy    bool     // cluster.proxy.disabled
	NoKubePrism    bool     // machine.features.kubePrism.enabled=false
	UserManifest   bool     // a user inline manifest of a non-Cilium app
	CPMemory       string   // default 2560Mi
	WorkerMemory   string   // default 1536Mi
	RegistryMirror bool     // nodes mirror ghcr.io to <prefix>-registry
	RelayPort      string   // local port of the kube-apiserver tunnel, default 5015
	CiliumChart    string   // path to the cilium chart archive (cilium-* variants)
	CiliumSets     []string // extra --set values for the cilium-* variants, e.g. operator.replicas=1
	DataDisk       bool     // workers get a second 3Gi disk carrying a plain and a LUKS2 user volume (v1.14 contract only)
}

// Cluster is what Up returns. Files live under Dir (gitignored bin/).
type Cluster struct {
	Opts         Options
	Dir          string
	CPs, Workers []string // pod IPs
	Talosconfig  string   // endpoints are the control planes' pod IPs
	Kubeconfig   string   // server is the first control plane's pod IP (use from a pod)
	LocalKube    string   // server is the local relay tunnel (use from the workstation)
}

// Nodes returns every node's pod IP, control planes first.
func (c *Cluster) Nodes() []string { return append(append([]string{}, c.CPs...), c.Workers...) }

func (o *Options) defaults() error {
	if !prefixRe.MatchString(o.Prefix) {
		return fmt.Errorf("prefix %q must match %s", o.Prefix, prefixRe)
	}
	if o.TalosVersion == "" {
		o.TalosVersion = cachedVersion
	}
	if o.ControlPlanes == 0 {
		o.ControlPlanes = 1
	}
	if o.CNI == "" {
		o.CNI = CNIFlannel
	}
	if o.CPMemory == "" {
		o.CPMemory = "2560Mi"
	}
	if o.WorkerMemory == "" {
		o.WorkerMemory = "1536Mi"
	}
	if o.RelayPort == "" {
		o.RelayPort = "5015"
	}
	if o.ControlPlanes < 1 || o.ControlPlanes > maxCPs || o.Workers < 0 || o.Workers > maxWorkers {
		return errors.New("control planes must be 1..3 and workers 0..4")
	}
	switch o.CNI {
	case CNIFlannel, CNINone, CNICiliumInline, CNICiliumHelm, CNICiliumCLI, CNICalico:
	default:
		return fmt.Errorf("unknown CNI variant %q", o.CNI)
	}
	return nil
}

func vmName(prefix, role string, i int) string { return fmt.Sprintf("%s-%s%d", prefix, role, i) }

// Dir is where a prefix keeps its files.
func Dir(prefix string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "bin", "foreign", prefix), nil
}

// Root is the repository root: the nearest parent of the working directory holding go.mod.
// (utils.GetProjectDir only strips /test/e2e, which is wrong for test/e2e/foreign.)
func Root() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		if parent := filepath.Dir(d); parent != d {
			d = parent
			continue
		}
		return "", errors.New("go.mod not found above the working directory")
	}
}

const vmTmpl = `apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %[1]s
  namespace: ichor-e2e
  labels:
    %[5]s: %[4]s
spec:
  runStrategy: Always
  dataVolumeTemplates:
    - metadata:
        name: %[1]s-iso
        labels:
          %[5]s: %[4]s
      spec:
        source:
%[2]s
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: %[3]s
          storageClassName: ceph-block
    - metadata:
        name: %[1]s-system
        labels:
          %[5]s: %[4]s
      spec:
        source:
          blank: {}
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: 10Gi
          storageClassName: ceph-block
  template:
    metadata:
      labels:
        %[5]s: %[4]s
    spec:
      domain:
        cpu:
          cores: 2
        memory:
          guest: %[6]s
        devices:
          disks:
            - name: iso
              cdrom:
                bus: sata
              bootOrder: 1
            - name: system
              disk:
                bus: virtio
              bootOrder: 2
          interfaces:
            - name: default
              bridge: {}
      networks:
        - name: default
          pod: {}
      volumes:
        - name: iso
          dataVolume:
            name: %[1]s-iso
        - name: system
          dataVolume:
            name: %[1]s-system
`

// isoSource clones the cached ISO DataVolume when the version is the cached one and the
// cache exists (it is never created or deleted here), and imports from the Image Factory otherwise.
func isoSource(version string) (src, size string) {
	if version == cachedVersion {
		cache := "ichor-e2e-talos-" + strings.ReplaceAll(version, ".", "-") + "-" + schematic[:8] + "-base"
		out, err := utils.Run(kube.Command("-n", namespace, "get", "pvc", cache, "-o", "jsonpath={.status.capacity.storage}"))
		if size := strings.TrimSpace(out); err == nil && size != "" {
			return fmt.Sprintf("          pvc:\n            namespace: %s\n            name: %s", namespace, cache), size
		}
	}
	return "          http:\n            url: https://factory.talos.dev/image/" + schematic + "/" + version + "/metal-amd64.iso", "1Gi"
}

func renderVM(name, prefix, mem, version string, dataDisk bool) string {
	src, size := isoSource(version)
	m := fmt.Sprintf(vmTmpl, name, src, size, prefix, Label, mem)
	if !dataDisk {
		return m
	}
	dv := fmt.Sprintf(`    - metadata:
        name: %[1]s-data
        labels:
          %[2]s: %[3]s
      spec:
        source:
          blank: {}
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: 3Gi
          storageClassName: ceph-block
  template:
`, name, Label, prefix)
	m = strings.Replace(m, "  template:\n", dv, 1)
	m = strings.Replace(m, "              bootOrder: 2\n", "              bootOrder: 2\n            - name: data\n              disk:\n                bus: virtio\n", 1)
	return m + "        - name: data\n          dataVolume:\n            name: " + name + "-data\n"
}

// dataVolumesPatch is the worker patch of Options.DataDisk: one plain and one LUKS2 (node-ID key) user volume on the non-system disk.
const dataVolumesPatch = `---
apiVersion: v1alpha1
kind: UserVolumeConfig
name: plain
volumeType: partition
provisioning:
  diskSelector:
    match: '!system_disk'
  minSize: 500MiB
  maxSize: 1GiB
---
apiVersion: v1alpha1
kind: UserVolumeConfig
name: secret
volumeType: partition
provisioning:
  diskSelector:
    match: '!system_disk'
  minSize: 500MiB
  maxSize: 1GiB
encryption:
  provider: luks2
  keys:
    - slot: 0
      nodeID: {}
`

const registryTmpl = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s-registry
  namespace: ichor-e2e
  labels:
    %[2]s: %[1]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s-registry
  template:
    metadata:
      labels:
        app: %[1]s-registry
        %[2]s: %[1]s
    spec:
      containers:
        - name: registry
          image: registry:2
          ports:
            - containerPort: 5000
      # emptyDir only: re-push images after a restart
---
apiVersion: v1
kind: Service
metadata:
  name: %[1]s-registry
  namespace: ichor-e2e
  labels:
    %[2]s: %[1]s
spec:
  selector:
    app: %[1]s-registry
  ports:
    - port: 5000
      targetPort: 5000
`

// EnsureRegistry creates <prefix>-registry (Deployment + Service) and returns the Service's ClusterIP.
func EnsureRegistry(prefix string) (string, error) {
	if !prefixRe.MatchString(prefix) {
		return "", fmt.Errorf("prefix %q must match %s", prefix, prefixRe)
	}
	if err := kube.Apply(fmt.Sprintf(registryTmpl, prefix, Label)); err != nil {
		return "", err
	}
	if _, err := utils.Run(kube.Command("-n", namespace, "wait", "deployment/"+prefix+"-registry", "--for=condition=Available", "--timeout=180s")); err != nil {
		return "", err
	}
	return kube.Poll("waiting for the registry Service IP", time.Minute, func() (string, bool) {
		out, err := utils.Run(kube.Command("-n", namespace, "get", "svc", prefix+"-registry", "-o", "jsonpath={.spec.clusterIP}"))
		ip := strings.TrimSpace(out)
		return ip, err == nil && ip != ""
	})
}

// PortForward starts a detached `kubectl port-forward` of svc/pod target to 127.0.0.1:local and
// records its pid under dir/<name>.pid.
func PortForward(dir, name, target, mapping string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pf := kube.Command("-n", namespace, "port-forward", target, mapping, "--address=127.0.0.1")
	pf.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := pf.Start(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".pid"), fmt.Appendf(nil, "%d %s", pf.Process.Pid, target), 0o600)
}

// stopPortForward kills the recorded port-forward only if its command line still names the target.
func stopPortForward(dir, name string) {
	p := filepath.Join(dir, name+".pid")
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var pid int
	var target string
	if _, err := fmt.Sscanf(string(raw), "%d %s", &pid, &target); err == nil && pid > 0 {
		cl, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if strings.Contains(string(cl), "port-forward") && strings.Contains(string(cl), target) {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	_ = os.Remove(p)
}

// Up builds the cluster and returns once every node is registered (and Ready unless the CNI is none/cilium-helm/calico
// variants that need an extra step, which Up performs).
func Up(o Options) (*Cluster, error) {
	if err := o.defaults(); err != nil {
		return nil, err
	}
	dir, err := Dir(o.Prefix)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Cluster{Opts: o, Dir: dir}

	mirror := ""
	if o.RegistryMirror {
		ip, err := EnsureRegistry(o.Prefix)
		if err != nil {
			return nil, err
		}
		mirror = "http://" + ip + ":5000"
	}

	// VMs.
	var names []string
	for i := range o.ControlPlanes {
		names = append(names, vmName(o.Prefix, "cp", i))
	}
	for i := range o.Workers {
		names = append(names, vmName(o.Prefix, "w", i))
	}
	for i, n := range names {
		mem := o.CPMemory
		if i >= o.ControlPlanes {
			mem = o.WorkerMemory
		}
		if err := kube.Apply(renderVM(n, o.Prefix, mem, o.TalosVersion, o.DataDisk && i >= o.ControlPlanes)); err != nil {
			return nil, fmt.Errorf("creating VM %s: %w", n, err)
		}
	}
	ips := map[string]string{}
	for _, n := range names {
		ip, err := kube.Poll("waiting for "+n+" pod IP", 12*time.Minute, func() (string, bool) {
			out, err := utils.Run(kube.Command("-n", namespace, "get", "vmi", n, "-o", "jsonpath={.status.interfaces[0].ipAddress}"))
			ip := strings.TrimSpace(out)
			return ip, err == nil && ip != ""
		})
		if err != nil {
			return nil, err
		}
		ips[n] = ip
	}
	for _, n := range names {
		if _, err := kube.Poll("waiting for "+n+" maintenance mode", 10*time.Minute, func() (string, bool) {
			out, err := talosctl.Get(namespace, ips[n], "machinestatus")
			if err != nil {
				return "", false
			}
			objs, derr := talosctl.DecodeAll(out)
			return "", derr == nil && len(objs) == 1 && talosctl.SpecField(objs[0], "stage") == "maintenance"
		}); err != nil {
			return nil, err
		}
	}
	for i := range o.ControlPlanes {
		c.CPs = append(c.CPs, ips[vmName(o.Prefix, "cp", i)])
	}
	for i := range o.Workers {
		c.Workers = append(c.Workers, ips[vmName(o.Prefix, "w", i)])
	}

	if err := c.generate(mirror); err != nil {
		return nil, err
	}
	if err := c.bringUp(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Cluster) path(name string) string { return filepath.Join(c.Dir, name) }

func runLocal(args ...string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput() //nolint:gosec // fixed tool names from our own callers
	return string(out), err
}

// ciliumHelmArgs are the Talos "Deploying Cilium" values (docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium,
// Talos v1.14): ipam kubernetes, cgroup not auto-mounted, the capability sets, and KubePrism
// as the API address when kube-proxy is replaced.
func (c *Cluster) ciliumHelmArgs() []string {
	sets := []string{
		"ipam.mode=kubernetes",
		"securityContext.capabilities.ciliumAgent={CHOWN,KILL,NET_ADMIN,NET_RAW,IPC_LOCK,SYS_ADMIN,SYS_RESOURCE,DAC_OVERRIDE,FOWNER,SETGID,SETUID}",
		"securityContext.capabilities.cleanCiliumState={NET_ADMIN,SYS_ADMIN,SYS_RESOURCE}",
		"cgroup.autoMount.enabled=false",
		"cgroup.hostRoot=/sys/fs/cgroup",
		"tunnelProtocol=geneve", // VXLAN does not pass between the KubeVirt VMs (the outer cluster uses its port)
	}
	if c.Opts.NoKubeProxy {
		host, port := "localhost", "7445"
		if c.Opts.NoKubePrism {
			host, port = c.CPs[0], "6443"
		}
		sets = append(sets, "kubeProxyReplacement=true", "k8sServiceHost="+host, "k8sServicePort="+port)
	}
	sets = append(sets, c.Opts.CiliumSets...)
	a := append(make([]string, 0, 2+2*len(sets)), "--namespace", "kube-system")
	for _, s := range sets {
		a = append(a, "--set", s)
	}
	return a
}

// generate runs `talosctl gen config` with one patch file per concern.
func (c *Cluster) generate(mirror string) error {
	o := c.Opts
	manifest, err := c.cniManifest()
	if err != nil {
		return err
	}
	var patch, cpPatch []byte
	if multiDoc(o.TalosVersion) {
		patch, cpPatch = c.patchMultiDoc(manifest)
	} else if patch, err = c.patchLegacy(manifest); err != nil {
		return err
	}
	patchFile := c.path("patch.yaml")
	if err := os.WriteFile(patchFile, patch, 0o600); err != nil {
		return err
	}
	args := []string{"talosctl", "gen", "config", o.Prefix, "https://" + c.CPs[0] + ":6443",
		"--output", c.Dir, "--force",
		"--install-disk", "/dev/vda",
		"--install-image", "factory.talos.dev/metal-installer/" + schematic + ":" + o.TalosVersion,
		"--talos-version", o.TalosVersion,
		"--additional-sans", "127.0.0.1,localhost," + strings.Join(c.CPs, ","),
		"--config-patch", "@" + patchFile}
	if len(cpPatch) > 0 {
		cpFile := c.path("patch-cp.yaml")
		if err := os.WriteFile(cpFile, cpPatch, 0o600); err != nil {
			return err
		}
		args = append(args, "--config-patch-control-plane", "@"+cpFile)
	}
	if o.DataDisk {
		wFile := c.path("patch-worker.yaml")
		if err := os.WriteFile(wFile, []byte(dataVolumesPatch), 0o600); err != nil {
			return err
		}
		args = append(args, "--config-patch-worker", "@"+wFile)
	}
	// Adopt refuses a Kubernetes newer than the release set, and talosctl's default for
	// Talos v1.14.5 is newer, so the foreign cluster starts one patch below the set.
	k8s := cmp.Or(o.K8sVersion, foreignK8sVersion)
	args = append(args, "--kubernetes-version", strings.TrimPrefix(k8s, "v"))
	if mirror != "" {
		args = append(args, "--registry-mirror", "ghcr.io="+mirror)
	}
	if out, err := runLocal(args...); err != nil {
		return fmt.Errorf("talosctl gen config: %w: %s", err, tail(out))
	}
	tc := c.path("talosconfig")
	if out, err := runLocal(append([]string{"talosctl", "--talosconfig", tc, "config", "endpoint"}, c.CPs...)...); err != nil {
		return fmt.Errorf("talosctl config endpoint: %w: %s", err, tail(out))
	}
	c.Talosconfig = tc
	return nil
}

func tail(s string) string {
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}

func (c *Cluster) bringUp() error {
	o := c.Opts
	cpCfg, err := os.ReadFile(c.path("controlplane.yaml"))
	if err != nil {
		return err
	}
	wCfg, err := os.ReadFile(c.path("worker.yaml"))
	if err != nil {
		return err
	}
	tcBytes, err := os.ReadFile(c.Talosconfig)
	if err != nil {
		return err
	}
	if err := c.applyConfigs(cpCfg, wCfg); err != nil {
		return err
	}
	for _, ip := range c.Nodes() {
		if _, err := kube.Poll("waiting for "+ip+" authenticated API", 12*time.Minute, func() (string, bool) {
			_, err := talosctl.GetAuthenticated(namespace, ip, "machinestatus", tcBytes)
			return "", err == nil
		}); err != nil {
			return err
		}
	}
	if _, err := kube.Poll("bootstrap", 3*time.Minute, func() (string, bool) {
		_, err := talosctl.Bootstrap(namespace, c.CPs[0], tcBytes)
		return "", err == nil
	}); err != nil {
		return err
	}
	kc, err := talosctl.Kubeconfig(namespace, c.CPs[0], tcBytes)
	if err != nil {
		return fmt.Errorf("kubeconfig: %w", err)
	}
	c.Kubeconfig = c.path("kubeconfig")
	c.LocalKube = c.path("kubeconfig.local")
	local := strings.ReplaceAll(string(kc), "https://"+c.CPs[0]+":6443", "https://127.0.0.1:"+o.RelayPort)
	if err := os.WriteFile(c.Kubeconfig, kc, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(c.LocalKube, []byte(local), 0o600); err != nil {
		return err
	}
	if err := c.startRelay(); err != nil {
		return err
	}
	if _, err := kube.Poll("waiting for all nodes to register", 10*time.Minute, func() (string, bool) {
		out, err := utils.Run(kube.InnerCommand(c.LocalKube, "get", "nodes", "--no-headers"))
		return "", err == nil && len(strings.Split(strings.TrimSpace(out), "\n")) == len(c.Nodes()) && strings.TrimSpace(out) != ""
	}); err != nil {
		return err
	}
	// adopt discovers machines from the CP's discovery membership and refuses an incomplete one,
	// so wait until it lists every node.
	if _, err := kube.Poll("waiting for discovery to list every member", 10*time.Minute, func() (string, bool) {
		out, err := talosctl.GetAuthenticated(namespace, c.CPs[0], "members", tcBytes)
		if err != nil {
			return "", false
		}
		objs, err := talosctl.DecodeAll(out)
		return "", err == nil && len(objs) == len(c.Nodes())
	}); err != nil {
		return err
	}
	if err := c.installCNI(); err != nil {
		return err
	}
	if o.CNI == CNINone {
		return nil
	}
	_, err = kube.Poll("waiting for all nodes Ready", 15*time.Minute, func() (string, bool) {
		out, err := utils.Run(kube.InnerCommand(c.LocalKube, "get", "nodes", "-o",
			"jsonpath={.items[*].status.conditions[?(@.type==\"Ready\")].status}"))
		f := strings.Fields(out)
		if err != nil || len(f) != len(c.Nodes()) {
			return "", false
		}
		for _, s := range f {
			if s != "True" {
				return "", false
			}
		}
		return "", true
	})
	return err
}

// applyConfigs applies the control-plane and worker configs to their nodes, which installs and reboots them.
func (c *Cluster) applyConfigs(cpCfg, wCfg []byte) error {
	fmt.Println("--- applying configs (installs and reboots) ---")
	for i, ip := range c.Nodes() {
		cfg := wCfg
		if i < len(c.CPs) {
			cfg = cpCfg
		}
		if out, err := talosctl.Apply(namespace, ip, cfg); err != nil {
			return fmt.Errorf("apply-config %s: %w: %s", ip, err, tail(out))
		}
	}
	return nil
}

// installCNI installs the CNI variant the cluster asked for; the variants it does not handle are a no-op.
func (c *Cluster) installCNI() error {
	o := c.Opts
	if o.CNI == CNICalico { // applied to the running cluster, as a user does; a Talos inline manifest would make adopt block the machine
		if err := c.applyCalico(); err != nil {
			return err
		}
	}
	if o.CNI == CNICiliumHelm {
		args := append([]string{"helm", "install", ciliumName, o.CiliumChart, "--kubeconfig", c.LocalKube}, c.ciliumHelmArgs()...)
		if out, err := runLocal(args...); err != nil {
			return fmt.Errorf("helm install cilium: %w: %s", err, tail(out))
		}
	}
	if o.CNI == CNICiliumCLI { // the chart version is pinned to the one the helm variant uses; the CLI downloads it and writes the Helm record
		args := append([]string{ciliumName, "install", "--version", foreignCiliumVersion, "--kubeconfig", c.LocalKube, "--wait=false"}, c.ciliumHelmArgs()...)
		if out, err := runLocal(args...); err != nil {
			return fmt.Errorf("cilium install: %w: %s", err, tail(out))
		}
	}
	return nil
}

func (c *Cluster) startRelay() error {
	name := c.Opts.Prefix + "-relay"
	m := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: ichor-e2e
  labels:
    %s: %s
spec:
  restartPolicy: Never
  containers:
    - name: relay
      image: alpine/socat
      args: ["TCP-LISTEN:6443,fork,reuseaddr", "TCP:%s:6443"]
`, name, Label, c.Opts.Prefix, c.CPs[0])
	if err := kube.Apply(m); err != nil {
		return err
	}
	if _, err := utils.Run(kube.Command("-n", namespace, "wait", "pod/"+name, "--for=condition=Ready", "--timeout=120s")); err != nil {
		return err
	}
	if err := PortForward(c.Dir, "relay", "pod/"+name, c.Opts.RelayPort+":6443"); err != nil {
		return err
	}
	_, err := kube.Poll("waiting for the API tunnel", 3*time.Minute, func() (string, bool) {
		_, err := utils.Run(kube.InnerCommand(c.LocalKube, "get", "--raw", "/readyz"))
		return "", err == nil
	})
	return err
}

// ToolsPod creates <prefix>-tools (busybox) that CLI commands run in, because the workstation cannot reach pod IPs.
func ToolsPod(prefix string) (string, error) {
	name := prefix + "-tools"
	m := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: ichor-e2e
  labels:
    %s: %s
spec:
  containers:
    - name: tools
      image: busybox:1.37
      command: ["sleep", "86400"]
`, name, Label, prefix)
	if err := kube.Apply(m); err != nil {
		return "", err
	}
	_, err := utils.Run(kube.Command("-n", namespace, "wait", "pod/"+name, "--for=condition=Ready", "--timeout=180s"))
	return name, err
}

// Down deletes the objects Up (and EnsureRegistry/ToolsPod) created for prefix. Each is
// deleted only if it exists (the registry only when withRegistry, so images survive between cases) and still carries Label=prefix; a mismatch is reported, not deleted.
func Down(prefix string, withRegistry bool) error {
	if !prefixRe.MatchString(prefix) {
		return fmt.Errorf("prefix %q must match %s", prefix, prefixRe)
	}
	if dir, err := Dir(prefix); err == nil {
		stopPortForward(dir, "relay")
		for _, f := range []string{"patch.yaml", "patch-cp.yaml", "patch-worker.yaml", "controlplane.yaml", "worker.yaml", "talosconfig", "kubeconfig", "kubeconfig.local", "relay.pid"} {
			_ = os.Remove(filepath.Join(dir, f))
		}
		if withRegistry {
			stopPortForward(dir, "registry")
		}
	}
	for _, n := range []string{"pod/" + prefix + "-relay", "pod/" + prefix + "-tools"} {
		if err := deleteOwned(prefix, n); err != nil {
			return err
		}
	}
	if withRegistry {
		for _, n := range []string{"deployment/", "service/"} {
			if err := deleteOwned(prefix, n+prefix+"-registry"); err != nil {
				return err
			}
		}
	}
	var vms []string
	for i := range maxCPs {
		vms = append(vms, vmName(prefix, "cp", i))
	}
	for i := range maxWorkers {
		vms = append(vms, vmName(prefix, "w", i))
	}
	for _, vm := range vms {
		for _, n := range []string{"vm/" + vm, "dv/" + vm + "-iso", "dv/" + vm + "-system", "dv/" + vm + "-data"} {
			if err := deleteOwned(prefix, n); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteOwned(prefix, ref string) error {
	out, err := utils.Run(kube.Command("-n", namespace, "get", ref, "--ignore-not-found", "-o", "json"))
	if err != nil {
		return fmt.Errorf("looking up %s: %w", ref, err)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var o struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		return err
	}
	if o.Metadata.Labels[Label] != prefix {
		return fmt.Errorf("refusing to delete %s: it does not carry %s=%s", ref, Label, prefix)
	}
	if _, err := utils.Run(kube.Command("-n", namespace, "delete", ref, "--wait=true", "--timeout=180s", "--ignore-not-found")); err != nil {
		return fmt.Errorf("deleting %s: %w", ref, err)
	}
	fmt.Println("deleted", ref)
	return nil
}

// multiDoc reports whether the contract is v1.14 or later, where cluster settings are separate documents.
func multiDoc(version string) bool {
	var maj, min int
	_, _ = fmt.Sscanf(version, "v%d.%d", &maj, &min)
	return maj > 1 || min >= 14
}

// cniManifest returns the manifest the node config carries for the cilium-inline and calico variants.
func (c *Cluster) cniManifest() (string, error) {
	if c.Opts.CNI != CNICiliumInline {
		return "", nil
	}
	if c.Opts.CiliumChart == "" {
		return "", errors.New("cilium-inline needs Options.CiliumChart")
	}
	out, err := runLocal(append([]string{"helm", "template", ciliumName, c.Opts.CiliumChart}, c.ciliumHelmArgs()...)...)
	if err != nil {
		return "", fmt.Errorf("helm template: %w: %s", err, tail(out))
	}
	return out, nil
}

func (c *Cluster) applyCalico() error {
	resp, err := http.Get(calicoManifest) //nolint:gosec,noctx // fixed URL, test harness
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// The pool follows the cluster's pod subnet (the default 192.168.0.0/16 is the LAN's), and VXLAN on its default
	// port 4789 replaces IP-in-IP, which the outer pod network does not carry.
	m := strings.NewReplacer(
		"- name: CALICO_IPV4POOL_IPIP\n              value: \"Always\"", "- name: CALICO_IPV4POOL_IPIP\n              value: \"Never\"",
		"- name: CALICO_IPV4POOL_VXLAN\n              value: \"Never\"", "- name: CALICO_IPV4POOL_VXLAN\n              value: \"Always\"",
		"# - name: CALICO_IPV4POOL_CIDR\n            #   value: \"192.168.0.0/16\"", "- name: CALICO_IPV4POOL_CIDR\n              value: \""+podSubnet+"\"",
	).Replace(string(b))
	if m == string(b) {
		return errors.New("calico manifest: none of the pool settings matched")
	}
	f := c.path("calico.yaml")
	if err := os.WriteFile(f, []byte(m), 0o600); err != nil {
		return err
	}
	_, err = utils.Run(kube.InnerCommand(c.LocalKube, "apply", "--server-side", "-f", f))
	return err
}

// patchMultiDoc returns the patch for every machine and the one only control planes accept (KubeProxyConfig).
func (c *Cluster) patchMultiDoc(manifest string) (all, cpOnly []byte) {
	o := c.Opts
	doc := func(kind string, f map[string]any) map[string]any { f["kind"] = kind; return f }
	disabled := func(kind string) map[string]any { return doc(kind, map[string]any{"enabled": false}) }
	docs := []map[string]any{
		doc("KubeNetworkConfig", map[string]any{"podSubnets": []string{podSubnet}, "serviceSubnets": []string{serviceSubnet}}),
	}
	if o.CNI != CNIFlannel {
		docs = append(docs, doc("KubeFlannelCNIConfig", map[string]any{"$patch": "delete"}))
	}
	if manifest != "" {
		docs = append(docs, doc("KubeInlineManifestConfig", map[string]any{keyName: o.CNI[:6], "manifest": manifest}))
	}
	if o.UserManifest {
		docs = append(docs, doc("KubeInlineManifestConfig", map[string]any{keyName: "userapp", "manifest": userManifest}))
	}
	if o.NoKubePrism {
		docs = append(docs, doc("KubePrismConfig", map[string]any{"$patch": "delete"})) // the document has no enabled field: absent means off
	}
	render := func(docs []map[string]any) []byte {
		var b strings.Builder
		for _, d := range docs {
			d["apiVersion"] = "v1alpha1"
			y, _ := yaml.Marshal(d)
			b.WriteString("---\n" + string(y))
		}
		return []byte(b.String())
	}
	if o.NoKubeProxy {
		cpOnly = render([]map[string]any{disabled("KubeProxyConfig")})
	}
	return render(docs), cpOnly
}

// keyName is the YAML key of a named document field.
const keyName = "name"

// userManifest is the non-Cilium application of B7: a ConfigMap, nothing Ichor can represent.
const userManifest = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: user-app\n  namespace: default\ndata:\n  a: b\n"

type inlineManifest struct {
	Name     string `yaml:"name"`
	Contents string `yaml:"contents"`
}

func (c *Cluster) patchLegacy(manifest string) ([]byte, error) {
	o := c.Opts
	net := map[string]any{"podSubnets": []string{podSubnet}, "serviceSubnets": []string{serviceSubnet}}
	cluster := map[string]any{"network": net}
	machine := map[string]any{}
	if o.CNI != CNIFlannel {
		net["cni"] = map[string]any{keyName: CNINone}
	}
	if manifest != "" || o.UserManifest {
		cluster["inlineManifests"] = []inlineManifest{}
	}
	if manifest != "" {
		cluster["inlineManifests"] = []inlineManifest{{o.CNI[:6], manifest}}
	}
	if o.UserManifest {
		cluster["inlineManifests"] = append(cluster["inlineManifests"].([]inlineManifest), inlineManifest{"userapp", userManifest})
	}
	if o.NoKubeProxy {
		cluster["proxy"] = map[string]any{"disabled": true}
	}
	if o.NoKubePrism {
		machine["features"] = map[string]any{"kubePrism": map[string]any{"enabled": false}}
	}
	return yaml.Marshal(map[string]any{"cluster": cluster, "machine": machine})
}

// Attach rebuilds the Cluster of a prefix that `foreign up` already brought up (so one case can span
// several foreground runs). The nodes' pod IPs come from their VMIs; the files and the API tunnel are Up's.
func Attach(o Options) (*Cluster, error) {
	if err := o.defaults(); err != nil {
		return nil, err
	}
	dir, err := Dir(o.Prefix)
	if err != nil {
		return nil, err
	}
	c := &Cluster{Opts: o, Dir: dir, Talosconfig: filepath.Join(dir, "talosconfig"),
		Kubeconfig: filepath.Join(dir, "kubeconfig"), LocalKube: filepath.Join(dir, "kubeconfig.local")}
	ip := func(name string) (string, error) {
		out, err := utils.Run(kube.Command("-n", namespace, "get", "vmi", name, "-o", "jsonpath={.status.interfaces[0].ipAddress}"))
		return strings.TrimSpace(out), err
	}
	for i := range o.ControlPlanes {
		v, err := ip(vmName(o.Prefix, "cp", i))
		if err != nil {
			return nil, err
		}
		c.CPs = append(c.CPs, v)
	}
	for i := range o.Workers {
		v, err := ip(vmName(o.Prefix, "w", i))
		if err != nil {
			return nil, err
		}
		c.Workers = append(c.Workers, v)
	}
	return c, nil
}
