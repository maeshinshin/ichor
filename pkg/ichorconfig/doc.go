// Package ichorconfig is Ichor's own configuration schema for a cluster: one nested object, written the way a
// person would write a cluster's configuration, from which the controller builds the Talos configuration
// documents when it applies it. zz_generated.config.go is written by internal/tools/ichorgen from the
// mirror types of pkg/typedconfig, so a field Talos adds to a document appears here without anyone editing a list.
//
// +kubebuilder:object:generate=true
package ichorconfig
