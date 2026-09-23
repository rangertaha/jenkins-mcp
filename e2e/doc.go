// SPDX-License-Identifier: GPL-3.0-or-later

// Package e2e holds end-to-end tests that drive the compiled jenkins binary
// over real JSON-RPC stdio against a real Jenkins instance in Docker.
//
// Every test file here is behind the "e2e" build tag, so `go test ./...`
// never runs them and the default suite stays hermetic and Docker-free. Run
// them with `make e2e` (or `go test -tags=e2e ./e2e/...`). This file carries
// no build tag purely so the package always has at least one buildable file,
// which keeps `go build ./...` and `go vet ./...` happy when the tag is off.
package e2e
