package controller

// ProgressFailureReasons are the reasons a progress condition (TalosUpgrade, KubernetesUpgrade ...) is False
// for a failure rather than a completed step. The CLI reads this set to mark such a condition as failed;
// TestProgressFailureReasonsCoverUpgradeReasons keeps it in step with the reason constants.
// reasonKubernetesVerifyTimedOut has the same value as reasonUpgradeVerifyTimedOut.
var ProgressFailureReasons = map[string]bool{
	reasonUpgradeFellBack:       true,
	reasonUpgradeVerifyTimedOut: true,
}
