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

package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	yaml "go.yaml.in/yaml/v4"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"k8s.io/utils/ptr"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// DownloadScope names one of the three downloads (docs/adr/0084). The string is
// also the RBAC subresource on talosmachines the caller needs the "download"
// verb on, so the three can be granted separately.
type DownloadScope string

const (
	ScopeSupportBundle DownloadScope = "support-bundle"
	ScopeEtcdSnapshot  DownloadScope = "etcd-snapshot"
	ScopePacketCapture DownloadScope = "packet-capture"
)

// The Talos role of each scope is the controller's to decide (credentials.TalosRoles): the least role that
// may call the scope's API, read from the node's own RBAC table (internal/app/machined/pkg/system/services/
// machined.go in Talos v1.14). Everything the bundle calls is open to os:reader, EtcdSnapshot to
// os:etcd:backup, and PacketCapture only to os:operator or os:admin.

// DownloadTalosClient is the Talos surface the three downloads need from one
// machine. Like NodeTalosClient it has no method that changes the node.
type DownloadTalosClient interface {
	// CollectSupport gathers the node's diagnostics and hands each file to add.
	// A source that fails is reported in problems and does not stop the rest;
	// err is only add's own failure or a cancelled context.
	CollectSupport(ctx context.Context, add func(name string, data []byte) error) (problems []string, err error)

	// EtcdSnapshot opens the snapshot stream. The caller bounds and closes it.
	EtcdSnapshot(ctx context.Context) (io.ReadCloser, error)

	// PacketCapture opens a pcap stream on iface with an already validated
	// filter. The stream runs until ctx ends; the caller bounds it.
	PacketCapture(ctx context.Context, iface string, filter []*machineapi.BPFInstruction) (io.ReadCloser, error)
}

// NewMachineryDownloadTalosClient dials m with a certificate for scope's Talos
// role. bundle is required: a machine that is not approved has no CA trust yet.
func NewMachineryDownloadTalosClient(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, id *TalosIdentity, scope DownloadScope,
) (DownloadTalosClient, error) {
	if id == nil {
		return nil, errors.New("downloads need a certificate for the call")
	}

	c, err := dialTalos(ctx, cluster, m, id)
	if err != nil {
		return nil, err
	}

	return &machineryDownloadClient{c: c}, nil
}

type machineryDownloadClient struct{ c *client.Client }

// Close releases the gRPC connection.
func (t *machineryDownloadClient) Close() error { return t.c.Close() }

func (t *machineryDownloadClient) EtcdSnapshot(ctx context.Context) (io.ReadCloser, error) {
	return t.c.EtcdSnapshot(ctx, &machineapi.EtcdSnapshotRequest{})
}

func (t *machineryDownloadClient) PacketCapture(
	ctx context.Context, iface string, filter []*machineapi.BPFInstruction,
) (io.ReadCloser, error) {
	return t.c.PacketCapture(ctx, &machineapi.PacketCaptureRequest{Interface: iface, BpfFilter: filter})
}

// Support bundle limits. Each file is capped so one chatty service cannot fill
// the whole bundle; the handler caps the archive as a whole.
const (
	bundleLogTailLines  = 5000
	bundleEntryMaxBytes = 8 << 20
	bundleMaxProcesses  = 2000
	bundleMaxPerType    = 2000
)

// excludedNamespaces and excludedTypes are COSI resources never written, on top
// of everything Talos itself marks sensitive: the secrets namespace holds
// certificates and keys, the config namespace holds the machine configuration,
// and the kernel command line can carry talos.config=<url with a token>.
// configNamespace is the COSI namespace of the machine configuration.
const configNamespace = "config"

var (
	excludedNamespaces = map[resource.Namespace]bool{"secrets": true, configNamespace: true}
	excludedTypes      = map[resource.Type]bool{
		"KernelCmdlines.runtime.talos.dev": true,
		"MachineConfigs.config.talos.dev":  true,
	}
)

func (t *machineryDownloadClient) CollectSupport(
	ctx context.Context, add func(name string, data []byte) error,
) ([]string, error) {
	var problems []string

	// try runs one source: a failure is recorded, an add failure aborts.
	try := func(what string, fn func() (map[string][]byte, error)) error {
		files, err := fn()
		if err != nil {
			problems = append(problems, what+": "+truncateText(err.Error(), 300))
		}

		for _, n := range slices.Sorted(maps.Keys(files)) {
			if err := add(n, files[n]); err != nil {
				return err
			}
		}

		return ctx.Err()
	}

	one := func(name string, data []byte, err error) (map[string][]byte, error) {
		if err != nil {
			return nil, err
		}

		return map[string][]byte{name: data}, nil
	}

	steps := []struct {
		what string
		fn   func() (map[string][]byte, error)
	}{
		{"version", func() (map[string][]byte, error) {
			d, e := marshalJSON(t.c.Version(ctx))
			return one("version.json", d, e)
		}},
		{"kernel log", func() (map[string][]byte, error) { d, e := t.dmesg(ctx); return one("dmesg.log", d, e) }},
		{"services", func() (map[string][]byte, error) { return t.serviceLogs(ctx, &problems) }},
		{"processes", func() (map[string][]byte, error) { d, e := t.processes(ctx); return one("processes.json", d, e) }},
		{"mounts", func() (map[string][]byte, error) {
			d, e := marshalJSON(t.c.Mounts(ctx))
			return one("mounts.json", d, e)
		}},
		{"memory", func() (map[string][]byte, error) {
			d, e := marshalJSON(t.c.Memory(ctx))
			return one("memory.json", d, e)
		}},
		{"containers", func() (map[string][]byte, error) { d, e := t.containers(ctx); return one("containers.json", d, e) }},
		{"resources", func() (map[string][]byte, error) { return collectResources(ctx, t.c.COSI, &problems) }},
	}

	for _, s := range steps {
		if err := try(s.what, s.fn); err != nil {
			return problems, err
		}
	}

	return problems, nil
}

// marshalJSON passes a failed call's error on, else renders its reply as indented JSON.
func marshalJSON(m proto.Message, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}

	return protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(m)
}

// readData reads a Dmesg or Logs stream to its end, keeping at most
// bundleEntryMaxBytes (the beginning is dropped, the end kept: the end is what
// matters in a log).
func readData(stream dataStream) ([]byte, error) {
	var buf bytes.Buffer

	for {
		d, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return buf.Bytes(), err
		}

		buf.Write(d.GetBytes())

		if buf.Len() > 2*bundleEntryMaxBytes {
			keep := bytes.Clone(buf.Bytes()[buf.Len()-bundleEntryMaxBytes:])
			buf.Reset()
			buf.Write(keep)
		}
	}

	out := buf.Bytes()
	if len(out) > bundleEntryMaxBytes {
		out = out[len(out)-bundleEntryMaxBytes:]
	}

	return out, nil
}

func (t *machineryDownloadClient) dmesg(ctx context.Context) ([]byte, error) {
	stream, err := t.c.Dmesg(ctx, false, false)
	if err != nil {
		return nil, err
	}

	raw, err := readData(stream)
	if err != nil {
		return nil, err
	}

	return redactKernelLog(raw), nil
}

// redactKernelLog masks credential-shaped arguments on the kernel's
// "Command line:" lines (talos.config=<url with a token>), the same rule
// GetNodeTalosDetails applies to the arguments it shows (docs/adr/0073).
func redactKernelLog(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")

	for i, l := range lines {
		if strings.Contains(strings.ToLower(l), "command line:") {
			lines[i] = strings.Join(redactKernelArgs(strings.Fields(l)), " ")
		}
	}

	return []byte(strings.Join(lines, "\n"))
}

func (t *machineryDownloadClient) serviceLogs(ctx context.Context, problems *[]string) (map[string][]byte, error) {
	list, err := t.c.ServiceList(ctx)
	if err != nil {
		return nil, err
	}

	type svc struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Healthy *bool  `json:"healthy,omitempty"`
	}

	var (
		infos []svc
		out   = map[string][]byte{}
	)

	for _, msg := range list.GetMessages() {
		for _, s := range msg.GetServices() {
			item := svc{ID: s.GetId(), State: s.GetState()}
			if h := s.GetHealth(); h != nil && !h.GetUnknown() {
				item.Healthy = ptr.To(h.GetHealthy())
			}

			infos = append(infos, item)
		}
	}

	out["services.yaml"], _ = yaml.Marshal(infos)

	for _, s := range infos {
		stream, err := t.c.Logs(ctx, constants.SystemContainerdNamespace, common.ContainerDriver_CONTAINERD, s.ID, false, bundleLogTailLines)
		if err != nil {
			*problems = append(*problems, "log of "+s.ID+": "+truncateText(err.Error(), 300))

			continue
		}

		raw, err := readData(stream)
		if err != nil {
			*problems = append(*problems, "log of "+s.ID+": "+truncateText(err.Error(), 300))
		}

		out["logs/"+path.Base(s.ID)+".log"] = raw
	}

	return out, nil
}

// processes keeps what the machine page keeps (docs/adr/0082): never the
// command line, arguments or cgroup label, which can carry a token.
func (t *machineryDownloadClient) processes(ctx context.Context) ([]byte, error) {
	resp, err := t.c.Processes(ctx)
	if err != nil {
		return nil, err
	}

	type proc struct {
		PID      int32   `json:"pid"`
		PPID     int32   `json:"ppid"`
		State    string  `json:"state"`
		Threads  int32   `json:"threads"`
		CPU      float64 `json:"cpuSeconds"`
		Resident uint64  `json:"residentBytes"`
		Virtual  uint64  `json:"virtualBytes"`
		Name     string  `json:"name"`
	}

	var procs []proc

	for _, msg := range resp.GetMessages() {
		for _, p := range msg.GetProcesses() {
			if len(procs) >= bundleMaxProcesses {
				break
			}

			procs = append(procs, proc{
				PID: p.GetPid(), PPID: p.GetPpid(), State: p.GetState(), Threads: p.GetThreads(), CPU: p.GetCpuTime(),
				Resident: p.GetResidentMemory(), Virtual: p.GetVirtualMemory(), Name: path.Base(p.GetExecutable()),
			})
		}
	}

	return mustJSON(procs), nil
}

func (t *machineryDownloadClient) containers(ctx context.Context) ([]byte, error) {
	var (
		out  []map[string]any
		errs []error
	)

	for _, src := range []struct {
		ns     string
		driver common.ContainerDriver
	}{{"k8s.io", common.ContainerDriver_CRI}, {constants.SystemContainerdNamespace, common.ContainerDriver_CONTAINERD}} {
		resp, err := t.c.Containers(ctx, src.ns, src.driver)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, msg := range resp.GetMessages() {
			for _, c := range msg.GetContainers() {
				out = append(out, map[string]any{
					"namespace": c.GetNamespace(), "id": c.GetId(), "pod": c.GetPodId(), "name": c.GetName(),
					"image": sanitizeImage(c.GetImage()), "status": c.GetStatus(), "pid": c.GetPid(),
				})
			}
		}
	}

	if len(errs) == 2 {
		return nil, errors.Join(errs...)
	}

	return mustJSON(out), nil
}

// resources writes every non-sensitive COSI resource type as one YAML file
// under resources/<namespace>/<type>.yaml.
func collectResources(ctx context.Context, st state.State, problems *[]string) (map[string][]byte, error) {
	defs, err := st.List(ctx, resource.NewMetadata(meta.NamespaceName, meta.ResourceDefinitionType, "", resource.VersionUndefined))
	if err != nil {
		return nil, err
	}

	out := map[string][]byte{}

	for _, item := range defs.Items {
		def, ok := item.(*meta.ResourceDefinition)
		if !ok {
			continue
		}

		spec := def.TypedSpec()
		if spec.Sensitivity == meta.Sensitive || excludedNamespaces[spec.DefaultNamespace] || excludedTypes[spec.Type] {
			continue
		}

		list, err := st.List(ctx, resource.NewMetadata(spec.DefaultNamespace, spec.Type, "", resource.VersionUndefined))
		if err != nil {
			*problems = append(*problems, "resource "+spec.Type+": "+truncateText(err.Error(), 300))

			continue
		}

		var buf bytes.Buffer

		for i, r := range list.Items {
			if i >= bundleMaxPerType {
				break
			}

			doc, err := resource.MarshalYAML(r)
			if err != nil {
				continue
			}

			raw, err := yaml.Marshal(doc)
			if err != nil {
				continue
			}

			buf.WriteString("---\n")
			buf.Write(raw)
		}

		if buf.Len() > 0 {
			out[fmt.Sprintf("resources/%s/%s.yaml", spec.DefaultNamespace, spec.Type)] = buf.Bytes()
		}
	}

	return out, nil
}

func mustJSON(v any) []byte {
	// ponytail: encoding/json of plain structs and maps cannot fail.
	raw, _ := json.MarshalIndent(v, "", "  ")

	return raw
}
