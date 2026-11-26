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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	kindUser         = "IchorUser"
	kindInstallation = "IchorInstallation"
	kindCluster      = "TalosCluster"
	kindOperation    = "TalosOperation"
	kindBackup       = "TalosEtcdBackup"
	kindMachine      = "TalosMachine"
	kindStorage      = "TalosStorage"
	kindPatch        = "TalosConfigPatch"
)

// conditionTypesSetBy maps each condition type the controllers set to the CRD kinds it lands on. Add a row for a
// new condition: TestEveryConditionTheControllersSetHasAKind fails until you do.
var conditionTypesSetBy = map[string][]string{
	"Ready":                      {kindUser, kindBackup, kindMachine, kindStorage},
	"UpToDate":                   {kindInstallation},
	"InstallerImageUnverified":   {kindInstallation},
	"AutoPatchHalted":            {kindInstallation},
	"SettingsApplied":            {kindInstallation},
	"ImpersonationRoleReady":     {kindInstallation},
	"PasswordSet":                {kindUser},
	"StoreInCluster":             {kindBackup},
	"SharedDestination":          {kindBackup},
	"ExtensionsPresent":          {kindStorage},
	"StorageSystemConsistent":    {kindStorage},
	"ConfigRollout":              {kindMachine},
	"ConfigWarnings":             {kindMachine},
	"Provisioned":                {kindMachine},
	"NodeReachable":              {kindMachine},
	"AdoptionHeld":               {kindMachine},
	"StorageEvacuation":          {kindMachine},
	"Removal":                    {kindMachine},
	"TalosUpgrade":               {kindMachine},
	"TalosUpgradeWaiting":        {kindMachine},
	"SecureBoot":                 {kindMachine, kindCluster},
	"KernelLockdown":             {kindMachine, kindCluster},
	"Transitioning":              {kindCluster},
	"BundleBackupStale":          {kindCluster},
	"CAClientsNotRestarted":      {kindCluster},
	"CARotationAdvised":          {kindCluster},
	"CertificatesExpiring":       {kindCluster},
	"CiliumUpgradeAvailable":     {kindCluster},
	"CNIHandover":                {kindCluster},
	"CNIValuesApplied":           {kindCluster},
	"NetworkPoliciesApplied":     {kindCluster},
	"KubeletServingCertificates": {kindCluster},
	"KubeletServingVerified":     {kindCluster},
	"CARotation":                 {kindOperation},
	"KeyRotation":                {kindOperation},
	"KubernetesUpgrade":          {kindOperation},
	"RemovedAPIsInUse":           {kindOperation},
	"Shutdown":                   {kindOperation},
	"VerifyingNewSide":           {kindOperation},
	"Paused":                     {kindOperation},
	"DeletionHeld":               {kindOperation},
	"OutsideControllerNamespace": {kindUser, kindBackup, kindMachine, kindStorage, kindCluster, kindOperation, kindPatch, reclaimKind},
}

// scanConditionTypes returns the value of every string constant named by the Type of a metav1.Condition literal in
// the non-test files of internal/controller. shortcut: a Type that is a variable or a call is not seen, and the
// dynamic StaleWipeSent-<node> family is covered by the prefix constant; drop this when the types are typed constants.
func scanConditionTypes(t *testing.T) map[string]string {
	t.Helper()

	consts := map[string]string{}
	var lits []*ast.CompositeLit

	for _, dir := range []string{".", "../../api/v1alpha1", "../../pkg/render"} {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}

		for _, e := range files {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}

			f, err := parser.ParseFile(token.NewFileSet(), dir+"/"+name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}

			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.ValueSpec:
					for i, id := range n.Names {
						if i < len(n.Values) {
							if bl, ok := n.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
								consts[id.Name], _ = strconv.Unquote(bl.Value)
							}
						}
					}
				case *ast.CompositeLit:
					if dir == "." {
						if sel, ok := n.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Condition" {
							lits = append(lits, n)
						}
					}
				}

				return true
			})
		}
	}

	found := map[string]string{}

	for _, l := range lits {
		for _, e := range l.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok || kv.Key.(*ast.Ident).Name != "Type" {
				continue
			}

			var name string

			switch v := kv.Value.(type) {
			case *ast.Ident:
				name = v.Name
			case *ast.SelectorExpr:
				name = v.Sel.Name
			}

			if val, ok := consts[name]; ok {
				found[val] = name
			}
		}
	}

	return found
}

func TestEveryConditionTheControllersSetHasAKind(t *testing.T) {
	t.Parallel()

	for val, name := range scanConditionTypes(t) {
		t.Logf("%s = %s", name, val)

		if _, ok := conditionTypesSetBy[val]; !ok {
			t.Errorf("the controllers set condition %q (%s) but conditionTypesSetBy has no row for it", val, name)
		}
	}
}

// TestStatusConditionsDescriptionsNameEveryConditionOfTheirKind: a status.conditions description either names every
// condition the kind's controller sets or names none (a generic sentence such as "represent the current state").
func TestStatusConditionsDescriptionsNameEveryConditionOfTheirKind(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		kindInstallation: "ichorinstallations", kindUser: "ichorusers", reclaimKind: "machinereclaims",
		kindCluster: "talosclusters", kindPatch: "talosconfigpatches", kindBackup: "talosetcdbackups",
		kindMachine: "talosmachines", kindOperation: "talosoperations", kindStorage: "talosstorages",
	}

	for kind, plural := range files {
		desc, found, err := unstructured.NestedString(crdVersion(t, loadCRDBase(t, "ichor.maesh.dev_"+plural+".yaml")),
			"schema", "openAPIV3Schema", "properties", "status", "properties", "conditions", "description")
		if err != nil || !found {
			t.Errorf("%s: status.conditions has no description (found %v, err %v)", kind, found, err)

			continue
		}

		var named, missing []string

		for val, kinds := range conditionTypesSetBy {
			if !slices.Contains(kinds, kind) {
				continue
			}

			if strings.Contains(desc, val) {
				named = append(named, val)
			} else {
				missing = append(missing, val)
			}
		}

		if len(named) > 0 && len(missing) > 0 {
			t.Errorf("%s: status.conditions names %v but not %v", kind, named, missing)
		}
	}
}
