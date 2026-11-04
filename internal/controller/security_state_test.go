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

	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	runtimeres "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
)

// newSecurityCOSI is a COSI state with the two resource types ReadSecurityState reads.
func newSecurityCOSI(t *testing.T) state.State {
	t.Helper()

	st := state.WrapCore(namespaced.NewState(inmem.Build))

	for _, def := range []meta.ResourceDefinitionSpec{
		runtimeres.SecurityStateExtension{}.ResourceDefinition(),
		runtimeres.KernelCmdlineExtension{}.ResourceDefinition(),
	} {
		d, err := meta.NewResourceDefinition(def)
		if err != nil {
			t.Fatal(err)
		}

		if err := st.Create(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}

	return st
}

func putSecurityState(t *testing.T, st state.State, mutate func(*runtimeres.SecurityStateSpec)) {
	t.Helper()

	r := runtimeres.NewSecurityStateSpec(runtimeres.NamespaceName)
	mutate(r.TypedSpec())

	if err := st.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func putCmdline(t *testing.T, st state.State, cmdline string) {
	t.Helper()

	r := runtimeres.NewKernelCmdline()
	r.TypedSpec().Cmdline = cmdline

	if err := st.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

// ReadSecurityState maps Talos's SecurityState and KernelCmdline resources (ADR 0123).
func TestReadSecurityState(t *testing.T) {
	t.Run("a SecureBoot node", func(t *testing.T) {
		st := newSecurityCOSI(t)
		putSecurityState(t, st, func(s *runtimeres.SecurityStateSpec) {
			s.SecureBoot, s.BootedWithUKI, s.ModuleSignatureEnforced = true, true, true
			s.UKISigningKeyFingerprint = "ab:cd"
		})
		putCmdline(t, st, "talos.platform=metal lockdown=confidentiality")

		got, err := ReadSecurityState(context.Background(), st)
		if err != nil {
			t.Fatal(err)
		}

		want := SecurityState{
			SecureBoot: true, BootedWithUKI: true, ModuleSignatureEnforced: true,
			UKISigningKeyFingerprint: "ab:cd", Cmdline: "talos.platform=metal lockdown=confidentiality",
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("a missing command line is empty, not an error", func(t *testing.T) {
		st := newSecurityCOSI(t)
		putSecurityState(t, st, func(*runtimeres.SecurityStateSpec) {})

		got, err := ReadSecurityState(context.Background(), st)
		if err != nil {
			t.Fatal(err)
		}

		if got.SecureBoot || got.Cmdline != "" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("a missing SecurityState is an error", func(t *testing.T) {
		if _, err := ReadSecurityState(context.Background(), newSecurityCOSI(t)); err == nil {
			t.Fatal("want an error")
		}
	})
}
