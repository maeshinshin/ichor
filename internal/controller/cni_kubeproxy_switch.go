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
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// KubeProxyAdmin removes the kube-proxy objects Talos leaves behind.
type KubeProxyAdmin interface {
	RemoveKubeProxy(ctx context.Context, talos TalosClient) error
}

type cniKubeProxyAdvancer struct {
	client.Client
	Reader              client.Reader
	ControllerNamespace string
	TalosClient         func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (TalosClient, error)
	Admin KubeProxyAdmin
}

func (a *cniKubeProxyAdvancer) Advance(
	context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosOperation,
) (bool, error) {
	return false, errors.New("not implemented")
}

const (
	conditionAwaitingCNIChange = "AwaitingCNIChange"
	conditionVerifyingNewSide  = "VerifyingNewSide"

	kubeProxyVerifyTimeout = 30 * time.Minute
	cniChangeTimeout       = 24 * time.Hour

	ciliumConfigName = "cilium-config"
	ciliumName       = "cilium"
)
