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
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NewNodesReady returns the NodesReadyFunc cmd/main.go wires in: it lists the Nodes through an
// admin kubeconfig the Talos API issues (the way kubeconfigAdmin reaches the cluster), so the
// controller's own ServiceAccount needs no right on Nodes for this. The kubeconfig is fetched
// fresh on every call, so it carries whichever Kubernetes CA the node issues from at that moment.
func NewNodesReady() NodesReadyFunc {
	return func(ctx context.Context, talos TalosClient) ([]string, error) {
		cfg, err := restConfig(ctx, talos)
		if err != nil {
			return nil, err
		}

		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("building a clientset from the admin kubeconfig: %w", err)
		}

		nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing Nodes: %w", err)
		}

		if len(nodes.Items) == 0 {
			return nil, errors.New("the Kubernetes API lists no Node")
		}

		var notReady []string

		for i := range nodes.Items {
			if !nodeIsReady(&nodes.Items[i]) {
				notReady = append(notReady, nodes.Items[i].Name)
			}
		}

		return notReady, nil
	}
}

// rootCAConfigMap is the ConfigMap kube-controller-manager's root-ca-cert-publisher keeps in
// every namespace; every pod's projected ca.crt is made from it.
const rootCAConfigMap = "kube-root-ca.crt"

// RootCAPublishedFunc reports the namespaces whose kube-root-ca.crt does not yet carry the CA with
// the given fingerprint: pods there would not trust a serving certificate signed by it. A seam so
// tests never reach a cluster; cmd/main.go wires NewRootCAPublished.
type RootCAPublishedFunc func(ctx context.Context, talos TalosClient, fingerprint string) (stale []string, err error)

// NewRootCAPublished returns the RootCAPublishedFunc cmd/main.go wires in. It reads the ConfigMaps
// through the admin kubeconfig the Talos API issues, like NewNodesReady, so the controller's own
// ServiceAccount needs no cluster-wide right on ConfigMaps.
func NewRootCAPublished() RootCAPublishedFunc {
	return func(ctx context.Context, talos TalosClient, fingerprint string) ([]string, error) {
		cfg, err := restConfig(ctx, talos)
		if err != nil {
			return nil, err
		}

		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("building a clientset from the admin kubeconfig: %w", err)
		}

		maps, err := cs.CoreV1().ConfigMaps(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			FieldSelector: "metadata.name=" + rootCAConfigMap,
		})
		if err != nil {
			return nil, fmt.Errorf("listing %s ConfigMaps: %w", rootCAConfigMap, err)
		}

		if len(maps.Items) == 0 {
			return nil, fmt.Errorf("the Kubernetes API lists no %s ConfigMap", rootCAConfigMap)
		}

		var stale []string

		for i := range maps.Items {
			if !pemBundleHolds([]byte(maps.Items[i].Data["ca.crt"]), fingerprint) {
				stale = append(stale, maps.Items[i].Namespace)
			}
		}

		return stale, nil
	}
}

// pemBundleHolds reports whether a PEM bundle carries the certificate with the given fingerprint
// (the sha256 of its DER encoding, as secretsbundle.Fingerprint computes it).
func pemBundleHolds(bundle []byte, fingerprint string) bool {
	for {
		var block *pem.Block

		block, bundle = pem.Decode(bundle)
		if block == nil {
			return false
		}

		sum := sha256.Sum256(block.Bytes)
		if block.Type == pemTypeCertificate && hex.EncodeToString(sum[:]) == fingerprint {
			return true
		}
	}
}

// pemBundleIs reports whether bundle holds exactly one certificate, the one with the given fingerprint.
func pemBundleIs(bundle []byte, fingerprint string) bool {
	certs := 0

	for {
		var block *pem.Block

		block, bundle = pem.Decode(bundle)
		if block == nil {
			return certs == 1
		}

		if block.Type == pemTypeCertificate {
			certs++

			sum := sha256.Sum256(block.Bytes)
			if hex.EncodeToString(sum[:]) != fingerprint {
				return false
			}
		}
	}
}
