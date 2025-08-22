package handlers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gptscript-ai/go-gptscript"
	nmcp "github.com/nanobot-ai/nanobot/pkg/mcp"
	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/accesscontrolrule"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/mcp"
	"github.com/obot-platform/obot/pkg/projects"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	"github.com/obot-platform/obot/pkg/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var envVarRegex = regexp.MustCompile(`\${([^}]+)}`)

// MCPOAuthChecker will check the OAuth status for an MCP server. This interface breaks an import cycle.
type MCPOAuthChecker interface {
	CheckForMCPAuth(ctx context.Context, server v1.MCPServer, config mcp.ServerConfig, userID, mcpID, oauthAppAuthRequestID string) (string, error)
}

type MCPHandler struct {
	mcpSessionManager *mcp.SessionManager
	mcpOAuthChecker   MCPOAuthChecker
	acrHelper         *accesscontrolrule.Helper
	serverURL         string
}

func NewMCPHandler(mcpLoader *mcp.SessionManager, acrHelper *accesscontrolrule.Helper, mcpOAuthChecker MCPOAuthChecker, serverURL string) *MCPHandler {
	return &MCPHandler{
		mcpSessionManager: mcpLoader,
		mcpOAuthChecker:   mcpOAuthChecker,
		acrHelper:         acrHelper,
		serverURL:         serverURL,
	}
}

func (m *MCPHandler) GetCatalogEntryFromDefaultCatalog(req api.Context) error {
	var (
		entry v1.MCPServerCatalogEntry
		id    = req.PathValue("entry_id")
	)

	if err := req.Get(&entry, id); err != nil {
		return err
	}

	if entry.Spec.MCPCatalogName != system.DefaultCatalog {
		return types.NewErrNotFound("MCP catalog entry not found")
	}

	// Authorization check.
	if !req.UserIsAdmin() {
		hasAccess, err := m.acrHelper.UserHasAccessToMCPServerCatalogEntry(req.User, entry.Name)
		if err != nil {
			return err
		}
		if !hasAccess {
			return types.NewErrForbidden("user is not authorized to access this catalog entry")
		}
	}

	return req.Write(convertMCPServerCatalogEntry(entry))
}

func (m *MCPHandler) ListEntriesInDefaultCatalog(req api.Context) error {
	var list v1.MCPServerCatalogEntryList
	if err := req.List(&list); err != nil {
		return err
	}

	if req.UserIsAdmin() {
		items := make([]types.MCPServerCatalogEntry, 0, len(list.Items))
		for _, entry := range list.Items {
			if entry.Spec.MCPCatalogName == system.DefaultCatalog {
				items = append(items, convertMCPServerCatalogEntry(entry))
			}
		}

		return req.Write(types.MCPServerCatalogEntryList{Items: items})
	}

	var entries []types.MCPServerCatalogEntry
	for _, entry := range list.Items {
		// For default catalog entries, check AccessControlRule authorization
		if entry.Spec.MCPCatalogName == system.DefaultCatalog {
			hasAccess, err := m.acrHelper.UserHasAccessToMCPServerCatalogEntry(req.User, entry.Name)
			if err != nil {
				return err
			}
			if hasAccess {
				entries = append(entries, convertMCPServerCatalogEntry(entry))
			}
		}
	}

	return req.Write(types.MCPServerCatalogEntryList{Items: entries})
}

func convertMCPServerCatalogEntry(entry v1.MCPServerCatalogEntry) types.MCPServerCatalogEntry {
	// Add extracted env vars directly to the entry
	addExtractedEnvVarsToCatalogEntry(&entry)

	return types.MCPServerCatalogEntry{
		Metadata:    MetadataFrom(&entry),
		Manifest:    entry.Spec.Manifest,
		Editable:    entry.Spec.Editable,
		CatalogName: entry.Spec.MCPCatalogName,
		SourceURL:   entry.Spec.SourceURL,
		UserCount:   entry.Status.UserCount,
	}
}

func (m *MCPHandler) ListServer(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	var fieldSelector kclient.MatchingFields
	if catalogID != "" {
		fieldSelector = kclient.MatchingFields{
			"spec.sharedWithinMCPCatalogName": catalogID,
		}
	} else if req.PathValue("project_id") != "" {
		t, err := getThreadForScope(req)
		if err != nil {
			return err
		}

		topMost, err := projects.GetRoot(req.Context(), req.Storage, t)
		if err != nil {
			return err
		}

		fieldSelector = kclient.MatchingFields{
			"spec.threadName": topMost.Name,
		}
	} else {
		// List servers scoped to the user.
		fieldSelector = kclient.MatchingFields{
			"spec.userID":     req.User.GetUID(),
			"spec.threadName": "",
		}
	}

	var servers v1.MCPServerList
	if err := req.List(&servers, fieldSelector); err != nil {
		return nil
	}

	credCtxs := make([]string, 0, len(servers.Items))
	if catalogID != "" {
		for _, server := range servers.Items {
			credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", catalogID, server.Name))
		}
	} else if req.PathValue("project_id") != "" {
		project, err := getProjectThread(req)
		if err != nil {
			return err
		}

		for _, server := range servers.Items {
			credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", project.Name, server.Name))
			if project.IsSharedProject() {
				// Add default credentials shared by the agent for this MCP server if available.
				credCtxs = append(credCtxs, fmt.Sprintf("%s-%s-shared", server.Spec.ThreadName, server.Name))
			}
		}
	} else {
		for _, server := range servers.Items {
			credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", req.User.GetUID(), server.Name))
		}
	}

	creds, err := req.GPTClient.ListCredentials(req.Context(), gptscript.ListCredentialsOptions{
		CredentialContexts: credCtxs,
	})
	if err != nil {
		return fmt.Errorf("failed to list credentials: %w", err)
	}

	credMap := make(map[string]map[string]string, len(creds))
	for _, cred := range creds {
		if _, ok := credMap[cred.ToolName]; !ok {
			c, err := req.GPTClient.RevealCredential(req.Context(), []string{cred.Context}, cred.ToolName)
			if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
				return fmt.Errorf("failed to find credential: %w", err)
			}
			credMap[cred.ToolName] = c.Env
		}
	}

	items := make([]types.MCPServer, 0, len(servers.Items))
	for _, server := range servers.Items {
		// Add extracted env vars to the server definition
		addExtractedEnvVars(&server)

		items = append(items, convertMCPServer(server, credMap[server.Name], m.serverURL))
	}

	return req.Write(types.MCPServerList{Items: items})
}

func (m *MCPHandler) GetServer(req api.Context) error {
	var (
		server    v1.MCPServer
		id        = req.PathValue("mcp_server_id")
		catalogID = req.PathValue("catalog_id")
	)

	if err := req.Get(&server, id); err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if server.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	var credCtxs []string
	if catalogID != "" {
		credCtxs = []string{fmt.Sprintf("%s-%s", catalogID, server.Name)}
	} else if req.PathValue("project_id") != "" {
		project, err := getProjectThread(req)
		if err != nil {
			return err
		}

		credCtxs = []string{
			fmt.Sprintf("%s-%s", project.Name, server.Name),
		}
		if project.IsSharedProject() {
			// Add default credentials shared by the agent for this MCP server if available.
			credCtxs = append(credCtxs, fmt.Sprintf("%s-%s-shared", server.Spec.ThreadName, server.Name))
		}
	} else {
		credCtxs = []string{fmt.Sprintf("%s-%s", req.User.GetUID(), server.Name)}
	}

	cred, err := req.GPTClient.RevealCredential(req.Context(), credCtxs, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	return req.Write(convertMCPServer(server, cred.Env, m.serverURL))
}

func (m *MCPHandler) DeleteServer(req api.Context) error {
	var (
		server    v1.MCPServer
		id        = req.PathValue("mcp_server_id")
		catalogID = req.PathValue("catalog_id")
	)

	if err := req.Get(&server, id); err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if server.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	if req.PathValue("project_id") != "" {
		project, err := getProjectThread(req)
		if err != nil {
			return err
		}

		// Ensure that the MCP server is in the same project as the request before deleting it.
		// This prevents chatbot users from deleting MCP servers from the agent.
		// This is necessary because in order to enable MCP servers to be shared across projects,
		// the standard authz middleware allows access to all MCP server endpoints from any "child" project
		// of the one the MCP server belongs to.
		if project.Name != server.Spec.ThreadName {
			return types.NewErrForbidden("cannot delete MCP server from this project")
		}
	}

	if err := req.Delete(&server); err != nil {
		return err
	}

	return req.Write(convertMCPServer(server, nil, m.serverURL))
}

func (m *MCPHandler) LaunchServer(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	server, serverConfig, err := serverForAction(req)
	if err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if server.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	if server.Spec.Manifest.Runtime != types.RuntimeRemote {
		if _, err = m.mcpSessionManager.ListTools(req.Context(), req.User.GetUID(), server, serverConfig); err != nil {
			if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
				return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
			}
			if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
				return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
			}
			if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
				return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
			}
			return fmt.Errorf("failed to launch MCP server: %w", err)
		}
	}

	return nil
}

func (m *MCPHandler) CheckOAuth(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	server, serverConfig, err := serverForAction(req)
	if err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if server.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	if server.Spec.Manifest.Runtime == types.RuntimeRemote {
		var are nmcp.AuthRequiredErr
		if _, err = m.mcpSessionManager.PingServer(req.Context(), req.User.GetUID(), server, serverConfig); err != nil {
			if !errors.As(err, &are) {
				return fmt.Errorf("failed to ping MCP server: %w", err)
			}
			req.WriteHeader(http.StatusPreconditionFailed)
		}
	}

	return nil
}

func (m *MCPHandler) GetOAuthURL(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	server, serverConfig, err := serverForAction(req)
	if err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if server.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	u, err := m.mcpOAuthChecker.CheckForMCPAuth(req.Context(), server, serverConfig, req.User.GetUID(), server.Name, "")
	if err != nil {
		return fmt.Errorf("failed to get OAuth URL: %w", err)
	}

	return req.Write(map[string]string{"oauthURL": u})
}

func (m *MCPHandler) GetTools(req api.Context) error {
	server, serverConfig, caps, err := serverForActionWithCapabilities(req, m.mcpSessionManager)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}
		return err
	}

	if caps.Tools == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support tools")
	}

	var allowedTools []string
	if server.Spec.ThreadName != "" {
		thread, err := getThreadForScope(req)
		if err != nil {
			return err
		}

		thread, err = projects.GetFirst(req.Context(), req.Storage, thread, func(project *v1.Thread) (bool, error) {
			return project.Spec.Manifest.AllowedMCPTools[server.Name] != nil, nil
		})
		if err != nil {
			return fmt.Errorf("failed to get project: %w", err)
		}

		allowedTools = thread.Spec.Manifest.AllowedMCPTools[server.Name]
	}

	tools, err := toolsForServer(req.Context(), m.mcpSessionManager, req.User.GetUID(), server, serverConfig, allowedTools)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}
		return fmt.Errorf("failed to list tools: %w", err)
	}

	return req.Write(tools)
}

func (m *MCPHandler) SetTools(req api.Context) error {
	thread, err := getThreadForScope(req)
	if err != nil {
		return err
	}

	var mcpServer v1.MCPServer
	if err = req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	var tools []string
	if err = req.Read(&tools); err != nil {
		return err
	}

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	credCtxs := []string{
		fmt.Sprintf("%s-%s", project.Name, mcpServer.Name),
	}
	if project.IsSharedProject() {
		// Add default credentials shared by the agent for this MCP server if available.
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s-shared", mcpServer.Spec.ThreadName, mcpServer.Name))
	}

	cred, err := req.GPTClient.RevealCredential(req.Context(), credCtxs, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}
	serverConfig, missingRequiredNames, err := mcp.ServerToServerConfig(mcpServer, project.Name, cred.Env, tools...)
	if err != nil {
		return fmt.Errorf("failed to get server config: %w", err)
	}

	if len(missingRequiredNames) > 0 {
		return types.NewErrBadRequest("MCP server %s is missing required parameters: %s", mcpServer.Name, strings.Join(missingRequiredNames, ", "))
	}

	mcpTools, err := toolsForServer(req.Context(), m.mcpSessionManager, req.User.GetUID(), mcpServer, serverConfig, tools)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}
		return fmt.Errorf("failed to render tools: %w", err)
	}

	if thread.Spec.Manifest.AllowedMCPTools == nil {
		thread.Spec.Manifest.AllowedMCPTools = make(map[string][]string)
	}

	if slices.Contains(tools, "*") {
		thread.Spec.Manifest.AllowedMCPTools[mcpServer.Name] = []string{"*"}
	} else {
		for _, t := range tools {
			if !slices.ContainsFunc(mcpTools, func(tool types.MCPServerTool) bool {
				return tool.ID == t
			}) {
				return types.NewErrBadRequest("tool %q is not a recognized tool for MCP server %q", t, mcpServer.Name)
			}
		}

		thread.Spec.Manifest.AllowedMCPTools[mcpServer.Name] = tools
	}

	if err = req.Update(thread); err != nil {
		return fmt.Errorf("failed to update thread: %w", err)
	}

	return nil
}

func (m *MCPHandler) GetResources(req api.Context) error {
	mcpServer, serverConfig, caps, err := serverForActionWithCapabilities(req, m.mcpSessionManager)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}
		return err
	}

	if caps.Resources == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support resources")
	}

	resources, err := m.mcpSessionManager.ListResources(req.Context(), req.User.GetUID(), mcpServer, serverConfig)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if strings.HasSuffix(strings.ToLower(err.Error()), "method not found") {
			return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support resources")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}

		var are nmcp.AuthRequiredErr
		if errors.As(err, &are) {
			return types.NewErrHTTP(http.StatusPreconditionFailed, "MCP server requires authentication")
		}
		return fmt.Errorf("failed to list resources: %w", err)
	}

	return req.Write(resources)
}

func (m *MCPHandler) ReadResource(req api.Context) error {
	mcpServer, serverConfig, caps, err := serverForActionWithCapabilities(req, m.mcpSessionManager)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}
		return err
	}

	if caps.Resources == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support resources")
	}

	contents, err := m.mcpSessionManager.ReadResource(req.Context(), req.User.GetUID(), mcpServer, serverConfig, req.PathValue("resource_uri"))
	if err != nil {
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if strings.HasSuffix(strings.ToLower(err.Error()), "method not found") {
			return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support resources")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}

		var are nmcp.AuthRequiredErr
		if errors.As(err, &are) {
			return types.NewErrHTTP(http.StatusPreconditionFailed, "MCP server requires authentication")
		}
		return fmt.Errorf("failed to list resources: %w", err)
	}

	return req.Write(contents)
}

func (m *MCPHandler) GetPrompts(req api.Context) error {
	mcpServer, serverConfig, caps, err := serverForActionWithCapabilities(req, m.mcpSessionManager)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}
		return err
	}

	if caps.Prompts == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support prompts")
	}

	prompts, err := m.mcpSessionManager.ListPrompts(req.Context(), req.User.GetUID(), mcpServer, serverConfig)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if strings.HasSuffix(strings.ToLower(err.Error()), "method not found") {
			return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support prompts")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}

		var are nmcp.AuthRequiredErr
		if errors.As(err, &are) {
			return types.NewErrHTTP(http.StatusPreconditionFailed, "MCP server requires authentication")
		}
		return fmt.Errorf("failed to list prompts: %w", err)
	}

	return req.Write(prompts)
}

func (m *MCPHandler) GetPrompt(req api.Context) error {
	mcpServer, serverConfig, caps, err := serverForActionWithCapabilities(req, m.mcpSessionManager)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}
		return err
	}

	if caps.Prompts == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support prompts")
	}

	var args map[string]string
	if err = req.Read(&args); err != nil {
		return fmt.Errorf("failed to read args: %w", err)
	}

	messages, description, err := m.mcpSessionManager.GetPrompt(req.Context(), req.User.GetUID(), mcpServer, serverConfig, req.PathValue("prompt_name"), args)
	if err != nil {
		if errors.Is(err, mcp.ErrHealthCheckFailed) || errors.Is(err, mcp.ErrHealthCheckTimeout) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "MCP server is not healthy, check configuration for errors")
		}
		if errors.Is(err, nmcp.ErrNoResult) || strings.HasSuffix(err.Error(), nmcp.ErrNoResult.Error()) {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "No response from MCP server, check configuration for errors")
		}
		if strings.HasSuffix(strings.ToLower(err.Error()), "method not found") {
			return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support prompts")
		}
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrHTTP(http.StatusBadRequest, nse.Error())
		}

		var are nmcp.AuthRequiredErr
		if errors.As(err, &are) {
			return types.NewErrHTTP(http.StatusPreconditionFailed, "MCP server requires authentication")
		}
		return fmt.Errorf("failed to get prompt: %w", err)
	}

	return req.Write(map[string]any{
		"messages":    messages,
		"description": description,
	})
}

func ServerFromMCPServerInstance(req api.Context, instanceID string) (v1.MCPServer, mcp.ServerConfig, error) {
	var (
		server   v1.MCPServer
		instance v1.MCPServerInstance
	)
	if err := req.Get(&instance, instanceID); err != nil {
		return server, mcp.ServerConfig{}, err
	}

	if err := req.Get(&server, instance.Spec.MCPServerName); err != nil {
		return server, mcp.ServerConfig{}, err
	}

	if server.Spec.NeedsURL {
		return server, mcp.ServerConfig{}, fmt.Errorf("mcp server %s needs to update its URL", server.Name)
	}

	addExtractedEnvVars(&server)

	var credCtx, scope string
	if server.Spec.SharedWithinMCPCatalogName != "" {
		credCtx = fmt.Sprintf("%s-%s", server.Spec.SharedWithinMCPCatalogName, server.Name)
		scope = server.Spec.SharedWithinMCPCatalogName
	} else {
		credCtx = fmt.Sprintf("%s-%s", instance.Spec.UserID, server.Name)
		scope = instance.Spec.UserID
	}

	cred, err := req.GPTClient.RevealCredential(req.Context(), []string{credCtx}, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return server, mcp.ServerConfig{}, fmt.Errorf("failed to find credential: %w", err)
	}

	serverConfig, missingConfig, err := mcp.ServerToServerConfig(server, scope, cred.Env)
	if err != nil {
		return server, mcp.ServerConfig{}, err
	}

	if len(missingConfig) > 0 {
		return server, mcp.ServerConfig{}, types.NewErrBadRequest("missing required config: %s", strings.Join(missingConfig, ", "))
	}

	return server, serverConfig, nil
}

func ServerForActionWithID(req api.Context, id string) (v1.MCPServer, mcp.ServerConfig, error) {
	var server v1.MCPServer
	if err := req.Get(&server, id); err != nil {
		return server, mcp.ServerConfig{}, err
	}

	if server.Spec.NeedsURL {
		return server, mcp.ServerConfig{}, types.NewErrBadRequest("mcp server %s needs to update its URL", server.Name)
	}

	var (
		credCtxs []string
		scope    string
	)
	if server.Spec.SharedWithinMCPCatalogName != "" {
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", server.Spec.SharedWithinMCPCatalogName, server.Name))
		scope = server.Spec.SharedWithinMCPCatalogName
	} else if server.Spec.ThreadName != "" {
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", server.Spec.ThreadName, server.Name))

		if req.PathValue("project_id") != "" {
			project, err := getProjectThread(req)
			if err != nil {
				return server, mcp.ServerConfig{}, err
			}

			if project.IsSharedProject() {
				credCtxs = append(credCtxs, fmt.Sprintf("%s-%s-shared", server.Spec.ThreadName, server.Name))
			}
		}

		scope = server.Spec.ThreadName
	} else {
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", server.Spec.UserID, server.Name))
		scope = server.Spec.UserID
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	cred, err := req.GPTClient.RevealCredential(req.Context(), credCtxs, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return server, mcp.ServerConfig{}, fmt.Errorf("failed to find credential: %w", err)
	}

	serverConfig, missingConfig, err := mcp.ServerToServerConfig(server, scope, cred.Env)
	if err != nil {
		return server, mcp.ServerConfig{}, err
	}

	if len(missingConfig) > 0 {
		return server, mcp.ServerConfig{}, types.NewErrBadRequest("missing required config: %s", strings.Join(missingConfig, ", "))
	}

	return server, serverConfig, nil
}

func serverForAction(req api.Context) (v1.MCPServer, mcp.ServerConfig, error) {
	return ServerForActionWithID(req, req.PathValue("mcp_server_id"))
}

func serverForActionWithCapabilities(req api.Context, mcpSessionManager *mcp.SessionManager) (v1.MCPServer, mcp.ServerConfig, nmcp.ServerCapabilities, error) {
	server, serverConfig, err := serverForAction(req)
	if err != nil {
		return server, serverConfig, nmcp.ServerCapabilities{}, err
	}

	caps, err := mcpSessionManager.ServerCapabilities(req.Context(), req.User.GetUID(), server, serverConfig)
	return server, serverConfig, caps, err
}

func serverManifestFromCatalogEntryManifest(isAdmin bool, entry types.MCPServerCatalogEntryManifest, input types.MCPServerManifest) (types.MCPServerManifest, error) {
	// Use the mapping function from types package to convert catalog entry to server manifest
	var userURL string
	if entry.Runtime == types.RuntimeRemote && entry.RemoteConfig != nil && entry.RemoteConfig.Hostname != "" && input.RemoteConfig != nil {
		userURL = input.RemoteConfig.URL
	}

	result, err := types.MapCatalogEntryToServer(entry, userURL)
	if err != nil {
		return types.MCPServerManifest{}, err
	}

	// If the user is an admin, they can override anything from the catalog entry.
	if isAdmin {
		result = mergeMCPServerManifests(result, input)
	}

	return result, nil
}

func mergeMCPServerManifests(existing, override types.MCPServerManifest) types.MCPServerManifest {
	if override.Name != "" {
		existing.Name = override.Name
	}
	if override.Description != "" {
		existing.Description = override.Description
	}
	if override.Icon != "" {
		existing.Icon = override.Icon
	}
	if len(override.Env) > 0 {
		existing.Env = override.Env
	}
	if override.Runtime != "" {
		existing.Runtime = override.Runtime
	}

	// Merge runtime-specific configurations
	if override.UVXConfig != nil {
		existing.UVXConfig = override.UVXConfig
	}
	if override.NPXConfig != nil {
		existing.NPXConfig = override.NPXConfig
	}
	if override.ContainerizedConfig != nil {
		existing.ContainerizedConfig = override.ContainerizedConfig
	}
	if override.RemoteConfig != nil {
		if existing.RemoteConfig == nil {
			existing.RemoteConfig = override.RemoteConfig
		} else {
			if override.RemoteConfig.URL != "" {
				existing.RemoteConfig.URL = override.RemoteConfig.URL
			}

			if len(override.RemoteConfig.Headers) > 0 {
				existing.RemoteConfig.Headers = override.RemoteConfig.Headers
			}
		}
	}

	return existing
}

func (m *MCPHandler) CreateServer(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	var input types.MCPServer
	if err := req.Read(&input); err != nil {
		return err
	}

	if input.MCPServerManifest.RemoteConfig != nil && !strings.HasPrefix(input.MCPServerManifest.RemoteConfig.URL, "http") {
		input.MCPServerManifest.RemoteConfig.URL = "https://" + input.MCPServerManifest.RemoteConfig.URL
	}

	server := v1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: system.MCPServerPrefix,
			Namespace:    req.Namespace(),
			Finalizers:   []string{v1.MCPServerFinalizer},
		},
		Spec: v1.MCPServerSpec{
			Alias:                     input.Alias,
			MCPServerCatalogEntryName: input.CatalogEntryID,
			UserID:                    req.User.GetUID(),
		},
	}

	if catalogID != "" {
		var catalog v1.MCPCatalog
		if err := req.Get(&catalog, catalogID); err != nil {
			return err
		}

		server.Spec.SharedWithinMCPCatalogName = catalogID
	}

	if input.CatalogEntryID != "" {
		if !req.UserIsAdmin() {
			hasAccess, err := m.acrHelper.UserHasAccessToMCPServerCatalogEntry(req.User, input.CatalogEntryID)
			if err != nil {
				return err
			}

			if !hasAccess {
				return types.NewErrForbidden("user does not have access to MCP server catalog entry")
			}
		}

		var catalogEntry v1.MCPServerCatalogEntry
		if err := req.Get(&catalogEntry, input.CatalogEntryID); err != nil {
			return err
		}

		manifest, err := serverManifestFromCatalogEntryManifest(req.UserIsAdmin(), catalogEntry.Spec.Manifest, input.MCPServerManifest)
		if err != nil {
			return err
		}

		server.Spec.Manifest = manifest
		server.Spec.UnsupportedTools = catalogEntry.Spec.UnsupportedTools
	} else if req.UserIsAdmin() {
		// If the user is an admin, they can create a server with a manifest that is not in the catalog.
		server.Spec.Manifest = input.MCPServerManifest
	} else {
		return types.NewErrBadRequest("catalogEntryID is required")
	}

	if err := validation.ValidateServerManifest(server.Spec.Manifest); err != nil {
		return types.NewErrBadRequest("validation failed: %v", err)
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	if err := req.Create(&server); err != nil {
		return err
	}

	var (
		cred gptscript.Credential
		err  error
	)
	if catalogID != "" {
		cred, err = req.GPTClient.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", catalogID, server.Name)}, server.Name)
	} else {
		cred, err = req.GPTClient.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", req.User.GetUID(), server.Name)}, server.Name)
	}
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	return req.WriteCreated(convertMCPServer(server, cred.Env, m.serverURL))
}

func (m *MCPHandler) AdminOnlyUpdateServer(req api.Context) error {
	var (
		id        = req.PathValue("mcp_server_id")
		catalogID = req.PathValue("catalog_id")
		err       error
		updated   types.MCPServerManifest
		existing  v1.MCPServer
	)

	if err := req.Get(&existing, id); err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if existing.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	if err = req.Read(&updated); err != nil {
		return err
	}

	if updated.RemoteConfig != nil && !strings.HasPrefix(updated.RemoteConfig.URL, "http") {
		updated.RemoteConfig.URL = "https://" + updated.RemoteConfig.URL
	}

	// Shutdown any server that is using the default credentials.
	var cred gptscript.Credential
	if catalogID != "" {
		cred, err = req.GPTClient.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", catalogID, existing.Name)}, existing.Name)
	} else {
		cred, err = req.GPTClient.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", req.User.GetUID(), existing.Name)}, existing.Name)
	}
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	// Shutdown the server, even if there is no credential
	if catalogID != "" {
		err = m.removeMCPServer(req.Context(), existing, catalogID, cred.Env)
	} else {
		err = m.removeMCPServer(req.Context(), existing, req.User.GetUID(), cred.Env)
	}
	if err != nil {
		return err
	}

	if err := validation.ValidateServerManifest(updated); err != nil {
		return types.NewErrBadRequest("validation failed: %v", err)
	}

	existing.Spec.Manifest = updated

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&existing)

	if err = req.Update(&existing); err != nil {
		return err
	}

	return req.Write(convertMCPServer(existing, cred.Env, m.serverURL))
}

func (m *MCPHandler) UpdateServerAlias(req api.Context) error {
	var (
		id     = req.PathValue("mcp_server_id")
		server v1.MCPServer
	)

	if err := req.Get(&server, id); err != nil {
		return err
	}

	if server.Spec.SharedWithinMCPCatalogName != "" {
		return types.NewErrBadRequest("cannot update alias for a multi-user MCP server")
	}

	var input struct {
		Alias string `json:"alias,omitempty"`
	}
	if err := req.Read(&input); err != nil {
		return err
	}

	if input.Alias == server.Spec.Alias {
		// If the alias is the same, skip update.
		return nil
	}
	server.Spec.Alias = input.Alias

	if err := req.Update(&server); err != nil {
		return err
	}

	return nil
}

func (m *MCPHandler) ConfigureServer(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if mcpServer.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&mcpServer)

	var envVars map[string]string
	if err := req.Read(&envVars); err != nil {
		return err
	}

	var credCtx, scope string
	if catalogID != "" {
		credCtx = fmt.Sprintf("%s-%s", catalogID, mcpServer.Name)
		scope = catalogID
	} else {
		credCtx = fmt.Sprintf("%s-%s", req.User.GetUID(), mcpServer.Name)
		scope = req.User.GetUID()
	}

	// Allow for updating credentials. The only way to update a credential is to delete the existing one and recreate it.
	if err := m.removeMCPServerAndCred(req.Context(), req.GPTClient, mcpServer, scope, []string{credCtx}); err != nil {
		return err
	}

	for key, val := range envVars {
		if val == "" {
			delete(envVars, key)
		}
	}

	if err := req.GPTClient.CreateCredential(req.Context(), gptscript.Credential{
		Context:  credCtx,
		ToolName: mcpServer.Name,
		Type:     gptscript.CredentialTypeTool,
		Env:      envVars,
	}); err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}

	return req.Write(convertMCPServer(mcpServer, envVars, m.serverURL))
}

func (m *MCPHandler) DeconfigureServer(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if mcpServer.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&mcpServer)

	var credCtx, scope string
	if catalogID != "" {
		credCtx = fmt.Sprintf("%s-%s", catalogID, mcpServer.Name)
		scope = catalogID
	} else {
		credCtx = fmt.Sprintf("%s-%s", req.User.GetUID(), mcpServer.Name)
		scope = req.User.GetUID()
	}

	if err := m.removeMCPServerAndCred(req.Context(), req.GPTClient, mcpServer, scope, []string{credCtx}); err != nil {
		return err
	}

	return req.Write(convertMCPServer(mcpServer, nil, m.serverURL))
}

func (m *MCPHandler) Reveal(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if mcpServer.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}

	var credCtx string
	if catalogID != "" {
		credCtx = fmt.Sprintf("%s-%s", catalogID, mcpServer.Name)
	} else {
		credCtx = fmt.Sprintf("%s-%s", req.User.GetUID(), mcpServer.Name)
	}

	cred, err := req.GPTClient.RevealCredential(req.Context(), []string{credCtx}, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	} else if err == nil {
		return req.Write(cred.Env)
	}

	return types.NewErrNotFound("no credential found for %q", mcpServer.Name)
}

func toolsForServer(ctx context.Context, mcpSessionManager *mcp.SessionManager, userID string, server v1.MCPServer, serverConfig mcp.ServerConfig, allowedTools []string) ([]types.MCPServerTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	gTools, err := mcpSessionManager.ListTools(ctx, userID, server, serverConfig)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		var are nmcp.AuthRequiredErr
		if strings.HasSuffix(strings.ToLower(err.Error()), "method not found") {
			return nil, types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support tools")
		} else if errors.As(err, &are) {
			return nil, types.NewErrHTTP(http.StatusPreconditionFailed, "MCP server requires authentication")
		}
		return nil, err
	}

	return mcp.ConvertTools(gTools, allowedTools, server.Spec.UnsupportedTools)
}

func (m *MCPHandler) removeMCPServer(ctx context.Context, mcpServer v1.MCPServer, scope string, credEnv map[string]string) error {
	serverConfig, _, err := mcp.ServerToServerConfig(mcpServer, scope, credEnv)
	if err != nil {
		return err
	}

	if err = m.mcpSessionManager.ShutdownServer(ctx, serverConfig); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

func (m *MCPHandler) removeMCPServerAndCred(ctx context.Context, gptClient *gptscript.GPTScript, mcpServer v1.MCPServer, scope string, credCtx []string) error {
	cred, err := gptClient.RevealCredential(ctx, credCtx, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	// Shutdown the server, even if there is no credential
	if err := m.removeMCPServer(ctx, mcpServer, scope, cred.Env); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	// If revealing the credential was successful, remove it.
	if err == nil {
		if err = gptClient.DeleteCredential(ctx, cred.Context, mcpServer.Name); err != nil {
			return fmt.Errorf("failed to remove existing credential: %w", err)
		}
	}

	return nil
}

func extractEnvVars(text string) []string {
	if text == "" {
		return nil
	}

	matches := envVarRegex.FindAllStringSubmatch(text, -1)

	vars := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			vars = append(vars, match[1])
		}
	}

	return vars
}

// addExtractedEnvVars extracts and adds environment variables to the server definition
func addExtractedEnvVars(server *v1.MCPServer) {
	// Keep track of existing env vars in the spec to avoid duplicates
	existing := make(map[string]struct{})
	for _, env := range server.Spec.Manifest.Env {
		existing[env.Key] = struct{}{}
	}

	// Extract variables based on runtime type
	var toExtract []string
	switch server.Spec.Manifest.Runtime {
	case types.RuntimeUVX:
		if server.Spec.Manifest.UVXConfig != nil {
			toExtract = []string{server.Spec.Manifest.UVXConfig.Command}
			if len(server.Spec.Manifest.UVXConfig.Args) > 0 {
				toExtract = append(toExtract, server.Spec.Manifest.UVXConfig.Args...)
			}
		}
	case types.RuntimeNPX:
		if server.Spec.Manifest.NPXConfig != nil && len(server.Spec.Manifest.NPXConfig.Args) > 0 {
			toExtract = append(toExtract, server.Spec.Manifest.NPXConfig.Args...)
		}
	case types.RuntimeContainerized:
		if server.Spec.Manifest.ContainerizedConfig != nil {
			toExtract = []string{server.Spec.Manifest.ContainerizedConfig.Command}
			if len(server.Spec.Manifest.ContainerizedConfig.Args) > 0 {
				toExtract = append(toExtract, server.Spec.Manifest.ContainerizedConfig.Args...)
			}
		}
	case types.RuntimeRemote:
		if server.Spec.Manifest.RemoteConfig != nil {
			toExtract = []string{server.Spec.Manifest.RemoteConfig.URL}
		}
	}

	for _, v := range toExtract {
		for _, env := range extractEnvVars(v) {
			if _, exists := existing[env]; !exists {
				server.Spec.Manifest.Env = append(server.Spec.Manifest.Env, types.MCPEnv{
					MCPHeader: types.MCPHeader{
						Name:        env,
						Key:         env,
						Description: "Automatically detected variable",
						Sensitive:   true,
						Required:    true,
					},
				})
			}
		}
	}
}

// addExtractedEnvVarsToCatalogEntry extracts and adds environment variables to the catalog entry manifest
func addExtractedEnvVarsToCatalogEntry(entry *v1.MCPServerCatalogEntry) {
	// Keep track of existing env vars in the manifest to avoid duplicates
	existing := make(map[string]struct{})
	for _, env := range entry.Spec.Manifest.Env {
		existing[env.Key] = struct{}{}
	}

	// Extract variables based on runtime type
	var toExtract []string

	switch entry.Spec.Manifest.Runtime {
	case types.RuntimeUVX:
		if entry.Spec.Manifest.UVXConfig != nil {
			toExtract = append(toExtract, entry.Spec.Manifest.UVXConfig.Command)
			if len(entry.Spec.Manifest.UVXConfig.Args) > 0 {
				toExtract = append(toExtract, entry.Spec.Manifest.UVXConfig.Args...)
			}
		}
	case types.RuntimeNPX:
		if entry.Spec.Manifest.NPXConfig != nil && len(entry.Spec.Manifest.NPXConfig.Args) > 0 {
			toExtract = append(toExtract, entry.Spec.Manifest.NPXConfig.Args...)
		}
	case types.RuntimeContainerized:
		if entry.Spec.Manifest.ContainerizedConfig != nil {
			toExtract = append(toExtract, entry.Spec.Manifest.ContainerizedConfig.Command)
			if len(entry.Spec.Manifest.ContainerizedConfig.Args) > 0 {
				toExtract = append(toExtract, entry.Spec.Manifest.ContainerizedConfig.Args...)
			}
		}
	case types.RuntimeRemote:
		if entry.Spec.Manifest.RemoteConfig != nil {
			toExtract = append(toExtract, entry.Spec.Manifest.RemoteConfig.FixedURL)
		}
	}

	for _, v := range toExtract {
		for _, env := range extractEnvVars(v) {
			if _, exists := existing[env]; !exists {
				entry.Spec.Manifest.Env = append(entry.Spec.Manifest.Env, types.MCPEnv{
					MCPHeader: types.MCPHeader{
						Name:        env,
						Key:         env,
						Description: "Automatically detected variable",
						Sensitive:   true,
						Required:    true,
					},
				})
			}
		}
	}
}

func convertMCPServer(server v1.MCPServer, credEnv map[string]string, serverURL string) types.MCPServer {
	var missingEnvVars, missingHeaders []string

	// Check for missing required env vars
	for _, env := range server.Spec.Manifest.Env {
		if !env.Required {
			continue
		}

		if _, ok := credEnv[env.Key]; !ok {
			missingEnvVars = append(missingEnvVars, env.Key)
		}
	}

	// Check for missing required headers (only for remote runtime)
	if server.Spec.Manifest.Runtime == types.RuntimeRemote && server.Spec.Manifest.RemoteConfig != nil {
		for _, header := range server.Spec.Manifest.RemoteConfig.Headers {
			if !header.Required {
				continue
			}

			if _, ok := credEnv[header.Key]; !ok {
				missingHeaders = append(missingHeaders, header.Key)
			}
		}
	}

	var connectURL string
	// Only non-shared servers get a connect URL.
	// Shared servers have connect URLs on the MCPServerInstances instead.
	if server.Spec.SharedWithinMCPCatalogName == "" {
		connectURL = fmt.Sprintf("%s/mcp-connect/%s", serverURL, server.Name)
	}

	conditions := make([]types.DeploymentCondition, 0, len(server.Status.DeploymentConditions))
	for _, cond := range server.Status.DeploymentConditions {
		conditions = append(conditions, types.DeploymentCondition{
			Type:               string(cond.Type),
			Status:             string(cond.Status),
			Reason:             cond.Reason,
			Message:            cond.Message,
			LastTransitionTime: *types.NewTime(cond.LastTransitionTime.Time),
			LastUpdateTime:     *types.NewTime(cond.LastUpdateTime.Time),
		})
	}

	return types.MCPServer{
		Metadata:                    MetadataFrom(&server),
		Alias:                       server.Spec.Alias,
		MissingRequiredEnvVars:      missingEnvVars,
		MissingRequiredHeaders:      missingHeaders,
		UserID:                      server.Spec.UserID,
		Configured:                  len(missingEnvVars) == 0 && len(missingHeaders) == 0 && !server.Spec.NeedsURL,
		MCPServerManifest:           server.Spec.Manifest,
		CatalogEntryID:              server.Spec.MCPServerCatalogEntryName,
		SharedWithinCatalogName:     server.Spec.SharedWithinMCPCatalogName,
		ConnectURL:                  connectURL,
		NeedsUpdate:                 server.Status.NeedsUpdate,
		NeedsURL:                    server.Spec.NeedsURL,
		PreviousURL:                 server.Spec.PreviousURL,
		MCPServerInstanceUserCount:  server.Status.MCPServerInstanceUserCount,
		DeploymentStatus:            server.Status.DeploymentStatus,
		DeploymentAvailableReplicas: server.Status.DeploymentAvailableReplicas,
		DeploymentReadyReplicas:     server.Status.DeploymentReadyReplicas,
		DeploymentReplicas:          server.Status.DeploymentReplicas,
		DeploymentConditions:        conditions,
	}
}

func (m *MCPHandler) ListServersInDefaultCatalog(req api.Context) error {
	var list v1.MCPServerList
	if err := req.List(&list, kclient.InNamespace(system.DefaultNamespace), kclient.MatchingFields{
		"spec.sharedWithinMCPCatalogName": system.DefaultCatalog,
	}); err != nil {
		return err
	}

	var allowedServers []v1.MCPServer
	if req.UserIsAdmin() {
		allowedServers = list.Items
	} else {
		for _, server := range list.Items {
			hasAccess, err := m.acrHelper.UserHasAccessToMCPServer(req.User, server.Name)
			if err != nil {
				return err
			}
			if hasAccess {
				allowedServers = append(allowedServers, server)
			}
		}
	}

	var credCtxs []string
	for _, server := range allowedServers {
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", server.Spec.SharedWithinMCPCatalogName, server.Name))
	}

	creds, err := req.GPTClient.ListCredentials(req.Context(), gptscript.ListCredentialsOptions{
		CredentialContexts: credCtxs,
	})
	if err != nil {
		return fmt.Errorf("failed to list credentials: %w", err)
	}

	credMap := make(map[string]map[string]string, len(creds))
	for _, cred := range creds {
		if _, ok := credMap[cred.ToolName]; !ok {
			c, err := req.GPTClient.RevealCredential(req.Context(), []string{cred.Context}, cred.ToolName)
			if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
				return fmt.Errorf("failed to find credential: %w", err)
			}
			credMap[cred.ToolName] = c.Env
		}
	}

	// Load catalog entries to enrich servers with tool previews
	var catalogEntries v1.MCPServerCatalogEntryList
	if err := req.List(&catalogEntries); err != nil {
		// Don't fail if we can't load catalog entries, just continue without previews
		log.Errorf("failed to load catalog entries: %v", err)
	}

	catalogEntryMap := make(map[string]v1.MCPServerCatalogEntry, len(catalogEntries.Items))
	for _, entry := range catalogEntries.Items {
		catalogEntryMap[entry.Name] = entry
	}

	var mcpServers []types.MCPServer
	for _, server := range allowedServers {
		addExtractedEnvVars(&server)
		// Enrich with tool preview data if catalog entry exists
		if server.Spec.MCPServerCatalogEntryName != "" {
			if entry, exists := catalogEntryMap[server.Spec.MCPServerCatalogEntryName]; exists {
				// Add tool preview from catalog entry to server manifest
				if entry.Spec.Manifest.ToolPreview != nil {
					server.Spec.Manifest.ToolPreview = entry.Spec.Manifest.ToolPreview
				}
			}
		}

		mcpServers = append(mcpServers, convertMCPServer(server, credMap[server.Name], m.serverURL))
	}

	return req.Write(types.MCPServerList{Items: mcpServers})
}

func (m *MCPHandler) GetServerFromDefaultCatalog(req api.Context) error {
	var (
		server v1.MCPServer
		id     = req.PathValue("mcp_server_id")
	)

	if err := req.Get(&server, id); err != nil {
		return err
	}

	if server.Spec.SharedWithinMCPCatalogName != system.DefaultCatalog {
		return types.NewErrNotFound("MCP server not found")
	}

	// Authorization check.
	if !req.UserIsAdmin() {
		hasAccess, err := m.acrHelper.UserHasAccessToMCPServer(req.User, server.Name)
		if err != nil {
			return err
		}
		if !hasAccess {
			return types.NewErrForbidden("user is not authorized to access this MCP server")
		}
	}

	cred, err := req.GPTClient.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", server.Spec.SharedWithinMCPCatalogName, server.Name)}, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	addExtractedEnvVars(&server)

	// Enrich with tool preview data if catalog entry exists
	if server.Spec.MCPServerCatalogEntryName != "" {
		var entry v1.MCPServerCatalogEntry
		if err := req.Get(&entry, server.Spec.MCPServerCatalogEntryName); err == nil {
			// Add tool preview from catalog entry to server manifest
			if entry.Spec.Manifest.ToolPreview != nil {
				server.Spec.Manifest.ToolPreview = entry.Spec.Manifest.ToolPreview
			}
		}
		// Don't fail if catalog entry is missing, just continue without preview
	}

	return req.Write(convertMCPServer(server, cred.Env, m.serverURL))
}

func (m *MCPHandler) ClearOAuthCredentials(req api.Context) error {
	catalogID := req.PathValue("catalog_id")

	var server v1.MCPServer
	if err := req.Get(&server, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// For servers that are in catalogs, this checks to make sure that a catalogID was provided and that it matches.
	// For servers that are not in catalogs, this checks to make sure that no catalogID was provided. (Both are empty strings.)
	if server.Spec.SharedWithinMCPCatalogName != catalogID {
		return types.NewErrNotFound("MCP server not found")
	}
	if err := req.GatewayClient.DeleteMCPOAuthToken(req.Context(), req.User.GetUID(), server.Name); err != nil {
		return fmt.Errorf("failed to delete OAuth credentials: %v", err)
	}

	req.WriteHeader(http.StatusNoContent)
	return nil
}

func (m *MCPHandler) GetServerDetails(req api.Context) error {
	server, serverConfig, err := serverForAction(req)
	if err != nil {
		return err
	}

	if serverConfig.Runtime == types.RuntimeRemote {
		return types.NewErrBadRequest("cannot get details for remote MCP server")
	}

	mcpServerDisplayName := server.Spec.Manifest.Name
	if mcpServerDisplayName == "" {
		mcpServerDisplayName = server.Name
	}

	details, err := m.mcpSessionManager.GetServerDetails(req.Context(), mcpServerDisplayName, server.Name, serverConfig)
	if err != nil {
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrNotFound(nse.Error())
		}
		return err
	}

	return req.Write(details)
}

func (m *MCPHandler) RestartServerDeployment(req api.Context) error {
	_, serverConfig, err := serverForAction(req)
	if err != nil {
		return err
	}

	if serverConfig.Runtime == types.RuntimeRemote {
		return types.NewErrBadRequest("cannot restart deployment for remote MCP server")
	}

	if err := m.mcpSessionManager.RestartServerDeployment(req.Context(), serverConfig); err != nil {
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrNotFound(nse.Error())
		}
		return err
	}

	req.WriteHeader(http.StatusNoContent)
	return nil
}

func (m *MCPHandler) StreamServerLogs(req api.Context) error {
	server, serverConfig, err := serverForAction(req)
	if err != nil {
		return err
	}

	if serverConfig.Runtime == types.RuntimeRemote {
		return types.NewErrBadRequest("cannot stream logs for remote MCP server")
	}

	mcpServerDisplayName := server.Spec.Manifest.Name
	if mcpServerDisplayName == "" {
		mcpServerDisplayName = server.Name
	}

	logs, err := m.mcpSessionManager.StreamServerLogs(req.Context(), mcpServerDisplayName, server.Name, serverConfig)
	if err != nil {
		if nse := (*mcp.ErrNotSupportedByBackend)(nil); errors.As(err, &nse) {
			return types.NewErrNotFound(nse.Error())
		}
		return err
	}
	defer logs.Close()

	// Set up Server-Sent Events headers
	req.ResponseWriter.Header().Set("Content-Type", "text/event-stream")
	req.ResponseWriter.Header().Set("Cache-Control", "no-cache")
	req.ResponseWriter.Header().Set("Connection", "keep-alive")

	flusher, shouldFlush := req.ResponseWriter.(http.Flusher)

	// Send initial connection event
	fmt.Fprintf(req.ResponseWriter, "event: connected\ndata: Log stream started\n\n")
	if shouldFlush {
		flusher.Flush()
	}

	// Channel to coordinate between goroutines
	logChan := make(chan string, 100) // Buffered to prevent blocking

	// Start a goroutine to read logs
	go func() {
		defer close(logChan)

		scanner := bufio.NewScanner(logs)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case <-req.Context().Done():
				return
			case logChan <- line:
			}
		}
		if err := scanner.Err(); err != nil {
			// Send error event
			select {
			case logChan <- fmt.Sprintf("ERROR retrieving logs: %v", err):
			case <-req.Context().Done():
			}
			return
		}
	}()

	// Send log events as they come in
	ticker := time.NewTicker(30 * time.Second) // Keep-alive ping
	defer ticker.Stop()

	for {
		select {
		case <-req.Context().Done():
			fmt.Fprintf(req.ResponseWriter, "event: disconnected\ndata: Client disconnected\n\n")
			if shouldFlush {
				flusher.Flush()
			}
			return nil
		case <-ticker.C:
			// Send keep-alive ping
			fmt.Fprintf(req.ResponseWriter, "event: ping\ndata: keep-alive\n\n")
			if shouldFlush {
				flusher.Flush()
			}
		case logLine, ok := <-logChan:
			if !ok {
				fmt.Fprintf(req.ResponseWriter, "event: ended\ndata: Log stream ended\n\n")
				if shouldFlush {
					flusher.Flush()
				}
				return nil
			}
			fmt.Fprintf(req.ResponseWriter, "event: log\ndata: %s\n\n", logLine)
			if shouldFlush {
				flusher.Flush()
			}
		}
	}
}

func (m *MCPHandler) UpdateURL(req api.Context) error {
	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return fmt.Errorf("failed to get server: %w", err)
	}

	if mcpServer.Spec.SharedWithinMCPCatalogName != "" {
		return types.NewErrBadRequest("cannot update the URL for a multi-user MCP server; use the UpdateServer endpoint instead")
	}

	if mcpServer.Spec.MCPServerCatalogEntryName == "" {
		return types.NewErrBadRequest("this server does not have a catalog entry")
	}

	if mcpServer.Spec.Manifest.Runtime != types.RuntimeRemote || mcpServer.Spec.Manifest.RemoteConfig == nil {
		return types.NewErrBadRequest("cannot update the URL for a non-remote MCP server")
	}

	var entry v1.MCPServerCatalogEntry
	if err := req.Get(&entry, mcpServer.Spec.MCPServerCatalogEntryName); err != nil {
		return fmt.Errorf("failed to get catalog entry: %w", err)
	}

	if entry.Spec.Manifest.RemoteConfig == nil {
		return types.NewErrBadRequest("the catalog entry for this server does not have remote configuration")
	}

	if entry.Spec.Manifest.RemoteConfig.FixedURL != "" {
		return types.NewErrBadRequest("this server already has a fixed URL that cannot be updated")
	}

	if entry.Spec.Manifest.RemoteConfig.Hostname == "" {
		return types.NewErrBadRequest("the catalog entry for this server does not have a hostname")
	}

	var input struct {
		URL string `json:"url"`
	}
	if err := req.Read(&input); err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	if !strings.HasPrefix(input.URL, "http") {
		input.URL = "https://" + input.URL
	}

	if err := types.ValidateURLHostname(input.URL, entry.Spec.Manifest.RemoteConfig.Hostname); err != nil {
		return types.NewErrBadRequest("the hostname in the URL does not match the hostname in the catalog entry: %v", err)
	}

	parsedURL, err := url.Parse(input.URL)
	if err != nil {
		return types.NewErrBadRequest("failed to parse input URL: %v", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return types.NewErrBadRequest("the URL must be HTTP or HTTPS")
	}

	mcpServer.Spec.Manifest.RemoteConfig.URL = input.URL
	mcpServer.Spec.NeedsURL = false
	mcpServer.Spec.PreviousURL = ""

	if err := validation.ValidateServerManifest(mcpServer.Spec.Manifest); err != nil {
		return err
	}

	if err := req.Update(&mcpServer); err != nil {
		return fmt.Errorf("failed to update server: %w", err)
	}

	return req.Write(convertMCPServer(mcpServer, nil, m.serverURL))
}

func (m *MCPHandler) TriggerUpdate(req api.Context) error {
	var server v1.MCPServer
	if err := req.Get(&server, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	if server.Spec.SharedWithinMCPCatalogName != "" {
		return types.NewErrBadRequest("cannot trigger update for a multi-user MCP server; use the UpdateServer endpoint instead")
	}

	if server.Spec.MCPServerCatalogEntryName == "" || !server.Status.NeedsUpdate {
		return nil
	}

	var entry v1.MCPServerCatalogEntry
	if err := req.Get(&entry, server.Spec.MCPServerCatalogEntryName); err != nil {
		return err
	}

	// Update the server manifest with the latest from the catalog entry
	server.Spec.Manifest.Metadata = entry.Spec.Manifest.Metadata
	server.Spec.Manifest.Name = entry.Spec.Manifest.Name
	server.Spec.Manifest.Description = entry.Spec.Manifest.Description
	server.Spec.Manifest.Icon = entry.Spec.Manifest.Icon
	server.Spec.Manifest.Env = entry.Spec.Manifest.Env
	server.Spec.Manifest.Runtime = entry.Spec.Manifest.Runtime
	server.Spec.Manifest.UVXConfig = entry.Spec.Manifest.UVXConfig
	server.Spec.Manifest.NPXConfig = entry.Spec.Manifest.NPXConfig
	server.Spec.Manifest.ContainerizedConfig = entry.Spec.Manifest.ContainerizedConfig

	// Handle remote runtime URL updates
	if entry.Spec.Manifest.Runtime == types.RuntimeRemote && entry.Spec.Manifest.RemoteConfig != nil {
		if entry.Spec.Manifest.RemoteConfig.FixedURL != "" {
			// Use the fixed URL from catalog entry
			server.Spec.Manifest.RemoteConfig = &types.RemoteRuntimeConfig{
				URL:     entry.Spec.Manifest.RemoteConfig.FixedURL,
				Headers: entry.Spec.Manifest.RemoteConfig.Headers,
			}
		} else if entry.Spec.Manifest.RemoteConfig.Hostname != "" {
			// Check if the server's current URL matches the new hostname requirement
			if server.Spec.Manifest.RemoteConfig != nil && server.Spec.Manifest.RemoteConfig.URL != "" {
				hostnameMismatchErr := types.ValidateURLHostname(server.Spec.Manifest.RemoteConfig.URL, entry.Spec.Manifest.RemoteConfig.Hostname)

				server.Spec.NeedsURL = hostnameMismatchErr != nil
				if server.Spec.NeedsURL {
					server.Spec.PreviousURL = server.Spec.Manifest.RemoteConfig.URL
					server.Spec.Manifest.RemoteConfig.URL = ""
				}

				server.Spec.Manifest.RemoteConfig = &types.RemoteRuntimeConfig{
					Headers: entry.Spec.Manifest.RemoteConfig.Headers,
				}
			} else {
				// No current URL, needs one
				server.Spec.NeedsURL = true
				server.Spec.Manifest.RemoteConfig = &types.RemoteRuntimeConfig{
					Headers: entry.Spec.Manifest.RemoteConfig.Headers,
				}
			}
		}
	} else {
		// For non-remote runtimes, clear the remote config
		server.Spec.Manifest.RemoteConfig = nil
	}

	// Shutdown any server that is using the default credentials.
	cred, err := req.GPTClient.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", server.Spec.UserID, server.Name)}, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	// Shutdown the server, even if there is no credential
	if err := m.removeMCPServer(req.Context(), server, server.Spec.UserID, cred.Env); err != nil {
		return err
	}

	if err := req.Update(&server); err != nil {
		return err
	}

	return nil
}
