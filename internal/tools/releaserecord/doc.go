// Command releaserecord measures the Helm release record of dist/chart and fails when it exceeds the
// 1,048,576 bytes a Secret can hold. Helm stores each release in a Secret whose data.release is the record;
// past the limit "helm install" fails on the user's cluster, so the figure is gated here.
//
// Run via "make check-release-record" from the repository root; CI runs it in the test job.
package main
