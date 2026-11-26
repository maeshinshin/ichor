package chart

import "testing"

const cpTaintKey = "node-role.kubernetes.io/control-plane"
const kindJob = "Job"

// podTemplates returns the name and pod spec of every pod template the chart renders (Deployment, Job, DaemonSet,
// StatefulSet, and a CronJob's Job template), hooks included.
func podTemplates(docs []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}

	for _, d := range docs {
		kind, _ := d["kind"].(string)
		spec, _ := d["spec"].(map[string]any)

		if kind == "CronJob" {
			spec, _ = spec["jobTemplate"].(map[string]any)["spec"].(map[string]any)
		}

		switch kind {
		case "Deployment", kindJob, "DaemonSet", "StatefulSet", "CronJob":
			tpl, _ := spec[kTemplate].(map[string]any)
			pod, _ := tpl["spec"].(map[string]any)
			meta, _ := d["metadata"].(map[string]any)
			out[kind+"/"+meta["name"].(string)] = pod
		}
	}

	return out
}

// TestPodTemplatesScheduleWhereTheManagerDoes pins that every pod the chart renders tolerates a tainted control plane
// by default, and follows manager.tolerations, nodeSelector, priorityClassName and imagePullSecrets when they are set.
func TestPodTemplatesScheduleWhereTheManagerDoes(t *testing.T) {
	pods := podTemplates(renderChartWithValues(t, "ichor"))
	if len(pods) < 3 {
		t.Fatalf("want the manager, ichor-ui and the migrate hook, got %v", pods)
	}

	for name, pod := range pods {
		found := false

		for _, tol := range asList(pod["tolerations"]) {
			if m, _ := tol.(map[string]any); m["key"] == cpTaintKey && m["effect"] == "NoSchedule" {
				found = true
			}
		}

		if !found {
			t.Errorf("%s has no toleration for %s:NoSchedule: tolerations = %v", name, cpTaintKey, pod["tolerations"])
		}
	}

	// ichor-ui has its own ichorUI.* scheduling values, so its toleration is set alongside the manager's.
	pods = podTemplates(renderChartWithValues(t, "ichor",
		"manager.tolerations[0].key=custom/taint", "manager.tolerations[0].operator=Exists",
		"manager.nodeSelector.pool=infra", "manager.priorityClassName=ichor-high",
		"manager.imagePullSecrets[0].name=pull", "ichorUI.tolerations[0].key=custom/taint",
		"ichorUI.tolerations[0].operator=Exists"))

	for name, pod := range pods {
		tols := asList(pod["tolerations"])
		if len(tols) != 1 || tols[0].(map[string]any)["key"] != "custom/taint" {
			t.Errorf("%s tolerations = %v, want the custom manager.tolerations", name, tols)
		}

		if name == "Deployment/ichor-ui" {
			continue // scheduled by ichorUI.*, not manager.*; only its tolerations are set above
		}

		if sel, _ := pod["nodeSelector"].(map[string]any); sel["pool"] != "infra" {
			t.Errorf("%s nodeSelector = %v, want pool=infra", name, pod["nodeSelector"])
		}

		if pod["priorityClassName"] != "ichor-high" {
			t.Errorf("%s priorityClassName = %v, want ichor-high", name, pod["priorityClassName"])
		}

		if len(asList(pod["imagePullSecrets"])) != 1 {
			t.Errorf("%s imagePullSecrets = %v, want one", name, pod["imagePullSecrets"])
		}
	}
}
