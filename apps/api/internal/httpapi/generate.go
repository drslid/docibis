// Package httpapi serves the contract in openapi.yaml: the code that binds
// requests is generated (api.gen.go), the handlers are written here.
package httpapi

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config oapi-codegen.yaml ../../openapi.yaml
