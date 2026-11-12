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

// Package embedded holds the struct shapes TestEmbeddedStructsAreFlattened feeds the generator: a struct
// embedded by value with the yaml inline option, the way Talos's link and probe documents embed their
// common fields.
package embedded

import (
	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Common is embedded in Outer.
type Common struct {
	Up  *bool  `yaml:"up,omitempty"`
	MTU uint32 `yaml:"mtu,omitempty"`
}

// Outer embeds Common between two fields of its own.
type Outer struct {
	Name   string `yaml:"name"`
	Common `yaml:",inline"`
	Extra  string `yaml:"extra,omitempty"`
}

// WithPayload keeps its typed fields and loses one free-form field.
type WithPayload struct {
	Name string             `yaml:"name"`
	Raw  mmeta.Unstructured `yaml:"raw"`
}

// OnlyPayload is a document that is nothing but a free-form field.
type OnlyPayload struct {
	mmeta.Meta `yaml:",inline"`
	Raw        mmeta.Unstructured `yaml:"raw"`
}

// Inner is nested in Upstream and carries a field of its own to omit.
type Inner struct {
	A string `json:"a,omitempty"`
	B string `json:"b,omitempty"`
}

// Upstream stands for a Kubernetes configuration type that a subset override mirrors in part. Its embedded
// metav1.TypeMeta is not a field of the subset.
type Upstream struct {
	metav1.TypeMeta `json:",inline"`
	Keep            string      `json:"keep,omitempty"`
	Drop            string      `json:"drop,omitempty"`
	Nested          *Inner      `json:"nested,omitempty"`
	When            metav1.Time `json:"when,omitempty"`
}
