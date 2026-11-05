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

package credentials

import (
	"bytes"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// sourcesWithRotation is the live bundle, a rotation of targets and the sources made from them.
func sourcesWithRotation(t *testing.T, targets ...string) (*secrets.Bundle, *secretsbundle.Rotation, *Sources) {
	t.Helper()

	contract, _ := coreconfig.ParseContractFromVersion("v1.14")

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatal(err)
	}

	rot, err := secretsbundle.NewRotationFor(time.Now(), contract, targets)
	if err != nil {
		t.Fatal(err)
	}

	src, err := NewSources(bundle, rot)
	if err != nil {
		t.Fatal(err)
	}

	return bundle, rot, src
}

// A rotation of the etcd or aggregator CA alone changes nothing an operator credential depends on:
// the sources stay the live bundle's, and are not "dual".
func TestNewSourcesIgnoresARotationOfEtcdAndAggregatorOnly(t *testing.T) {
	t.Parallel()

	bundle, _, src := sourcesWithRotation(t, "etcd", "kubernetes-aggregator")

	if src.Dual {
		t.Fatal("no CA of a credential is being replaced, so the trust lists are not dual")
	}

	if !bytes.Equal(src.TalosSigner.Crt, bundle.Certs.OS.Crt) || !bytes.Equal(src.KubernetesSigner.Crt, bundle.Certs.K8s.Crt) {
		t.Fatal("credentials must still be signed by the CAs the nodes issue from")
	}
}

// With the Talos CA alone in the rotation the Talos side is dual and the Kubernetes side is not.
func TestNewSourcesRotatesOnlyTheCAsTheRotationHolds(t *testing.T) {
	t.Parallel()

	bundle, rot, src := sourcesWithRotation(t, "talos")

	if !src.Dual || !bytes.Equal(src.TalosSigner.Crt, rot.Talos.Crt) {
		t.Fatal("the Talos credentials are signed by the new CA and trust both")
	}

	if !bytes.Equal(src.KubernetesSigner.Crt, bundle.Certs.K8s.Crt) || !bytes.Equal(src.KubernetesTrust, bundle.Certs.K8s.Crt) {
		t.Fatal("the Kubernetes credentials are those of the live CA alone")
	}
}
