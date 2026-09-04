package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTheFormsRenderWhatTheProfileDeclares drives static/schema.js against the
// real shipped profiles.
//
// That file is what turns a species profile into the inputs a person fills in,
// and reads them back into the two maps a record carries. Nothing else covers
// it: TestSchemaDrivesTheForms only checks that no vocabulary was hardcoded,
// which a form rendering nothing at all would also pass.
//
// It runs against the same document GET /api/schema serves, so a profile change
// is exercised here the moment it ships rather than against a fixture that
// drifts.
func TestTheFormsRenderWhatTheProfileDeclares(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found; skipping the form-rendering test")
	}

	body, _, err := schemaDocument()
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	fixture := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(fixture, body, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	out, err := exec.CommandContext(t.Context(), node,
		filepath.Join("testdata", "forms_test.js"), fixture).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("node failed: %v", err)
	}

	var got struct {
		Failures []string `json:"failures"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode node output: %v\n%s", err, out)
	}
	for _, f := range got.Failures {
		t.Error(f)
	}
}
