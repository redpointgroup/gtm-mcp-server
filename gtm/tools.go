package gtm

import (
	"context"
	"fmt"

	"gtm-mcp-server/auth"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterTools adds GTM tools to the MCP server. Read tools are always
// registered; the mutating tools only when enableMutations is true.
//
// Note that registerUpdateAccount sat under a "Read operations" heading upstream
// despite calling accounts.update. It is a mutation and is gated as one.
func RegisterTools(server *mcp.Server, enableMutations bool) {
	registerReadTools(server)

	if enableMutations {
		registerMutationTools(server)
	}

	// Prompts (template workflows). Several describe write workflows, but a
	// prompt is inert text — it cannot call a tool that was never registered.
	RegisterPrompts(server)
}

// registerReadTools adds every tool that only ever issues GTM list/get calls.
// All of these accept the tagmanager.readonly scope (verified against the Tag
// Manager API v2 reference, 2026-08-31).
func registerReadTools(server *mcp.Server) {
	// Accounts, containers, workspaces
	registerListAccounts(server)
	registerListContainers(server)
	registerListWorkspaces(server)
	registerGetWorkspaceStatus(server)

	// Container entities
	registerListTags(server)
	registerGetTag(server)
	registerListTriggers(server)
	registerGetTrigger(server)
	registerListVariables(server)
	registerGetVariable(server)
	registerListFolders(server)
	registerGetFolderEntities(server)
	registerListTemplates(server)
	registerGetTemplate(server)
	registerListVersions(server)
	registerListBuiltInVariables(server)

	// Clients and transformations (server-side containers)
	registerListClients(server)
	registerGetClient(server)
	registerListTransformations(server)
	registerGetTransformation(server)

	// Static parameter-format helpers (no API call)
	registerGetTagTemplates(server)
	registerGetTriggerTemplates(server)

	// Resources (URI-based read access)
	RegisterResources(server)
}

// registerMutationTools adds every tool that creates, updates, deletes or
// publishes. Gated behind GTM_ENABLE_MUTATIONS (default off) — see
// config.Config.EnableMutations.
func registerMutationTools(server *mcp.Server) {
	// Accounts and containers
	registerUpdateAccount(server)
	registerCreateContainer(server)
	registerUpdateContainer(server)
	registerDeleteContainer(server)
	registerCreateWorkspace(server)

	// Tags, triggers, variables
	registerCreateTag(server)
	registerUpdateTag(server)
	registerDeleteTag(server)
	registerCreateTrigger(server)
	registerUpdateTrigger(server)
	registerDeleteTrigger(server)
	registerCreateVariable(server)
	registerUpdateVariable(server)
	registerDeleteVariable(server)

	// Versions — publishing is the live-site-affecting one
	registerCreateVersion(server)
	registerPublishVersion(server)

	// Templates
	registerImportGalleryTemplate(server)
	registerCreateTemplate(server)
	registerUpdateTemplate(server)
	registerDeleteTemplate(server)

	// Built-in variables
	registerEnableBuiltInVariables(server)
	registerDisableBuiltInVariables(server)

	// Clients (server-side containers)
	registerCreateClient(server)
	registerUpdateClient(server)
	registerDeleteClient(server)

	// Transformations (server-side containers)
	registerCreateTransformation(server)
	registerUpdateTransformation(server)
	registerDeleteTransformation(server)
}

// getClient creates a GTM client from the request context.
// In S2S mode the shared service account token source is used directly.
// In OAuth mode an auto-refreshing token source is built from the user's Google token.
func getClient(ctx context.Context) (*Client, error) {
	// S2S mode: shared service account token source injected by middleware
	if saTS := auth.GetSATokenSource(ctx); saTS != nil {
		return NewClient(ctx, saTS)
	}

	// OAuth mode: build auto-refreshing token source from user's Google token
	tokenInfo := auth.GetTokenInfo(ctx)
	if tokenInfo == nil || tokenInfo.GoogleToken == nil {
		return nil, fmt.Errorf("not authenticated - please authenticate with Google first")
	}

	store := auth.GetTokenStore(ctx)
	google := auth.GetGoogleProvider(ctx)

	var tokenSource = auth.NewAutoRefreshTokenSource(
		store,
		tokenInfo.AccessToken,
		google.Config(),
		tokenInfo.GoogleToken,
	)

	return NewClient(ctx, tokenSource)
}
