package chart

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const controllerDeployment = "ichor-controller-manager"

// Every port a chart value sets must reach the binary that listens on it: the Service
// targetPort and the containerPort come from the value, so the --flag must too.
func TestListeningPortValuesReachTheBinary(t *testing.T) {
	type listener struct{ value, deployment, flag, def string }

	listeners := []listener{
		{"webhook.port", controllerDeployment, "--webhook-port=", "9443"},
		{"metrics.port", controllerDeployment, "--metrics-bind-address=:", "8443"},
		{"ichorUI.port", ichorUIDeploymentName, "--addr=:", "8443"},
		{"ichorUI.users.internalPort", ichorUIDeploymentName, "--internal-addr=:", "8444"},
	}

	for _, custom := range []bool{false, true} {
		var sets []string

		want := map[string]string{}

		for i, l := range listeners {
			want[l.value] = l.def
			if custom {
				want[l.value] = fmt.Sprint(9000 + i)
				sets = append(sets, l.value+"="+want[l.value])
			}
		}

		docs := renderChartWithValues(t, "ichor", sets...)

		var targets []string

		for _, svc := range findAll(docs, "Service") {
			spec, _ := svc["spec"].(map[string]any)
			ports, _ := spec["ports"].([]any)

			for _, p := range ports {
				targets = append(targets, fmt.Sprint(p.(map[string]any)["targetPort"]))
			}
		}

		for _, l := range listeners {
			dep := find(docs, kindDeployment, l.deployment)
			containers, _ := podSpecOf(dep)["containers"].([]any)
			c, _ := containers[0].(map[string]any)

			var gotArg string

			for _, a := range containerArgs(dep) {
				if s, ok := a.(string); ok && strings.HasPrefix(s, l.flag) {
					gotArg = strings.TrimPrefix(s, l.flag)
				}
			}

			var cports []string

			ps, _ := c["ports"].([]any)
			for _, p := range ps {
				cports = append(cports, fmt.Sprint(p.(map[string]any)["containerPort"]))
			}

			w := want[l.value]
			if gotArg != w {
				t.Errorf("custom=%v %s: the binary is run with %s%q, want %s", custom, l.value, l.flag, gotArg, w)
			}

			if l.value != "metrics.port" && !slices.Contains(cports, w) { // the chart declares no containerPort for metrics
				t.Errorf("custom=%v %s: containerPorts %v lack %s", custom, l.value, cports, w)
			}

			if !slices.Contains(targets, w) {
				t.Errorf("custom=%v %s: no Service targetPort %s in %v", custom, l.value, w, targets)
			}
		}
	}
}
