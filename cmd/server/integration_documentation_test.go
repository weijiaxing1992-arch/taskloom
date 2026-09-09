package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No App, server, credential or database is constructed. Both public OpenAPI
// endpoints and this export use the same in-process schema implementation.
func TestIntegrationDocumentationOpenAPISnapshot(t *testing.T) {
	want, err := json.MarshalIndent(integrationOpenAPISpec(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("..", "..", "docs", "openapi.json")
	if os.Getenv("DEVFLOW_UPDATE_OPENAPI") == "1" {
		if err := os.WriteFile(path, want, 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI export: %v; run node scripts/export-openapi.mjs", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("docs/openapi.json differs from integrationOpenAPISpec(); run node scripts/export-openapi.mjs")
	}
	for i := 0; i < 12; i++ {
		next, err := json.MarshalIndent(integrationOpenAPISpec(), "", "  ")
		if err != nil || !bytes.Equal(append(next, '\n'), want) {
			t.Fatal("OpenAPI generation is not deterministic")
		}
	}
}

func TestIntegrationDocumentationCoversPublicContract(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "docs", "api-reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(content)
	for path := range integrationOpenAPISpec()["paths"].(map[string]any) {
		if !strings.Contains(doc, "`"+path+"`") {
			t.Errorf("API reference is missing public path %s", path)
		}
	}
	scopes := make([]string, 0, len(integrationScopes))
	for _, scope := range integrationScopes {
		key := scope["key"]
		scopes = append(scopes, key)
		if !strings.Contains(doc, "`"+key+"`") {
			t.Errorf("API reference is missing scope %s", key)
		}
	}
	for _, tool := range integrationMCPTools(scopes) {
		if !strings.Contains(doc, "`"+tool.Name+"`") {
			t.Errorf("API reference is missing MCP tool %s", tool.Name)
		}
	}
	for resource := range integrationResources {
		for field := range integrationOpenAPIWriteSchema(resource, false)["properties"].(map[string]any) {
			if !strings.Contains(doc, "`"+field+"`") {
				t.Errorf("API reference is missing %s field %s", resource, field)
			}
		}
	}
	for _, important := range []string{"internal-api-reference.md", "ai-collaboration.md", "openapi.json", "If-Match", "Idempotency-Key", "operation_pending", "replyToId", "actualResult", "DEVFLOW_UPDATE_OPENAPI"} {
		if !strings.Contains(doc, important) {
			t.Errorf("API reference is missing contract detail %s", important)
		}
	}
}

func TestIntegrationDocumentationSchemaMatchesNativeResponseBoundaries(t *testing.T) {
	execution := integrationOpenAPIWriteSchema("executions", false)
	required, ok := execution["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "status" {
		t.Fatal("execution update must document required status")
	}
	paths := integrationOpenAPISpec()["paths"].(map[string]any)
	iteration := paths["/iterations/{id}"].(map[string]any)["patch"].(map[string]any)
	response := iteration["responses"].(map[string]any)["200"].(map[string]any)
	schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["properties"].(map[string]any)["sprint"] == nil {
		t.Fatal("iteration PATCH must document the native sprint response envelope")
	}
}
