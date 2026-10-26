package chart

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const healthConfigMap = "ichor-argocd-health"

// luaRun executes the health script the way Argo CD does: a chunk that reads the global
// obj and returns a table. Argo CD uses gopher-lua (Lua 5.1), as does flake.nix's lua5_1.
func luaRun(t *testing.T, script, obj string) (status, message string) {
	t.Helper()
	prog := `obj = ` + obj + `
local hs = assert(loadfile("` + script + `"))()
print(hs.status)
print(hs.message or "")`
	out, err := exec.Command("lua", "-e", prog).CombinedOutput()
	if err != nil {
		t.Fatalf("lua %s: %v\n%s", script, err, out)
	}
	lines := strings.SplitN(strings.TrimRight(string(out), "\n"), "\n", 2)
	if len(lines) < 2 {
		lines = append(lines, "")
	}
	return lines[0], lines[1]
}

func cond(typ, status, reason string) string {
	return `{type="` + typ + `",status="` + status + `",reason="` + reason + `",message="m"}`
}

func TestArgoCDHealthScripts(t *testing.T) {
	machine := func(gen, seen string, conds ...string) string {
		return `{metadata={generation=` + gen + `},status={observedGeneration=` + seen +
			`,conditions={` + strings.Join(conds, ",") + `}}}`
	}
	cluster := func(conds ...string) string { return `{status={conditions={` + strings.Join(conds, ",") + `}}}` }
	ready := func(status, reason string) string { return machine("1", "1", cond("Ready", status, reason)) }
	const (
		hP, hH, hD = "Progressing", "Healthy", "Degraded"
		m, c, st   = "TalosMachine", "TalosCluster", "TalosStorage"
	)
	deleting := `{metadata={generation=1,deletionTimestamp="2026-01-01T00:00:00Z"}}`
	warn := cond("ConfigWarnings", "True", "DryRunWarnings")
	cases := []struct {
		file, name, obj, want string
	}{
		{m, "no status yet", `{metadata={generation=1}}`, hP},
		{m, "no Ready condition", machine("1", "1"), hP},
		{m, "deleting", deleting, hP},
		{m, "stale generation", machine("2", "1", cond("Ready", "True", "Converged")), hP},
		{m, "converged", ready("True", "Converged"), hH},
		{m, "applied", ready("True", "Applied"), hH},
		{m, "lock held", ready("False", "ClusterLockHeld"), hP},
		{m, "cluster transitioning", ready("False", "ClusterTransitioning"), hP},
		{m, "apply failed", ready("False", "ApplyFailed"), hD},
		{m, "reboot required", ready("False", "RebootRequired"), hD},
		{m, "drifted", ready("False", "NodeDrifted"), hD},
		{m, "other condition ignored", machine("1", "1", warn, cond("Ready", "True", "Converged")), hH},
		{st, "no status yet", `{metadata={generation=1}}`, hP},
		{st, "deleting", deleting, hP},
		{st, "stale generation", machine("2", "1", cond("Ready", "True", "Prepared")), hP},
		{st, "prepared", ready("True", "Prepared"), hH},
		{st, "nodes not converged is a wait", ready("False", "NodesNotConverged"), hP},
		{st, "no nodes selected holds the wave", ready("False", "NoNodesSelected"), hP},
		{st, "cluster missing", ready("False", "ClusterNotFound"), hD},
		{st, "storage system mismatch never gates",
			machine("1", "1", cond("Ready", "True", "Prepared"),
				cond("StorageSystemConsistent", "False", "SettingDisabled")), hH},
		{c, "no status", `{}`, hH},
		{c, "stable", cluster(cond("Transitioning", "False", "StableControlPlaneCount")), hH},
		{c, "CNI handover false is normal", cluster(cond("CNIHandover", "False", "ManualPending")), hH},
		{c, "even control planes", cluster(cond("Transitioning", "True", "EvenControlPlaneCount")), hP},
	}
	for _, tc := range cases {
		t.Run(tc.file+"/"+tc.name, func(t *testing.T) {
			got, msg := luaRun(t, "files/argocd-health/"+tc.file+".lua", tc.obj)
			if got != tc.want {
				t.Errorf("status = %q (%s), want %q", got, msg, tc.want)
			}
		})
	}
}

// Disabled renders nothing; enabled renders one ConfigMap whose keys are argocd-cm's
// resource.customizations.health.<group>_<kind> and whose values are the scripts verbatim.
func TestArgoCDHealthOption(t *testing.T) {
	if find(renderChart(t, "ichor"), "ConfigMap", healthConfigMap) != nil {
		t.Fatalf("argocdHealth.enabled defaults to false but %s rendered", healthConfigMap)
	}
	cm := find(renderChartWithValues(t, "ichor", "argocdHealth.enabled=true"), "ConfigMap", healthConfigMap)
	if cm == nil {
		t.Fatalf("argocdHealth.enabled=true: no ConfigMap %s", healthConfigMap)
	}
	data, _ := cm["data"].(map[string]any)
	for _, kind := range []string{"TalosCluster", "TalosMachine", "TalosStorage"} {
		src, err := os.ReadFile("files/argocd-health/" + kind + ".lua")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := data["resource.customizations.health.ichor.maesh.dev_"+kind].(string)
		if strings.TrimSpace(got) != strings.TrimSpace(string(src)) {
			t.Errorf("%s: rendered key missing or differs from the .lua file", kind)
		}
	}
	if len(data) != 3 {
		t.Errorf("want exactly 3 keys (TalosConfigPatch/TalosOperation get none), got %d", len(data))
	}
}
