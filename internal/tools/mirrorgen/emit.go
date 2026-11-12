package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

func (d *structDef) machExpr() string {
	return machImportAlias(d.pkgPath) + "." + d.machName
}

var versionContractRE = regexp.MustCompile(`VersionContract:\s*"(v[0-9.]+)"`)

// pinnedVersionContract reads release.Pinned.VersionContract from source: importing pkg/release would make
// the generator depend on the package it generates.
func pinnedVersionContract() string {
	_, self, _, _ := runtime.Caller(0)

	src, err := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "..", "..", "pkg", "release", "release.go"))
	if err != nil {
		fatalf("reading the pinned version contract: %v", err)
	}

	m := versionContractRE.FindSubmatch(src)
	if m == nil {
		fatalf("no VersionContract literal in pkg/release/release.go")
	}

	return string(m[1])
}

// referencePage is the Talos configuration reference page of one root kind, for the pinned version contract.
func referencePage(r rootKind) string {
	return fmt.Sprintf("https://docs.siderolabs.com/talos/%s/reference/configuration/%s/%s",
		pinnedVersionContract(), path.Base(r.pkgPath), strings.ToLower(r.kind))
}

// addPage records page on d and on every struct reachable from it.
func (d *structDef) addPage(page string) {
	if d.pages[page] {
		return
	}

	if d.pages == nil {
		d.pages = map[string]bool{}
	}

	d.pages[page] = true

	for _, f := range d.fields {
		for m := f.mapping; m != nil; m = m.elem {
			if m.structDef != nil {
				m.structDef.addPage(page)
			}
		}
	}
}

// talosPkgPrefix is the import-path prefix of Talos's own packages.
const talosPkgPrefix = "github.com/siderolabs/talos/"

// reference is the one-line pointer to the upstream type, named after the package that really defines it.
// A Talos struct shared by several root kinds has no single page, so it gets the symbol alone.
func (d *structDef) reference() string {
	switch {
	case d.pkgPath == pkgSpecs:
		return "OCI runtime-spec: " + d.machName + " — https://github.com/opencontainers/runtime-spec"
	case !strings.HasPrefix(d.pkgPath, talosPkgPrefix):
		return d.pkgPath + ": " + d.machName
	}

	for page := range d.pages {
		if len(d.pages) == 1 {
			return "Talos: " + d.machName + " — " + page
		}
	}

	return "Talos: " + d.machName
}

// mirrorTypeExprOf is the Go type expression for m on the generated mirror
// side. Nested structs are always pointers on the mirror side, whether or
// not machinery itself used a pointer there (ToMachinery/FromMachinery
// below reconcile the difference).
func mirrorTypeExprOf(m *typeMapping) string {
	switch m.kind {
	case kStruct:
		return "*" + m.structDef.name
	case kPointer:
		if m.elem.kind == kStruct {
			return "*" + m.elem.structDef.name
		}

		return "*" + mirrorTypeExprOf(m.elem)
	case kSlice:
		return "[]" + mirrorTypeExprOf(m.elem)
	case kMap:
		return "map[string]" + mirrorTypeExprOf(m.elem)
	case kPassthrough, kCast, kSubstScalar, kSubstArgValue:
		return m.mirrorExpr
	case kOverride:
		if m.overridePtr {
			return "*" + m.mirrorExpr
		}

		return m.mirrorExpr
	case kBytesCast:
		return "[]byte"
	}

	fatalf("mirrorTypeExprOf: unhandled kind %d", m.kind)

	return ""
}

// machTypeExprOf is the Go type expression for m on the machinery side,
// exactly as machinery declares it (pointer-ness and all).
func machTypeExprOf(m *typeMapping) string {
	switch m.kind {
	case kStruct:
		return m.structDef.machExpr()
	case kPointer:
		return "*" + machTypeExprOf(m.elem)
	case kSlice:
		return "[]" + machTypeExprOf(m.elem)
	case kMap:
		return "map[string]" + machTypeExprOf(m.elem)
	case kPassthrough:
		return m.machExpr
	case kCast, kBytesCast, kSubstScalar, kSubstArgValue, kOverride:
		return m.machExpr
	}

	fatalf("machTypeExprOf: unhandled kind %d", m.kind)

	return ""
}

// emitConvert appends the statements to convert src into dst for mapping m,
// in the direction given by toMachinery. src/dst are Go expressions (not
// necessarily simple identifiers); depth disambiguates loop variable names
// across nesting. fieldPath is used only for error-wrapping context.
func emitConvert(b *strings.Builder, m *typeMapping, src, dst string, toMachinery bool, depth int, fieldPath string) {
	switch m.kind {
	case kPassthrough:
		fmt.Fprintf(b, "\t%s = %s\n", dst, src)

	case kCast, kBytesCast:
		target := m.machExpr
		if !toMachinery {
			target = mirrorTypeExprOf(m)
		}

		fmt.Fprintf(b, "\t%s = %s(%s)\n", dst, target, src)

	case kSubstScalar:
		call := "machineryToMirrorScalar(" + src + ")"
		if toMachinery {
			call = fmt.Sprintf("mirrorToMachineryScalar[%s](%s)", m.machExpr, src)
		}

		emitTryAssign(b, call, depth, dst, fieldPath)

	case kSubstArgValue:
		call := "argValueFromMachinery(" + src + ")"
		if toMachinery {
			call = "argValueToMachinery(" + src + ")"
		}

		emitTryAssign(b, call, depth, dst, fieldPath)

	case kOverride:
		emitOverrideConvert(b, m, src, dst, toMachinery, depth, fieldPath)

	case kStruct:
		// Reached directly (not through kPointer, which emitPointerConvert
		// handles on its own): the machinery side is a plain struct value,
		// not a pointer.
		emitStructConvert(b, m, src, dst, toMachinery, depth, fieldPath, true)

	case kPointer:
		emitPointerConvert(b, m, src, dst, toMachinery, depth, fieldPath)

	case kSlice:
		emitContainerConvert(b, m, src, dst, toMachinery, depth, fieldPath, true)

	case kMap:
		emitContainerConvert(b, m, src, dst, toMachinery, depth, fieldPath, false)

	default:
		fatalf("emitConvert: unhandled kind %d for %s", m.kind, fieldPath)
	}
}

// emitErrCheck emits the error return that wraps err with fieldPath, indented by indent.
func emitErrCheck(b *strings.Builder, indent, fieldPath string) {
	fmt.Fprintf(b, "%sif err != nil {\n%s\treturn nil, fmt.Errorf(%q+\": %%w\", err)\n%s}\n", indent, indent, fieldPath, indent)
}

// emitTryAssign emits `v<depth>, err := call`, the error return, and dst = v<depth>.
func emitTryAssign(b *strings.Builder, call string, depth int, dst, fieldPath string) {
	v := fmt.Sprintf("v%d", depth)

	fmt.Fprintf(b, "\t%s, err := %s\n", v, call)
	emitErrCheck(b, "\t", fieldPath)
	fmt.Fprintf(b, "\t%s = %s\n", dst, v)
}

// emitOverrideConvert handles a fieldOverrides entry: mirror is an upstream
// type, machinery is always a plain meta.Unstructured value (never a
// pointer, whatever m.overridePtr says about the mirror side -- see its
// doc). Map-shaped overrides (overridePtr false, e.g. corev1.ResourceList)
// are symmetric values on both sides and round-trip a nil mirror value to a
// nil Object on their own. Struct-shaped overrides (overridePtr true) need
// an explicit nil/empty guard instead: marshaling a zero-value Go struct is
// not the same as "unset" (it still emits every non-omitempty field), so
// without the guard an absent field would come back as a populated one.
func emitOverrideConvert(b *strings.Builder, m *typeMapping, src, dst string, toMachinery bool, depth int, fieldPath string) {
	v := fmt.Sprintf("v%d", depth)

	if !m.overridePtr {
		call := fmt.Sprintf("machineryToMirrorUnstructured[%s](%s)", m.mirrorExpr, src)
		if toMachinery {
			call = "mirrorToMachineryUnstructured(" + src + ")"
		}

		emitTryAssign(b, call, depth, dst, fieldPath)

		return
	}

	if toMachinery {
		fmt.Fprintf(b, "\tif %s != nil {\n", src)
		fmt.Fprintf(b, "\t\t%s, err := mirrorToMachineryUnstructured(*%s)\n", v, src)
		emitErrCheck(b, "\t\t", fieldPath)
		fmt.Fprintf(b, "\t\t%s = %s\n", dst, v)
		fmt.Fprintf(b, "\t}\n")

		return
	}

	fmt.Fprintf(b, "\tif len(%s.Object) > 0 {\n", src)
	fmt.Fprintf(b, "\t\t%s, err := machineryToMirrorUnstructured[%s](%s)\n", v, m.mirrorExpr, src)
	emitErrCheck(b, "\t\t", fieldPath)
	fmt.Fprintf(b, "\t\t%s = &%s\n", dst, v)
	fmt.Fprintf(b, "\t}\n")
}

// emitStructConvert handles a struct reached without (valueMachinery=false,
// the common case: machinery used a pointer, or this is the slice/map
// element case which is always driven through the pointer-returning
// methods) or with (valueMachinery=true) an intervening value (non-pointer)
// on the machinery side.
func emitStructConvert(b *strings.Builder, m *typeMapping, src, dst string, toMachinery bool, depth int, fieldPath string, valueMachinery bool) {
	t := fmt.Sprintf("t%d", depth)

	if toMachinery {
		fmt.Fprintf(b, "\t%s, err := (%s).ToMachinery()\n", t, src)
		emitErrCheck(b, "\t", fieldPath)

		if valueMachinery {
			fmt.Fprintf(b, "\tif %s != nil {\n\t\t%s = *%s\n\t}\n", t, dst, t)
		} else {
			fmt.Fprintf(b, "\t%s = %s\n", dst, t)
		}

		return
	}

	srcAddr := src
	if valueMachinery {
		srcAddr = "&(" + src + ")"
	}

	fmt.Fprintf(b, "\t%s, err := %sFromMachinery(%s)\n", t, m.structDef.name, srcAddr)
	emitErrCheck(b, "\t", fieldPath)
	fmt.Fprintf(b, "\t%s = %s\n", dst, t)
}

// emitPointerConvert handles a *elem field. elem==kStruct is delegated to
// emitStructConvert directly (ToMachinery/FromMachinery are already
// nil-safe, so no extra nil check is needed there). elem==kPassthrough is a
// direct pointer assignment, since the pointee type is identical on both
// sides. Everything else needs a fresh value to point the new pointer at.
func emitPointerConvert(b *strings.Builder, m *typeMapping, src, dst string, toMachinery bool, depth int, fieldPath string) {
	elem := m.elem

	if elem.kind == kStruct {
		emitStructConvert(b, elem, src, dst, toMachinery, depth, fieldPath, false)

		return
	}

	if elem.kind == kPassthrough {
		fmt.Fprintf(b, "\t%s = %s\n", dst, src)

		return
	}

	v := fmt.Sprintf("p%dVal", depth)
	valType := machTypeExprOf(elem)

	if !toMachinery {
		valType = mirrorTypeExprOf(elem)
	}

	fmt.Fprintf(b, "\tif %s != nil {\n", src)
	fmt.Fprintf(b, "\t\tvar %s %s\n", v, valType)

	var inner strings.Builder
	emitConvert(&inner, elem, "*"+src, v, toMachinery, depth+1, fieldPath)

	for line := range strings.SplitSeq(strings.TrimRight(inner.String(), "\n"), "\n") {
		fmt.Fprintf(b, "\t%s\n", line)
	}

	fmt.Fprintf(b, "\t\t%s = &%s\n", dst, v)
	fmt.Fprintf(b, "\t}\n")
}

func emitContainerConvert(b *strings.Builder, m *typeMapping, src, dst string, toMachinery bool, depth int, fieldPath string, isSlice bool) {
	elem := m.elem

	if elem.kind == kPassthrough && m.machNamedCast == "" {
		fmt.Fprintf(b, "\t%s = %s\n", dst, src)

		return
	}

	tmp := fmt.Sprintf("c%d", depth)
	key := fmt.Sprintf("k%d", depth)
	val := fmt.Sprintf("e%d", depth)

	elemType := machTypeExprOf(elem)
	if !toMachinery {
		elemType = mirrorTypeExprOf(elem)
	}

	fmt.Fprintf(b, "\tif %s != nil {\n", src)

	if isSlice {
		fmt.Fprintf(b, "\t\t%s := make([]%s, len(%s))\n", tmp, elemType, src)
		fmt.Fprintf(b, "\t\tfor %s, %s := range %s {\n", key, val, src)
	} else {
		fmt.Fprintf(b, "\t\t%s := make(map[string]%s, len(%s))\n", tmp, elemType, src)
		fmt.Fprintf(b, "\t\tfor %s, %s := range %s {\n", key, val, src)
	}

	var inner strings.Builder

	elemDst := tmp + "[" + key + "]"
	emitConvert(&inner, elem, val, elemDst, toMachinery, depth+1, fieldPath)

	for line := range strings.SplitSeq(strings.TrimRight(inner.String(), "\n"), "\n") {
		fmt.Fprintf(b, "\t\t%s\n", line)
	}

	fmt.Fprintf(b, "\t\t}\n")

	if toMachinery && m.machNamedCast != "" {
		fmt.Fprintf(b, "\t\t%s = %s(%s)\n", dst, m.machNamedCast, tmp)
	} else {
		fmt.Fprintf(b, "\t\t%s = %s\n", dst, tmp)
	}

	fmt.Fprintf(b, "\t}\n")
}

// --- file writers -----------------------------------------------------

const header = `// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// Copyright 2026 maeshinshin.

// Code generated by mirrorgen. DO NOT EDIT.
`

func (g *generator) sortedOrder() []*structDef {
	order := slices.Clone(g.order)
	slices.SortFunc(order, func(a, b *structDef) int { return strings.Compare(a.name, b.name) })

	return order
}

func (g *generator) writeMirror(filename string) {
	var b strings.Builder

	b.WriteString(header)
	b.WriteString(`
package typedconfig

import (
	corev1 "k8s.io/api/core/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	kubeletv1 "k8s.io/kubelet/config/v1"
)
`)

	for _, def := range g.sortedOrder() {
		writeMirrorStruct(&b, def)
	}

	mustWriteFile(filename, b.String())
}

func writeMirrorStruct(b *strings.Builder, def *structDef) {
	b.WriteString("\n")

	fmt.Fprintf(b, "// %s\n", def.reference())

	fmt.Fprintf(b, "type %s struct {\n", def.name)

	for _, f := range def.fields {

		if f.optional {
			fmt.Fprintf(b, "\t// +optional\n")
		} else {
			fmt.Fprintf(b, "\t// +required\n")
		}

		jsonTag := f.jsonName
		if f.optional {
			jsonTag += ",omitempty"
		}

		fmt.Fprintf(b, "\t%s %s `json:\"%s\"`\n", f.name, mirrorTypeExprOf(f.mapping), jsonTag)
	}

	b.WriteString("}\n")
}

func (g *generator) writeConversion(filename string) {
	var b strings.Builder

	b.WriteString(header)
	b.WriteString(`
package typedconfig

import (
	"fmt"

	mx509 "github.com/siderolabs/crypto/x509"
	mcel "github.com/siderolabs/talos/pkg/machinery/cel"
	mblock "github.com/siderolabs/talos/pkg/machinery/config/types/block"
	mnetwork "github.com/siderolabs/talos/pkg/machinery/config/types/network"
	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	mruntime "github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	mnethelpers "github.com/siderolabs/talos/pkg/machinery/nethelpers"
	rblock "github.com/siderolabs/talos/pkg/machinery/resources/block"
	corev1 "k8s.io/api/core/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	kubeletv1 "k8s.io/kubelet/config/v1"
	"time"
)
`)

	for _, def := range g.sortedOrder() {
		writeStructConversion(&b, def)
	}

	mustWriteFile(filename, b.String())
}

func writeStructConversion(b *strings.Builder, def *structDef) {
	mach := def.machExpr()

	// ToMachinery
	fmt.Fprintf(b, "\n// ToMachinery converts m to the machinery %s type.\n", mach)
	fmt.Fprintf(b, "func (m *%s) ToMachinery() (*%s, error) {\n", def.name, mach)
	b.WriteString("\tif m == nil {\n\t\treturn nil, nil\n\t}\n")
	fmt.Fprintf(b, "\tout := &%s{}\n", mach)

	if def.isRoot && def.rootKind.kindConst() != "" {
		fmt.Fprintf(b, "\tout.Meta = mmeta.Meta{MetaKind: %s, MetaAPIVersion: %q}\n", def.rootKind.kindConst(), def.rootKind.apiVersion)
	}

	for i, f := range def.fields {
		emitConvert(b, f.mapping, "m."+f.name, "out."+f.name, true, i+1, def.name+"."+f.jsonName)
	}

	b.WriteString("\treturn out, nil\n}\n")

	// FromMachinery
	fmt.Fprintf(b, "\n// %sFromMachinery converts a machinery %s document to the mirror type.\n", def.name, mach)
	fmt.Fprintf(b, "func %sFromMachinery(src *%s) (*%s, error) {\n", def.name, mach, def.name)
	b.WriteString("\tif src == nil {\n\t\treturn nil, nil\n\t}\n")
	fmt.Fprintf(b, "\tout := &%s{}\n", def.name)

	for i, f := range def.fields {
		emitConvert(b, f.mapping, "src."+f.name, "out."+f.name, false, i+1, def.name+"."+f.jsonName)
	}

	b.WriteString("\treturn out, nil\n}\n")
}

func mustWriteFile(name, content string) {
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		fatalf("writing %s: %v", name, err)
	}
}

func (g *generator) report() {
	fmt.Println("mirrorgen: generated", len(g.order), "mirror types for", len(g.roots), "shipped kinds;",
		len(g.pending), "accept kinds are not shipped yet")

	for _, n := range g.notes {
		fmt.Println("mirrorgen: note:", n)
	}

	if len(g.subst) > 0 {
		names := make([]string, 0, len(g.subst))
		for n := range g.subst {
			names = append(names, n)
		}

		slices.Sort(names)

		fmt.Println("mirrorgen: substituted types (no faithful structural-schema representation):")

		for _, n := range names {
			fmt.Println("  -", n)
		}
	}

	if len(g.overridden) > 0 {
		overridden := slices.Clone(g.overridden)
		slices.Sort(overridden)

		fmt.Println("mirrorgen: field overrides (meta.Unstructured mirrored as an upstream type):")

		for _, f := range overridden {
			ov := fieldOverrides[f]
			fmt.Printf("  - %s -> %s.%s\n", f, ov.pkgPath, ov.typeName)
		}
	}

	if len(g.dropped) > 0 {
		fmt.Println("mirrorgen: dropped fields (meta.Unstructured; would reintroduce the raw-YAML hatch):")

		for _, d := range g.dropped {
			fmt.Printf("  - %s.%s (yaml %q)\n", d.Struct, d.Field, d.YAML)
		}
	}

	if len(g.droppedSecret) > 0 {
		fmt.Println("mirrorgen: dropped fields (secret material; must not be representable, not just hard to represent):")

		for _, d := range g.droppedSecret {
			fmt.Printf("  - %s.%s (yaml %q): %s\n", d.Struct, d.Field, d.YAML, d.Why)
		}
	}

	if survivors := g.suspiciousSurvivors(); len(survivors) > 0 {
		fmt.Println("mirrorgen: fields whose name suggests a secret, still in the mirror " +
			"(neither in secretFields nor reviewedSurvivors -- see suspicious.go):")

		for _, s := range survivors {
			fmt.Printf("  - %s (yaml %q)\n", s.key, s.yaml)
		}
	}
}

const unionHeader = `/*
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

// Code generated by mirrorgen. DO NOT EDIT.

package v1alpha1

import "github.com/maeshinshin/ichor/pkg/typedconfig"
`

// unionSource returns the Go source of api/v1alpha1's TalosConfigPatchDocument for the given roots: the kind
// enum, one field per root, and the two CEL rules that tie the fields to kind. controller-gen builds the CRD
// from it as from any other API type.
func unionSource(roots []rootKind) string {
	var b strings.Builder

	b.WriteString(unionHeader)
	b.WriteString(`
// TalosConfigPatchDocument carries exactly one Talos config document, restricted to Ichor's
// allow-list of kinds. kind is a discriminator: it says which of the fields below is
// populated, the same way a Talos config document's own "kind" field says what the rest of the
// document is. The kinds, the fields and the two rules below are generated by mirrorgen from
// its list of shipped kinds, which only ever grows: a field, once shipped, is never removed.
`)

	celCount, celKind, celList := unionRules(roots)

	fmt.Fprintf(&b, "// +kubebuilder:validation:XValidation:rule=%q,message=%q\n", celCount, "exactly one of "+celList+" must be set")
	fmt.Fprintf(&b, "// +kubebuilder:validation:XValidation:rule=%q,message=\"kind must match the populated field\"\n", celKind)
	b.WriteString("type TalosConfigPatchDocument struct {\n")
	b.WriteString("\t// kind selects which of the fields below is populated.\n")

	kinds := make([]string, len(roots))
	for i, r := range roots {
		kinds[i] = r.kind
	}

	fmt.Fprintf(&b, "\t// +kubebuilder:validation:Enum=%s\n", strings.Join(kinds, ";"))
	b.WriteString("\t// +unionDiscriminator\n\t// +required\n\tKind string `json:\"kind\"`\n")

	for _, r := range roots {
		what := strings.ReplaceAll(r.what, "\n", "\n\t// ")

		fmt.Fprintf(&b, "\n\t// %s is a Talos %s; populated when kind is %q.\n", fieldName(r.kind), what, r.kind)
		fmt.Fprintf(&b, "\t// +optional\n\t%s *typedconfig.%s `json:\"%s,omitempty\"`\n", r.kind, r.typeName, fieldName(r.kind))
	}

	b.WriteString("}\n")

	return b.String()
}

// unionRules returns the CEL rule that exactly one field is set, the rule that kind names the set field, and
// the human list of fields ("a, b or c") the first rule's message uses. Both rules grow with the number of
// roots, and the API server evaluates them once per documents[] element.
func unionRules(roots []rootKind) (count, kindRule, list string) {
	terms := make([]string, len(roots))
	names := make([]string, len(roots))

	for i, r := range roots {
		terms[i] = fmt.Sprintf("(has(self.%s) ? 1 : 0)", fieldName(r.kind))
		names[i] = fieldName(r.kind)
	}

	count = strings.Join(terms, " + ") + " == 1"

	// The last kind needs no test of its own: the enum rules out any other value.
	for _, r := range roots[:len(roots)-1] {
		kindRule += fmt.Sprintf("self.kind == '%s' ? has(self.%s) : ", r.kind, fieldName(r.kind))
	}

	kindRule += fmt.Sprintf("has(self.%s)", fieldName(roots[len(roots)-1].kind))

	list = strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]

	return count, kindRule, list
}
