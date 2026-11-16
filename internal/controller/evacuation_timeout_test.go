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
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// hungEvacuator blocks every Resolve until its context ends.
type hungEvacuator struct{ StorageEvacuator }

func (hungEvacuator) Resolve(ctx context.Context, _ *ichorv1alpha1.TalosCluster, _ string) (*evacuationTarget, error) {
	<-ctx.Done()

	return nil, ctx.Err()
}

// returnsAtOnce finishes every Resolve with nothing to evacuate, unless its context has already ended.
type returnsAtOnce struct{ StorageEvacuator }

func (returnsAtOnce) Resolve(ctx context.Context, _ *ichorv1alpha1.TalosCluster, _ string) (*evacuationTarget, error) {
	return nil, ctx.Err()
}

// The storage bound is its own constant: with etcdDialTimeout at zero, a call that finishes at once still succeeds.
func TestStorageCallBoundIsNotTheEtcdDialTimeout(t *testing.T) {
	old := etcdDialTimeout
	etcdDialTimeout = 0

	t.Cleanup(func() { etcdDialTimeout = old })

	m := &ichorv1alpha1.TalosMachine{}
	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionStorageEvacuation, Status: metav1.ConditionTrue, Reason: reasonEvacuating,
	})

	if _, err := evacuateMachine(t.Context(), nil, returnsAtOnce{}, nil, m, "node", "test"); err != nil {
		t.Errorf("evacuateMachine: %v", err)
	}

	if err := endMachineEvacuation(t.Context(), nil, returnsAtOnce{}, nil, m, "node"); err != nil {
		t.Errorf("endMachineEvacuation: %v", err)
	}
}

func TestEvacuationCallsAreBounded(t *testing.T) {
	old := storageCallTimeout
	storageCallTimeout = 50 * time.Millisecond

	t.Cleanup(func() { storageCallTimeout = old })

	m := &ichorv1alpha1.TalosMachine{}
	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionStorageEvacuation, Status: metav1.ConditionTrue, Reason: reasonEvacuating,
	})

	for name, call := range map[string]func() error{
		"evacuateMachine": func() error {
			_, err := evacuateMachine(t.Context(), nil, hungEvacuator{}, nil, m, "node", "test")

			return err
		},
		"endMachineEvacuation": func() error {
			return endMachineEvacuation(t.Context(), nil, hungEvacuator{}, nil, m, "node")
		},
	} {
		done := make(chan error, 1)

		go func() { done <- call() }()

		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: a hung Resolve returned no error", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: a hung Resolve was not bounded", name)
		}
	}
}
