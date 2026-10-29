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
// adopt and reclaim hardware cases (ADR 0103, "Hardware test plan"). The configs come from
// `talosctl gen config`, are applied with `talosctl apply-config` and bootstrapped with
// `talosctl bootstrap`; nothing here renders a machine config through Ichor. Every object
// carries the label Label=<prefix>, and Down deletes only objects that still carry it.
package foreign

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// cachedVersion is the Talos version whose ISO is already cached as a DataVolume (ADR 0102).
	cachedVersion = "v1.14.3"
	maxWorkers    = 4
	maxCPs        = 3
	podSubnet     = "10.248.0.0/16"
	serviceSubnet = "10.249.0.0/16"
	cniNone       = "none"

	// CNI variants.
	CNIFlannel      = "talos-flannel"
	CNINone         = cniNone
	CNICiliumInline = "cilium-inline"
	CNICiliumHelm   = "cilium-helm"
	CNICalico       = "calico"

	calicoManifest = "https://raw.githubusercontent.com/projectcalico/calico/v3.30.3/manifests/calico.yaml"
)

var prefixRe = regexp.MustCompile(`^ichor-e2e-f[a-z0-9-]{0,20}$`)

// Options describes a foreign cluster.
type Options struct {
	Prefix         string // must start with PrefixRoot
	TalosVersion   string // e.g. v1.14.3; empty means the cached one
	K8sVersion     string // empty: talosctl's default for the contract
	ControlPlanes  int    // 1..3, default 1
	Workers        int    // 0..4, default 1
	CNI            string // one of the CNI* constants, default talos-flannel
	NoKubeProxy    bool   // cluster.proxy.disabled
	NoKubePrism    bool   // machine.features.kubePrism.enabled=false
	CPMemory       string // default 2560Mi
	WorkerMemory   string // default 1536Mi
	RegistryMirror bool   // nodes mirror ghcr.io to <prefix>-registry
	RelayPort      string // local port of the kube-apiserver tunnel, default 5015
	CiliumChart    string // path to the cilium chart archive (cilium-* variants)
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
	case CNIFlannel, CNINone, CNICiliumInline, CNICiliumHelm, CNICalico:
	default:
		return fmt.Errorf("unknown CNI variant %q", o.CNI)
	}
	return nil
}

func vmName(prefix, role string, i int) string { return fmt.Sprintf("%s-%s%d", prefix, role, i) }

// Dir is where a prefix keeps its files.
func Dir(prefix string) (string, error) {
	root, err := utils.GetProjectDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "bin", "foreign", prefix), nil
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
func isoSource(version string) string {
	if version == cachedVersion {
		cache := "ichor-e2e-talos-" + strings.ReplaceAll(version, ".", "-") + "-" + schematic[:8] + "-base"
		out, err := utils.Run(kube.Command("-n", namespace, "get", "pvc", cache, "-o", "jsonpath={.status.capacity.storage}"))
		if size := strings.TrimSpace(out); err == nil && size != "" {
			return fmt.Sprintf("          pvc:\n            namespace: %s\n            name: %s\n#SIZE=%s", namespace, cache, size)
		}
	}
	return "          http:\n            url: https://factory.talos.dev/image/" + schematic + "/" + version + "/metal-amd64.iso\n#SIZE=1Gi"
}

func renderVM(name, prefix, mem, version string) string {
	src := isoSource(version)
	src, size, _ := strings.Cut(src, "\n#SIZE=")
	return fmt.Sprintf(vmTmpl, name, src, size, prefix, Label, mem)
}

func apply(manifest string) error {
	cmd := kube.Command("apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	return err
}

func poll(step string, timeout time.Duration, check func() (string, bool)) (string, error) {
	fmt.Println(step)
	deadline := time.Now().Add(timeout)
	for {
		if v, ok := check(); ok {
			return v, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%s: timed out after %s", step, timeout)
		}
		time.Sleep(5 * time.Second)
	}
}

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
	if err := apply(fmt.Sprintf(registryTmpl, prefix, Label)); err != nil {
		return "", err
	}
	return poll("waiting for the registry Service IP", time.Minute, func() (string, bool) {
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
	for _, n := range names {
		mem := o.CPMemory
		if strings.Contains(n, "-w") {
			mem = o.WorkerMemory
		}
		if err := apply(renderVM(n, o.Prefix, mem, o.TalosVersion)); err != nil {
			return nil, fmt.Errorf("creating VM %s: %w", n, err)
		}
	}
	ips := map[string]string{}
	for _, n := range names {
		ip, err := poll("waiting for "+n+" pod IP", 12*time.Minute, func() (string, bool) {
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
		if _, err := poll("waiting for "+n+" maintenance mode", 10*time.Minute, func() (string, bool) {
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
	a := []string{
		"--namespace", "kube-system",
		"--set", "ipam.mode=kubernetes",
		"--set", "securityContext.capabilities.ciliumAgent={CHOWN,KILL,NET_ADMIN,NET_RAW,IPC_LOCK,SYS_ADMIN,SYS_RESOURCE,DAC_OVERRIDE,FOWNER,SETGID,SETUID}",
		"--set", "securityContext.capabilities.cleanCiliumState={NET_ADMIN,SYS_ADMIN,SYS_RESOURCE}",
		"--set", "cgroup.autoMount.enabled=false",
		"--set", "cgroup.hostRoot=/sys/fs/cgroup",
	}
	if c.Opts.NoKubeProxy {
		host, port := "localhost", "7445"
		if c.Opts.NoKubePrism {
			host, port = c.CPs[0], "6443"
		}
		a = append(a, "--set", "kubeProxyReplacement=true", "--set", "k8sServiceHost="+host, "--set", "k8sServicePort="+port)
	}
	return a
}

// generate runs `talosctl gen config` with one patch file per concern.
func (c *Cluster) generate(mirror string) error {
	o := c.Opts
	cluster := map[string]any{"network": map[string]any{
		"podSubnets": []string{podSubnet}, "serviceSubnets": []string{serviceSubnet},
	}}
	machine := map[string]any{}
	net := cluster["network"].(map[string]any)
	switch o.CNI {
	case CNINone, CNICiliumHelm:
		net["cni"] = map[string]any{"name": cniNone}
	case CNICiliumInline:
		if o.CiliumChart == "" {
			return errors.New("cilium-inline needs Options.CiliumChart")
		}
		out, err := runLocal(append([]string{"helm", "template", "cilium", o.CiliumChart}, c.ciliumHelmArgs()...)...)
		if err != nil {
			return fmt.Errorf("helm template: %w: %s", err, tail(out))
		}
		net["cni"] = map[string]any{"name": cniNone}
		cluster["inlineManifests"] = []map[string]string{{"name": "cilium", "contents": out}}
	case CNICalico:
		net["cni"] = map[string]any{"name": "custom", "urls": []string{calicoManifest}}
	}
	if o.NoKubeProxy {
		cluster["proxy"] = map[string]any{"disabled": true}
	}
	if o.NoKubePrism {
		machine["features"] = map[string]any{"kubePrism": map[string]any{"enabled": false}}
	}
	patch, err := yaml.Marshal(map[string]any{"cluster": cluster, "machine": machine})
	if err != nil {
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
	if o.K8sVersion != "" {
		args = append(args, "--kubernetes-version", strings.TrimPrefix(o.K8sVersion, "v"))
	}
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
	fmt.Println("--- applying configs (installs and reboots) ---")
	for _, ip := range c.CPs {
		if out, err := talosctl.Apply(namespace, ip, cpCfg); err != nil {
			return fmt.Errorf("apply-config %s: %w: %s", ip, err, tail(out))
		}
	}
	for _, ip := range c.Workers {
		if out, err := talosctl.Apply(namespace, ip, wCfg); err != nil {
			return fmt.Errorf("apply-config %s: %w: %s", ip, err, tail(out))
		}
	}
	all := append(append([]string{}, c.CPs...), c.Workers...)
	for _, ip := range all {
		if _, err := poll("waiting for "+ip+" authenticated API", 12*time.Minute, func() (string, bool) {
			_, err := talosctl.GetAuthenticated(namespace, ip, "machinestatus", tcBytes)
			return "", err == nil
		}); err != nil {
			return err
		}
	}
	if _, err := poll("bootstrap", 3*time.Minute, func() (string, bool) {
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
	if _, err := poll("waiting for all nodes to register", 10*time.Minute, func() (string, bool) {
		out, err := utils.Run(kube.InnerCommand(c.LocalKube, "get", "nodes", "--no-headers"))
		return "", err == nil && len(strings.Split(strings.TrimSpace(out), "\n")) == len(all) && strings.TrimSpace(out) != ""
	}); err != nil {
		return err
	}
	if o.CNI == CNICiliumHelm {
		args := append([]string{"helm", "install", "cilium", o.CiliumChart, "--kubeconfig", c.LocalKube}, c.ciliumHelmArgs()...)
		if out, err := runLocal(args...); err != nil {
			return fmt.Errorf("helm install cilium: %w: %s", err, tail(out))
		}
	}
	if o.CNI == CNINone {
		return nil
	}
	_, err = poll("waiting for all nodes Ready", 15*time.Minute, func() (string, bool) {
		out, err := utils.Run(kube.InnerCommand(c.LocalKube, "get", "nodes", "-o",
			"jsonpath={.items[*].status.conditions[?(@.type==\"Ready\")].status}"))
		f := strings.Fields(out)
		if err != nil || len(f) != len(all) {
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
	if err := apply(m); err != nil {
		return err
	}
	if _, err := utils.Run(kube.Command("-n", namespace, "wait", "pod/"+name, "--for=condition=Ready", "--timeout=120s")); err != nil {
		return err
	}
	if err := PortForward(c.Dir, "relay", "pod/"+name, c.Opts.RelayPort+":6443"); err != nil {
		return err
	}
	_, err := poll("waiting for the API tunnel", 30*time.Second, func() (string, bool) {
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
	if err := apply(m); err != nil {
		return "", err
	}
	_, err := utils.Run(kube.Command("-n", namespace, "wait", "pod/"+name, "--for=condition=Ready", "--timeout=180s"))
	return name, err
}

// Down deletes the objects Up (and EnsureRegistry/ToolsPod) created for prefix. Each is
// deleted only if it exists and still carries Label=prefix; a mismatch is reported, not deleted.
func Down(prefix string) error {
	if !prefixRe.MatchString(prefix) {
		return fmt.Errorf("prefix %q must match %s", prefix, prefixRe)
	}
	if dir, err := Dir(prefix); err == nil {
		stopPortForward(dir, "relay")
		stopPortForward(dir, "registry")
		defer os.RemoveAll(dir) //nolint:errcheck // local scratch files
	}
	type obj struct{ kind, name string }
	var objs []obj
	for _, n := range []string{"-relay", "-tools"} {
		objs = append(objs, obj{"pod", prefix + n})
	}
	objs = append(objs, obj{"deployment", prefix + "-registry"}, obj{"service", prefix + "-registry"})
	for i := range maxCPs {
		objs = append(objs, obj{"vm", vmName(prefix, "cp", i)})
	}
	for i := range maxWorkers {
		objs = append(objs, obj{"vm", vmName(prefix, "w", i)})
	}
	for _, v := range objs {
		if v.kind == "vm" {
			continue
		}
		if err := deleteOwned(prefix, v.kind, v.name); err != nil {
			return err
		}
	}
	for _, v := range objs {
		if v.kind != "vm" {
			continue
		}
		if err := deleteOwned(prefix, "vm", v.name); err != nil {
			return err
		}
		for _, dv := range []string{v.name + "-iso", v.name + "-system"} {
			if err := deleteOwned(prefix, "dv", dv); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteOwned(prefix, kind, name string) error {
	out, err := utils.Run(kube.Command("-n", namespace, "get", kind, name, "--ignore-not-found", "-o", "json"))
	if err != nil {
		return fmt.Errorf("looking up %s/%s: %w", kind, name, err)
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
		return fmt.Errorf("refusing to delete %s/%s: it does not carry %s=%s", kind, name, Label, prefix)
	}
	if _, err := utils.Run(kube.Command("-n", namespace, "delete", kind, name, "--wait=true", "--timeout=180s", "--ignore-not-found")); err != nil {
		return fmt.Errorf("deleting %s/%s: %w", kind, name, err)
	}
	fmt.Println("deleted", kind+"/"+name)
	return nil
}
