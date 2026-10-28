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

package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// defaultHandoverWait bounds the wait for the Cilium Helm Job (ADR 0099).
const defaultHandoverWait = 20 * time.Minute

// waitForCNIHandover polls the TalosCluster until CNIHandover is True: Cilium is then a Helm
// release owned by Ichor and the inline manifest is gone from the rendered configuration.
func waitForCNIHandover(
	ctx context.Context, c client.Client, key types.NamespacedName, timeout, interval time.Duration, progress io.Writer,
) error {
	return pollWhile(ctx, timeout, interval, progress, "Cilium to be handed over to Ichor ("+render.ConditionCNIHandover+")", func(ctx context.Context) error {
		var cluster ichorv1alpha1.TalosCluster
		if err := c.Get(ctx, key, &cluster); err != nil {
			return err
		}

		cond := apimeta.FindStatusCondition(cluster.Status.Conditions, render.ConditionCNIHandover)
		if cond == nil {
			return fmt.Errorf("the controller has not reported %s yet", render.ConditionCNIHandover)
		}

		if cond.Status != metav1.ConditionTrue {
			return fmt.Errorf("%s is %s (%s): %s", render.ConditionCNIHandover, cond.Status, cond.Reason, cond.Message)
		}

		return nil
	}, func(error) bool { return true })
}
