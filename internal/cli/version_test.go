package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/midas-cli/midas/internal/config"
	"github.com/midas-cli/midas/internal/ui"
	"github.com/midas-cli/midas/internal/version"
)

func TestVersionJSONFlagsAfterCommand(t *testing.T) {
	var out, stderr bytes.Buffer
	ctx := &Ctx{Cfg: config.Defaults(), UI: &ui.UI{Out: &out, Err: &stderr}}
	if err := ctx.cmdVersion([]string{"--json", "--commit"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["schema_version"] != float64(1) || got["version"] != version.Version || got["commit"] != version.Commit {
		t.Fatalf("version JSON = %v", got)
	}
}
