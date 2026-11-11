package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProgressUsesPlainOutputWhenNonInteractive(t *testing.T) {
	var out bytes.Buffer
	u := &UI{Err: &out}
	p := u.NewProgressWithCounts("copy", 10, 2)
	p.last = time.Now().Add(-2 * time.Second)
	p.Add(5)
	p.AddFile()
	p.Failed()
	got := out.String()
	if strings.Contains(got, "\r") || !strings.Contains(got, "copy stopped") || !strings.Contains(got, "50.0%") {
		t.Fatalf("non-interactive progress output = %q", got)
	}
	if strings.Contains(got, "100.0%") {
		t.Fatalf("failed operation was rendered complete: %q", got)
	}
}

func TestPrintJSONAddsStableSchemaVersion(t *testing.T) {
	var out bytes.Buffer
	u := &UI{Out: &out}
	if err := u.PrintJSON(map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(out.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value["schema_version"] != float64(JSONSchemaVersion) {
		t.Fatalf("schema version = %#v", value["schema_version"])
	}
}
