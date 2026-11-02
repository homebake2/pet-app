// Package webapi holds Go types generated from ../../open-api/web-spec.json.
// The web spec is separate from the client spec (open-api/spec.json): it is
// consumed by the pet-web site, not by the mobile app. Run `go generate ./...`
// after changing it.
package webapi

//go:generate go tool oapi-codegen -config config.yaml ../../open-api/web-spec.json
