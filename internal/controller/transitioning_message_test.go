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

import "testing"

func TestTransitioningMessageAgreesInNumber(t *testing.T) {
	for n, want := range map[int]string{
		0: "0 control planes approved right now",
		1: "1 control plane approved right now",
		2: "2 control planes approved right now",
	} {
		if got := transitioningMessage(n); got != want {
			t.Errorf("transitioningMessage(%d) = %q, want %q", n, got, want)
		}
	}
}
