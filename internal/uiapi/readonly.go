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

import "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"

// readOnlyProcedures are the calls ichor-ui serves when no sign-in is configured and
// --allow-unauthenticated is not set: they only read, and they read with
// the ServiceAccount, which is what the UI showed before there was a login. A procedure
// not listed here is refused, so a new RPC is closed by default until someone decides it
// reads only. Downloads (support bundle, etcd snapshot, packet capture, CA credentials)
// are not listed: they need an identity.
var readOnlyProcedures = map[string]bool{
	ichorv1alpha1connect.ClusterServiceListClustersProcedure:           true,
	ichorv1alpha1connect.ClusterServiceGetClusterProcedure:             true,
	ichorv1alpha1connect.MachineServiceListMachinesProcedure:           true,
	ichorv1alpha1connect.MachineServiceGetMachineProcedure:             true,
	ichorv1alpha1connect.NodeServiceGetNodeProcedure:                   true,
	ichorv1alpha1connect.NodeServiceGetEffectiveConfigProcedure:        true,
	ichorv1alpha1connect.NodeServiceGetNodeTalosDetailsProcedure:       true,
	ichorv1alpha1connect.NodeServiceGetNodeTalosRuntimeProcedure:       true,
	ichorv1alpha1connect.NodeServiceStreamNodeLogsProcedure:            true,
	ichorv1alpha1connect.SettingsServiceGetSettingsProcedure:           true,
	ichorv1alpha1connect.CniServiceGetCiliumValuesProcedure:            true,
	ichorv1alpha1connect.CniServiceValidateCiliumValuesProcedure:       true,
	ichorv1alpha1connect.CniServiceDiffCiliumValuesProcedure:           true,
	ichorv1alpha1connect.ReclaimServiceCanReclaimProcedure:             true,
	ichorv1alpha1connect.ReclaimServiceGetReclaimProcedure:             true,
	ichorv1alpha1connect.ReclaimServiceListReclaimsProcedure:           true,
	ichorv1alpha1connect.OperationServiceListOperationsProcedure:       true,
	ichorv1alpha1connect.OperationServiceGetOperationProcedure:         true,
	ichorv1alpha1connect.OperationServicePlanOperationProcedure:        true,
	ichorv1alpha1connect.OperationServiceListEtcdBackupsProcedure:      true,
	ichorv1alpha1connect.OperationServiceGetIchorInstallationProcedure: true,
	ichorv1alpha1connect.OperationServiceListOperationEventsProcedure:  true,
	ichorv1alpha1connect.UserServiceWhoAmIProcedure:                    true,
	ichorv1alpha1connect.DiagnosticsServiceGetMyPermissionsProcedure:   true,
	ichorv1alpha1connect.PatchServiceListPatchesProcedure:              true,
	ichorv1alpha1connect.PatchServiceGetPatchProcedure:                 true,
	ichorv1alpha1connect.PatchServicePreviewImpactProcedure:            true,
}
