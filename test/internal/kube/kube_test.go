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

//nolint:goconst // table-driven argument lists
package kube

import (
	"reflect"
	"testing"
)

// These tests only inspect the command that would be built; nothing is executed.

func TestArgsRefusesUnsetContext(t *testing.T) {
	for _, c := range []string{"", "  "} {
		if _, err := Args(c, []string{"-n", Namespace, "get", "pod"}); err == nil {
			t.Fatalf("context %q: want refusal", c)
		}
	}
	t.Setenv(ContextEnv, "")
	if err := Command("get", "pod").Err; err == nil {
		t.Fatal("Command with unset env: want Err set")
	}
	if _, err := RequireContext(); err == nil {
		t.Fatal("RequireContext with unset env: want error")
	}
}

func TestArgsPrependsContext(t *testing.T) {
	got, err := Args("lab", []string{"-n", Namespace, "get", "vmi"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--context", "lab", "-n", Namespace, "get", "vmi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	t.Setenv(ContextEnv, "lab")
	cmd := Command("get", "vmi")
	if cmd.Err != nil || cmd.Args[1] != "--context" || cmd.Args[2] != "lab" {
		t.Fatalf("Command args %v err %v", cmd.Args, cmd.Err)
	}
}

func TestArgsDeleteIsConfinedToNamespace(t *testing.T) {
	bad := [][]string{
		{"delete", "vm", "x"},
		{"-n", "default", "delete", "vm", "x"},
		{"-n", "kube-system", "delete", "pod", "x"},
		{"--namespace=prod", "delete", "pod", "x"},
		{"-n", Namespace, "delete", "vm", "--all"},
		{"delete", "vm", "-A"},
		{"-n", Namespace, "delete", "-f", "x.yaml"},
		{"-n", Namespace, "delete", "pod", "-l", "a=b"},
		{"-n", Namespace, "delete", "pod", "--context", "other"},
		{"delete", "ns", Namespace},
	}
	for _, a := range bad {
		if _, err := Args("lab", a); err == nil {
			t.Errorf("%v: want refusal", a)
		}
	}
	good := [][]string{
		{"-n", Namespace, "delete", "pod", "x", "--ignore-not-found"},
		{"--namespace=" + Namespace, "delete", "vm", "a", "b"},
	}
	for _, a := range good {
		if _, err := Args("lab", a); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
}

func TestExecWithInnerDeleteIsNotMistakenForDelete(t *testing.T) {
	// "kubectl exec ... -- kubectl delete" runs inside a pod; the outer verb is exec.
	if _, err := Args("lab", []string{"-n", Namespace, "exec", "-i", "p", "--", "kubectl", "delete", "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestInnerArgs(t *testing.T) {
	if _, err := InnerArgs("", []string{"get", "nodes"}); err == nil {
		t.Fatal("empty kubeconfig: want refusal")
	}
	if _, err := InnerArgs("/k", []string{"delete", "node", "x"}); err == nil {
		t.Fatal("inner delete: want refusal")
	}
	got, err := InnerArgs("/k", []string{"get", "nodes"})
	if err != nil || !reflect.DeepEqual(got, []string{"--kubeconfig", "/k", "get", "nodes"}) {
		t.Fatalf("got %v err %v", got, err)
	}
}
