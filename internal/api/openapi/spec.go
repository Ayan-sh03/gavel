package openapi

import (
	"bytes"
	"net/http"
	"time"

	_ "embed"
)

//go:embed openapi.json
var spec []byte

//go:embed swagger.html
var swaggerHTML []byte

// Handler returns an HTTP handler that serves the embedded OpenAPI document.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(spec)
	})
}

// UI returns an HTTP handler that renders the Swagger UI page backed by the OpenAPI document.
func UI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "swagger.html", time.Unix(0, 0), bytes.NewReader(swaggerHTML))
	})
}

// Bytes returns the raw OpenAPI specification.
func Bytes() []byte {
	return spec
}
