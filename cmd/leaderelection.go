package main

import "time"

// Leader election timing. On a single control plane the API server is
// down for the whole static-pod restart of a CA rotation, longer than controller-runtime's
// 10 s RenewDeadline, so the default loses the lease and the manager exits. client-go requires
// leaseDuration > renewDeadline > retryPeriod*1.2 (its JitterFactor). A crash failover now
// takes up to leaseDuration.
var (
	leaseDuration = 120 * time.Second
	renewDeadline = 90 * time.Second
	retryPeriod   = 10 * time.Second
)
