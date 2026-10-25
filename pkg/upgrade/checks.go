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

package upgrade

import "context"

// EtcdHealthChecker reports whether etcd is healthy enough to proceed with
// a control-plane upgrade: every member started and reachable, with enough
// of them for the cluster to keep quorum if one goes down for the upgrade
// (design section "Upgrades": "Pre-checks cover etcd health"). Deliberately
// just the contract -- answering it needs a connection to the running
// cluster, which this package does not have and is not the place for; the
// state machine that drives an actual upgrade (out of this package's
// scope, see ADR 0022) implements and calls it.
type EtcdHealthChecker interface {
	EtcdHealthy(ctx context.Context) error
}

// NodeReadinessChecker reports whether a specific node is ready to be
// upgraded (design section "Upgrades": "Pre-checks cover ... node
// readiness"). Same reasoning as EtcdHealthChecker: a contract only, since
// answering it needs to observe the running cluster.
type NodeReadinessChecker interface {
	NodeReady(ctx context.Context, node string) error
}
