package gtm

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mutatingPrefixes are the verbs that begin every tool name capable of changing
// a container. Every read tool is named list_* or get_*, so the two sets do not
// overlap and a prefix test is exact rather than heuristic.
var mutatingPrefixes = []string{
	"create_", "update_", "delete_", "publish_",
	"import_", "enable_", "disable_",
}

func isMutating(name string) bool {
	for _, p := range mutatingPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// registeredToolNames asks a live session what tools the server advertises.
// Asserting against the registration source would only restate what the code
// says; this observes what an MCP client would actually be offered.
func registeredToolNames(t *testing.T, enableMutations bool) map[string]bool {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "test"}, nil)
	RegisterTools(server, enableMutations)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	names := map[string]bool{}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		names[tool.Name] = true
	}
	if len(names) == 0 {
		t.Fatal("no tools advertised at all — the reader is broken, not the gate")
	}
	return names
}

// TestReadOnlyByDefault is the load-bearing assertion: with mutations disabled,
// nothing that can change a container is advertised.
func TestReadOnlyByDefault(t *testing.T) {
	names := registeredToolNames(t, false)

	for name := range names {
		if isMutating(name) {
			t.Errorf("mutating tool %q advertised in read-only mode", name)
		}
	}
}

// TestReadToolsPresentInReadOnlyMode guards the opposite failure: a gate that
// achieves read-only by withholding everything would pass the test above.
// These are the tools the Phase 1 container inventory depends on.
func TestReadToolsPresentInReadOnlyMode(t *testing.T) {
	names := registeredToolNames(t, false)

	required := []string{
		"list_accounts", "list_containers", "list_workspaces",
		"list_tags", "get_tag",
		"list_triggers", "get_trigger",
		"list_variables", "get_variable",
		"list_built_in_variables",
		"list_folders", "get_folder_entities",
		"list_templates", "get_template",
		"list_versions", "get_workspace_status",
		"list_clients", "get_client",
		"list_transformations", "get_transformation",
	}
	for _, want := range required {
		if !names[want] {
			t.Errorf("read tool %q missing in read-only mode", want)
		}
	}
}

// TestMutationsRestorableByFlag proves the gate is a gate and not a deletion,
// so Phase 3 is an env-var change rather than a code revert.
func TestMutationsRestorableByFlag(t *testing.T) {
	readOnly := registeredToolNames(t, false)
	withWrites := registeredToolNames(t, true)

	var restored int
	for name := range withWrites {
		if !isMutating(name) {
			continue
		}
		restored++
		if readOnly[name] {
			t.Errorf("tool %q leaked into read-only mode", name)
		}
	}
	if restored == 0 {
		t.Fatal("enabling mutations advertised no mutating tools — the flag is not wired")
	}

	// Enabling writes must add tools, never remove or rename a read tool.
	for name := range readOnly {
		if !withWrites[name] {
			t.Errorf("read tool %q disappeared when mutations were enabled", name)
		}
	}

	t.Logf("read-only advertises %d tools; enabling mutations adds %d", len(readOnly), restored)
}
