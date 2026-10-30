package main

import "time"

// Leader election timing. On a single control plane the API server is
// down for the whole static-pod restart of a CA rotation, longer than controller-runtime's
// 10 s RenewDeadline, so the default loses the lease and the manager exits. client-go requires
// leaseDuration > renewDeadline > retryPeriod*1.2 (its JitterFactor). A crash failover now
// takes up to leaseDuration.
//
// releaseLeaseOnCancel hands the lease over when the manager stops, so a restarted controller (an
// upgrade) takes over at once instead of after leaseDuration. When the API server is gone (an etcd
// restore) the release fails and the lease runs out as before.
var (
	releaseLeaseOnCancel = true

	leaseDuration = 120 * time.Second
	renewDeadline = 90 * time.Second
	retryPeriod   = 10 * time.Second
)
