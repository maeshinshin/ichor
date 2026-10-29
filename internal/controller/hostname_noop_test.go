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

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	pathHostnameConfigName = "HostnameConfig.hostname"
	pathProxyDisabled      = "cluster.proxy.disabled"
	testOldValue           = "old"
)

func TestAssessChange_HostnameUnchangedNeedsNoReboot(t *testing.T) {
	for _, paths := range [][]string{
		{"HostnameConfig.auto", pathHostnameConfigName},
		{"machine.network.hostname"},
	} {
		impact := assessChange(context.Background(), &fakeTalosClient{nodeName: "Worker-1"}, "worker-1", paths, nil)
		if impact.RebootRequired {
			t.Errorf("assessChange(%v) = %+v, want no reboot for the hostname the node already has", paths, impact)
		}
	}
}

func TestAssessChange_HostnameUnchangedKeepsOtherReboots(t *testing.T) {
	paths := []string{pathHostnameConfigName, pathProxyDisabled}

	impact := assessChange(context.Background(), &fakeTalosClient{nodeName: "worker-1"}, "worker-1", paths, nil)
	if !impact.RebootRequired || len(impact.RebootPaths) != 1 || impact.RebootPaths[0] != pathProxyDisabled {
		t.Errorf("impact = %+v, want only cluster.proxy.disabled to need the reboot", impact)
	}
}

func TestAssessChange_RenameStaysRebootRequired(t *testing.T) {
	for _, paths := range [][]string{{pathHostnameConfigName}, {"machine.network.hostname"}} {
		impact := assessChange(context.Background(), &fakeTalosClient{nodeName: testOldValue}, "new", paths, nil)
		if !impact.RebootRequired {
			t.Errorf("assessChange(%v) = %+v, want reboot-required for a rename", paths, impact)
		}
	}
}

func TestAssessChange_UnknownNodeNameStaysRebootRequired(t *testing.T) {
	talos := &fakeTalosClient{nodeNameErr: errors.New("unreachable")}

	if impact := assessChange(context.Background(), talos, "worker-1", []string{pathHostnameConfigName}, nil); !impact.RebootRequired {
		t.Errorf("impact = %+v, want reboot-required when the node's name cannot be read", impact)
	}
}

func TestRenameNote(t *testing.T) {
	if got := renameNote([]string{pathProxyDisabled}); got != "" {
		t.Errorf("renameNote without a hostname path = %q, want empty", got)
	}

	if got := renameNote([]string{pathHostnameConfigName}); !strings.Contains(got, "keeps its old name") {
		t.Errorf("renameNote = %q, want the Kubernetes node name warning", got)
	}
}
