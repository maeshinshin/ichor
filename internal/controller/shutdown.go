package controller

// shuttingDownError is the refusal of every disruptive operation while a
// shutdown of the cluster has not completed.
type shuttingDownError struct{ operation string }

func (e *shuttingDownError) Error() string { return "cluster is shutting down (operation " + e.operation + ")" }
