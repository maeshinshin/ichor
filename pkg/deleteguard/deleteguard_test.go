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

package deleteguard

import (
	"strings"
	"testing"
)

func TestCheckDelete(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		wantErr     bool
	}{
		{name: "no annotations", annotations: nil, wantErr: true},
		{name: "annotation missing", annotations: map[string]string{"other": "x"}, wantErr: true},
		{name: "annotation false", annotations: map[string]string{AllowDeleteAnnotation: "false"}, wantErr: true},
		{name: "annotation true", annotations: map[string]string{AllowDeleteAnnotation: AllowDeleteValue}, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDelete(tc.annotations)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CheckDelete(%v) = nil, want error", tc.annotations)
				}
				if !strings.Contains(err.Error(), AllowDeleteAnnotation) {
					t.Fatalf("CheckDelete(%v) error %q does not name the annotation", tc.annotations, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckDelete(%v) = %v, want nil", tc.annotations, err)
			}
		})
	}
}
