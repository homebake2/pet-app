// Package adminapi holds Go types generated from ../../open-api/admin-spec.json.
// The admin spec is separate from the client spec (open-api/spec.json) and is
// not mirrored to the frontend. Run `go generate ./...` after changing it.
package adminapi

//go:generate go tool oapi-codegen -config config.yaml ../../open-api/admin-spec.json
