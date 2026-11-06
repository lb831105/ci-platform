package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestOpenAPISpecIsValidJSON(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(openAPISpec, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	if doc["openapi"] == nil || doc["paths"] == nil {
		t.Fatalf("missing openapi/paths: %+v", doc)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths is not an object")
	}
	for _, want := range []string{"/api/pipelines/{pid}/build", "/api/dashboard", "/api/profile/tokens"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("missing path %s", want)
		}
	}
	rec := httptest.NewRecorder()
	OpenAPISpec(rec, httptest.NewRequest("GET", "/openapi.json", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") == "" {
		t.Fatalf("spec handler = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
