// This file exists only so the root Go module's `./...` never walks into
// ui/ (a Vite/Preact frontend, not Go). Without it, npm dependencies that
// happen to vendor Go source — e.g. flatted's golang port under
// node_modules — would be picked up by `go build ./...`, `go vet ./...`
// and `go test ./...`, which could break Go-only workflows on any future
// `npm install` churn (ADR 0017).
module ichor/ui-not-a-go-module

go 1.27
