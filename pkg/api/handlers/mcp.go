package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gptscript-ai/go-gptscript"
	gtypes "github.com/gptscript-ai/gptscript/pkg/types"
	nmcp "github.com/nanobot-ai/nanobot/pkg/mcp"
	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/mcp"
	"github.com/obot-platform/obot/pkg/projects"
	"github.com/obot-platform/obot/pkg/render"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type MCPHandler struct {
	gptscript         *gptscript.GPTScript
	mcpSessionManager *mcp.SessionManager
}

var envVarRegex = regexp.MustCompile(`\${([^}]+)}`)

func NewMCPHandler(gptscript *gptscript.GPTScript, mcpLoader *mcp.SessionManager) *MCPHandler {
	return &MCPHandler{
		gptscript:         gptscript,
		mcpSessionManager: mcpLoader,
	}
}

func (m *MCPHandler) GetCatalogEntry(req api.Context) error {
	var (
		entry v1.MCPServerCatalogEntry
		id    = req.PathValue("mcp_server_id")
	)

	if err := req.Get(&entry, id); err != nil {
		return err
	}

	return req.Write(convertMCPServerCatalogEntry(entry))
}

func (m *MCPHandler) ListCatalog(req api.Context) error {
	var list v1.MCPServerCatalogEntryList
	if err := req.List(&list); err != nil {
		return err
	}

	items := make([]types.MCPServerCatalogEntry, 0, len(list.Items))
	for _, entry := range list.Items {
		items = append(items, convertMCPServerCatalogEntry(entry))
	}

	return req.Write(types.MCPServerCatalogEntryList{Items: items})
}

func convertMCPServerCatalogEntry(entry v1.MCPServerCatalogEntry) types.MCPServerCatalogEntry {
	// Add extracted env vars directly to the entry
	addExtractedEnvVarsToCatalogEntry(&entry)

	return types.MCPServerCatalogEntry{
		Metadata:          MetadataFrom(&entry),
		CommandManifest:   entry.Spec.CommandManifest,
		URLManifest:       entry.Spec.URLManifest,
		ToolReferenceName: entry.Spec.ToolReferenceName,
	}
}

func (m *MCPHandler) ListServer(req api.Context) error {
	t, err := getThreadForScope(req)
	if err != nil {
		return err
	}

	topMost, err := projects.GetRoot(req.Context(), req.Storage, t)
	if err != nil {
		return err
	}

	var servers v1.MCPServerList
	if err := req.List(&servers, kclient.MatchingFields{
		"spec.threadName": topMost.Name,
	}); err != nil {
		return nil
	}

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	credCtxs := make([]string, 0, len(servers.Items))
	for _, server := range servers.Items {
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s", project.Name, server.Name))
		if project.IsSharedProject() {
			// Add default credentials shared by the agent for this MCP server if available.
			credCtxs = append(credCtxs, fmt.Sprintf("%s-%s-shared", server.Spec.ThreadName, server.Name))
		}
	}

	creds, err := m.gptscript.ListCredentials(req.Context(), gptscript.ListCredentialsOptions{
		CredentialContexts: credCtxs,
	})
	if err != nil {
		return fmt.Errorf("failed to list credentials: %w", err)
	}

	credMap := make(map[string]map[string]string, len(creds))
	for _, cred := range creds {
		if _, ok := credMap[cred.ToolName]; !ok {
			c, err := m.gptscript.RevealCredential(req.Context(), []string{cred.Context}, cred.ToolName)
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

		items = append(items, convertMCPServer(server, credMap[server.Name]))
	}

	return req.Write(types.MCPServerList{Items: items})
}

func (m *MCPHandler) GetServer(req api.Context) error {
	var (
		server v1.MCPServer
		id     = req.PathValue("mcp_server_id")
	)

	if err := req.Get(&server, id); err != nil {
		return err
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	credCtxs := []string{
		fmt.Sprintf("%s-%s", project.Name, server.Name),
	}
	if project.IsSharedProject() {
		// Add default credentials shared by the agent for this MCP server if available.
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s-shared", server.Spec.ThreadName, server.Name))
	}

	cred, err := m.gptscript.RevealCredential(req.Context(), credCtxs, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	return req.Write(convertMCPServer(server, cred.Env))
}

func (m *MCPHandler) DeleteServer(req api.Context) error {
	var (
		server v1.MCPServer
		id     = req.PathValue("mcp_server_id")
	)

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	if err = req.Get(&server, id); err != nil {
		return err
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	// Ensure that the MCP server is in the same project as the request before deleting it.
	// This prevents chatbot users from deleting MCP servers from the agent.
	// This is necessary because in order to enable MCP servers to be shared across projects,
	// the standard authz middleware allows access to all MCP server endpoints from any "child" project
	// of the one the MCP server belongs to.
	if project.Name != server.Spec.ThreadName {
		return types.NewErrForbidden("cannot delete MCP server from this project")
	}

	if err := req.Delete(&server); err != nil {
		return err
	}

	return req.Write(convertMCPServer(server, nil))
}

func (m *MCPHandler) GetTools(req api.Context) error {
	server, serverConfig, caps, err := m.serverForAction(req)
	if err != nil {
		return err
	}

	if caps.Tools == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support tools")
	}

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

	tools, err := m.toolsForServer(req.Context(), req.Storage, server, serverConfig, thread.Spec.Manifest.AllowedMCPTools[server.Name])
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
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

	cred, err := m.gptscript.RevealCredential(req.Context(), credCtxs, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}
	serverConfig, missingRequiredNames := mcp.ToServerConfig(mcpServer, project.Name, cred.Env, tools...)
	if len(missingRequiredNames) > 0 {
		return types.NewErrBadRequest("MCP server %s is missing required parameters: %s", mcpServer.Name, strings.Join(missingRequiredNames, ", "))
	}

	mcpTools, err := m.toolsForServer(req.Context(), req.Storage, mcpServer, serverConfig, tools)
	if err != nil {
		if uc := (*render.UnconfiguredMCPError)(nil); errors.As(err, &uc) {
			return types.NewErrBadRequest("MCP server %s is missing required parameters: %s", uc.MCPName, strings.Join(uc.Missing, ", "))
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
	mcpServer, serverConfig, caps, err := m.serverForAction(req)
	if err != nil {
		return err
	}

	if caps.Resources == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support resources")
	}

	resources, err := m.mcpSessionManager.ListResources(req.Context(), mcpServer, serverConfig)
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	return req.Write(resources)
}

func (m *MCPHandler) ReadResource(req api.Context) error {
	mcpServer, serverConfig, caps, err := m.serverForAction(req)
	if err != nil {
		return err
	}

	if caps.Resources == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support resources")
	}

	contents, err := m.mcpSessionManager.ReadResource(req.Context(), mcpServer, serverConfig, req.PathValue("resource_uri"))
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	return req.Write(contents)
}

func (m *MCPHandler) GetPrompts(req api.Context) error {
	mcpServer, serverConfig, caps, err := m.serverForAction(req)
	if err != nil {
		return err
	}

	if caps.Prompts == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support prompts")
	}

	prompts, err := m.mcpSessionManager.ListPrompts(req.Context(), mcpServer, serverConfig)
	if err != nil {
		return fmt.Errorf("failed to list prompts: %w", err)
	}

	return req.Write(prompts)
}

func (m *MCPHandler) GetPrompt(req api.Context) error {
	mcpServer, serverConfig, caps, err := m.serverForAction(req)
	if err != nil {
		return err
	}

	if caps.Prompts == nil {
		return types.NewErrHTTP(http.StatusFailedDependency, "MCP server does not support prompts")
	}

	var args map[string]string
	if err = req.Read(&args); err != nil {
		return fmt.Errorf("failed to read args: %w", err)
	}

	messages, description, err := m.mcpSessionManager.GetPrompt(req.Context(), mcpServer, serverConfig, req.PathValue("prompt_name"), args)
	if err != nil {
		return fmt.Errorf("failed to get prompt: %w", err)
	}

	return req.Write(map[string]any{
		"messages":    messages,
		"description": description,
	})
}

func (m *MCPHandler) serverForAction(req api.Context) (v1.MCPServer, mcp.ServerConfig, nmcp.ServerCapabilities, error) {
	var (
		server v1.MCPServer
		id     = req.PathValue("mcp_server_id")
	)

	project, err := getProjectThread(req)
	if err != nil {
		return server, mcp.ServerConfig{}, nmcp.ServerCapabilities{}, err
	}

	if err = req.Get(&server, id); err != nil {
		return server, mcp.ServerConfig{}, nmcp.ServerCapabilities{}, err
	}

	if server.Spec.ToolReferenceName != "" && server.Spec.Manifest.Command == "" && server.Spec.Manifest.URL == "" {
		// Legacy tool bundles support tools.
		return server, mcp.ServerConfig{}, nmcp.ServerCapabilities{
			Tools: &nmcp.ToolsServerCapability{},
		}, nil
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	credCtxs := []string{
		fmt.Sprintf("%s-%s", project.Name, server.Name),
	}
	if project.IsSharedProject() {
		// Add default credentials shared by the agent for this MCP server if available.
		credCtxs = append(credCtxs, fmt.Sprintf("%s-%s-shared", server.Spec.ThreadName, server.Name))
	}

	cred, err := m.gptscript.RevealCredential(req.Context(), credCtxs, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return server, mcp.ServerConfig{}, nmcp.ServerCapabilities{}, fmt.Errorf("failed to find credential: %w", err)
	}

	serverConfig, missingConfig := mcp.ToServerConfig(server, project.Name, cred.Env)

	if len(missingConfig) > 0 {
		return server, mcp.ServerConfig{}, nmcp.ServerCapabilities{}, types.NewErrBadRequest("missing required config: %s", strings.Join(missingConfig, ", "))
	}

	caps, err := m.mcpSessionManager.ServerCapabilities(req.Context(), server, serverConfig)
	return server, serverConfig, caps, err
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
	if override.Command != "" {
		existing.Command = override.Command
	}
	if len(override.Args) > 0 {
		existing.Args = override.Args
	}
	if override.URL != "" {
		existing.URL = override.URL
	}
	if len(override.Headers) > 0 {
		existing.Headers = override.Headers
	}

	return existing
}

func (m *MCPHandler) CreateServer(req api.Context) error {
	var input types.MCPServer
	if err := req.Read(&input); err != nil {
		return err
	}

	t, err := getThreadForScope(req)
	if err != nil {
		return err
	}

	server := v1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: system.MCPServerPrefix,
			Namespace:    req.Namespace(),
		},
		Spec: v1.MCPServerSpec{
			Manifest:                  input.MCPServerManifest,
			MCPServerCatalogEntryName: input.CatalogID,
			ThreadName:                t.Name,
			UserID:                    req.User.GetUID(),
		},
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&server)

	if input.CatalogID != "" {
		var catalogEntry v1.MCPServerCatalogEntry
		if err := req.Get(&catalogEntry, input.CatalogID); err != nil {
			return err
		}

		if catalogEntry.Spec.CommandManifest.Server.URL != "" {
			server.Spec.Manifest = catalogEntry.Spec.URLManifest.Server
		} else {
			server.Spec.Manifest = catalogEntry.Spec.CommandManifest.Server
		}
		server.Spec.ToolReferenceName = catalogEntry.Spec.ToolReferenceName
		server.Spec.UnsupportedTools = catalogEntry.Spec.UnsupportedTools
		// Override the defaults from the catalog with the values from the request.
		server.Spec.Manifest = mergeMCPServerManifests(server.Spec.Manifest, input.MCPServerManifest)
	}

	if err = req.Create(&server); err != nil {
		return err
	}

	cred, err := m.gptscript.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", server.Spec.ThreadName, server.Name)}, server.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	return req.WriteCreated(convertMCPServer(server, cred.Env))
}

func (m *MCPHandler) UpdateServer(req api.Context) error {
	var (
		id       = req.PathValue("mcp_server_id")
		updated  types.MCPServerManifest
		existing v1.MCPServer
	)

	if err := req.Get(&existing, id); err != nil {
		return err
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&existing)

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	// Ensure that the MCP server being updated is in the project referenced by the request.
	// This prevents chatbot users from editing MCP servers in the agent.
	// This is necessary because in order to enable MCP servers to be shared across projects,
	// the standard authz middleware allows access to all MCP server endpoints from any "child" project
	// of the one the MCP server belongs to.
	if project.Name != existing.Spec.ThreadName {
		return types.NewErrForbidden("cannot edit MCP server from this project")
	}

	if err := req.Read(&updated); err != nil {
		return err
	}

	// Shutdown any server that is using the default credentials.
	cred, err := m.gptscript.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", existing.Spec.ThreadName, existing.Name)}, existing.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	// Shutdown the server, even if there is no credential
	if err = m.removeMCPServer(req.Context(), existing, project.Name, cred.Env); err != nil {
		return err
	}

	// Shutdown the MCP server using any shared credentials.
	sharedCred, err := m.gptscript.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s-shared", existing.Spec.ThreadName, existing.Name)}, existing.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	var chatBots v1.ThreadList
	if err = req.List(&chatBots, &kclient.ListOptions{
		Namespace: project.Namespace,
		FieldSelector: fields.SelectorFromSet(map[string]string{
			"spec.parentThreadName": project.Name,
			"spec.project":          "true",
		}),
	}); err != nil {
		return fmt.Errorf("failed to list child projects: %w", err)
	}

	// Shutdown all chatbot MCP servers.
	for _, chatBot := range chatBots.Items {
		childCred, err := m.gptscript.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", chatBot.Name, existing.Name)}, existing.Name)
		if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
			return fmt.Errorf("failed to find credential: %w", err)
		} else if err != nil {
			// Use the shared parent credential if we didn't find the chatbot's credential.
			childCred = sharedCred
		}

		// Shutdown the server, even if there is no credential
		if err = m.removeMCPServer(req.Context(), existing, chatBot.Name, childCred.Env); err != nil {
			return err
		}
	}

	existing.Spec.Manifest = updated

	if err = req.Update(&existing); err != nil {
		return err
	}

	return req.Write(convertMCPServer(existing, cred.Env))
}

func (m *MCPHandler) ConfigureServer(req api.Context) error {
	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&mcpServer)

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	var envVars map[string]string
	if err := req.Read(&envVars); err != nil {
		return err
	}

	// Allow for updating credentials. The only way to update a credential is to delete the existing one and recreate it.
	credCtx := fmt.Sprintf("%s-%s", project.Name, mcpServer.Name)
	if err = m.removeMCPServerAndCred(req.Context(), mcpServer, project.Name, []string{credCtx}); err != nil {
		return err
	}

	for key, val := range envVars {
		if val == "" {
			delete(envVars, key)
		}
	}

	if err = m.gptscript.CreateCredential(req.Context(), gptscript.Credential{
		Context:  credCtx,
		ToolName: mcpServer.Name,
		Type:     gptscript.CredentialTypeTool,
		Env:      envVars,
	}); err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}

	return req.Write(convertMCPServer(mcpServer, envVars))
}

func (m *MCPHandler) ConfigureSharedServer(req api.Context) error {
	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&mcpServer)

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	if project.Name != mcpServer.Spec.ThreadName {
		return types.NewErrForbidden("cannot edit shared MCP server from this project")
	}

	var envVars map[string]string
	if err = req.Read(&envVars); err != nil {
		return err
	}

	var chatBots v1.ThreadList
	if err = req.List(&chatBots, &kclient.ListOptions{
		Namespace: project.Namespace,
		FieldSelector: fields.SelectorFromSet(map[string]string{
			"spec.parentThreadName": project.Name,
			"spec.project":          "true",
		}),
	}); err != nil {
		return fmt.Errorf("failed to list child projects: %w", err)
	}

	credCtx := fmt.Sprintf("%s-%s-shared", mcpServer.Spec.ThreadName, mcpServer.Name)
	cred, err := m.gptscript.RevealCredential(req.Context(), []string{credCtx}, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	// Remove the MCP server for all chatbots using this credential.
	for _, chatBot := range chatBots.Items {
		if err = m.removeMCPServer(req.Context(), mcpServer, chatBot.Name, cred.Env); err != nil {
			return err
		}
	}

	// Remove the top-level MCP server if it exists and remove the credential.
	if err = m.removeMCPServerAndCred(req.Context(), mcpServer, project.Name, []string{credCtx}); err != nil {
		return err
	}

	for key, val := range envVars {
		if val == "" {
			delete(envVars, key)
		}
	}

	if err = m.gptscript.CreateCredential(req.Context(), gptscript.Credential{
		Context:  credCtx,
		ToolName: mcpServer.Name,
		Type:     gptscript.CredentialTypeTool,
		Env:      envVars,
	}); err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}

	return req.Write(convertMCPServer(mcpServer, envVars))
}

func (m *MCPHandler) DeconfigureServer(req api.Context) error {
	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&mcpServer)

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	if err = m.removeMCPServerAndCred(req.Context(), mcpServer, project.Name, []string{fmt.Sprintf("%s-%s", project.Name, mcpServer.Name)}); err != nil {
		return err
	}

	return req.Write(convertMCPServer(mcpServer, nil))
}

func (m *MCPHandler) DeconfigureSharedServer(req api.Context) error {
	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	// Add extracted env vars to the server definition
	addExtractedEnvVars(&mcpServer)

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	if project.Name != mcpServer.Spec.ThreadName {
		return types.NewErrForbidden("cannot edit shared MCP server from this project")
	}

	var chatBots v1.ThreadList
	if err = req.List(&chatBots, &kclient.ListOptions{
		Namespace: project.Namespace,
		FieldSelector: fields.SelectorFromSet(map[string]string{
			"spec.parentThreadName": project.Name,
			"spec.project":          "true",
		}),
	}); err != nil {
		return fmt.Errorf("failed to list child projects: %w", err)
	}

	credCtx := []string{fmt.Sprintf("%s-%s-shared", mcpServer.Spec.ThreadName, mcpServer.Name)}

	cred, err := m.gptscript.RevealCredential(req.Context(), credCtx, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	for _, chatBot := range chatBots.Items {
		if err = m.removeMCPServer(req.Context(), mcpServer, chatBot.Name, cred.Env); err != nil {
			return err
		}
	}

	// Remove the top-level MCP server if it exists and remove the credential.
	if err = m.removeMCPServerAndCred(req.Context(), mcpServer, project.Name, credCtx); err != nil {
		return err
	}

	return req.Write(convertMCPServer(mcpServer, nil))
}

func (m *MCPHandler) Reveal(req api.Context) error {
	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	project, err := getProjectThread(req)
	if err != nil {
		return err
	}

	cred, err := m.gptscript.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s", project.Name, mcpServer.Name)}, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	} else if err == nil {
		return req.Write(cred.Env)
	}

	return types.NewErrNotFound("no credential found for %q", mcpServer.Name)
}

func (m *MCPHandler) RevealSharedServer(req api.Context) error {
	var mcpServer v1.MCPServer
	if err := req.Get(&mcpServer, req.PathValue("mcp_server_id")); err != nil {
		return err
	}

	cred, err := m.gptscript.RevealCredential(req.Context(), []string{fmt.Sprintf("%s-%s-shared", mcpServer.Spec.ThreadName, mcpServer.Name)}, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	} else if err == nil {
		return req.Write(cred.Env)
	}

	return types.NewErrNotFound("no credential found for %q", mcpServer.Name)
}

func (m *MCPHandler) toolsForServer(ctx context.Context, client kclient.Client, server v1.MCPServer, serverConfig mcp.ServerConfig, allowedTools []string) ([]types.MCPServerTool, error) {
	allTools := allowedTools == nil || slices.Contains(allowedTools, "*")
	if server.Spec.ToolReferenceName != "" {
		var toolReferences v1.ToolReferenceList
		if err := client.List(ctx, &toolReferences, kclient.MatchingFields{
			"spec.bundleToolName": server.Spec.ToolReferenceName,
		}); err != nil {
			return nil, err
		}

		tools := make([]types.MCPServerTool, 0, len(toolReferences.Items))
		for _, ref := range toolReferences.Items {
			if ref.Status.Tool != nil {
				tools = append(tools, types.MCPServerTool{
					ID:          ref.Name,
					Name:        ref.Status.Tool.Name,
					Description: ref.Status.Tool.Description,
					Metadata:    ref.Status.Tool.Metadata,
					Params:      ref.Status.Tool.Params,
					Credentials: ref.Status.Tool.Credentials,
					Enabled:     allTools || slices.Contains(allowedTools, ref.Name),
				})
			}
		}

		return tools, nil
	}

	tool, err := mcp.ServerToolWithCreds(server, serverConfig)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	gTools, err := m.mcpSessionManager.Load(ctx, gtypes.Tool{
		ToolDef: gtypes.ToolDef{
			Parameters: gtypes.Parameters{
				Name: tool.Name,
			},
			Instructions: tool.Instructions,
		},
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, err
	}

	// Exclude the first tool because it is the "bundle" tool, and we aren't concerned with that.
	tools := make([]types.MCPServerTool, 0, len(gTools)-1)
	for _, t := range gTools[1:] {
		mcpTool := types.MCPServerTool{
			ID:          t.Name,
			Name:        t.Name,
			Description: t.Description,
			Metadata:    t.MetaData,
			Enabled:     allTools && !slices.Contains(server.Spec.UnsupportedTools, t.Name) || slices.Contains(allowedTools, t.Name),
			Unsupported: slices.Contains(server.Spec.UnsupportedTools, t.Name),
		}

		if t.Arguments != nil {
			mcpTool.Params = make(map[string]string, len(t.Arguments.Properties))
			for name, param := range t.Arguments.Properties {
				if param != nil {
					mcpTool.Params[name] = param.Description
				}
			}
		}

		tools = append(tools, mcpTool)
	}

	return tools, nil
}

func (m *MCPHandler) removeMCPServer(ctx context.Context, mcpServer v1.MCPServer, projectName string, credEnv map[string]string) error {
	serverConfig, _ := mcp.ToServerConfig(mcpServer, projectName, credEnv)
	if err := m.mcpSessionManager.ShutdownServer(ctx, serverConfig); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

func (m *MCPHandler) removeMCPServerAndCred(ctx context.Context, mcpServer v1.MCPServer, projectName string, credCtx []string) error {
	cred, err := m.gptscript.RevealCredential(ctx, credCtx, mcpServer.Name)
	if err != nil && !errors.As(err, &gptscript.ErrNotFound{}) {
		return fmt.Errorf("failed to find credential: %w", err)
	}

	// Shutdown the server, even if there is no credential
	if err := m.removeMCPServer(ctx, mcpServer, projectName, cred.Env); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	// If revealing the credential was successful, remove it.
	if err == nil {
		if err = m.gptscript.DeleteCredential(ctx, cred.Context, mcpServer.Name); err != nil {
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

	// Extract variables from command
	extracted := make(map[string]struct{})
	for _, v := range extractEnvVars(server.Spec.Manifest.Command) {
		extracted[v] = struct{}{}
	}

	// Extract variables from args
	for _, arg := range server.Spec.Manifest.Args {
		for _, v := range extractEnvVars(arg) {
			extracted[v] = struct{}{}
		}
	}

	// Extract variables from URL
	for _, v := range extractEnvVars(server.Spec.Manifest.URL) {
		extracted[v] = struct{}{}
	}

	// Add any new vars to the server's Env list
	for v := range extracted {
		if _, exists := existing[v]; !exists {
			server.Spec.Manifest.Env = append(server.Spec.Manifest.Env, types.MCPEnv{
				MCPHeader: types.MCPHeader{
					Name:        v,
					Key:         v,
					Description: "Automatically detected variable",
					Sensitive:   true,
					Required:    true,
				},
			})
		}
	}
}

// addExtractedEnvVarsToCatalogEntry extracts and adds environment variables to both manifests in the catalog entry
func addExtractedEnvVarsToCatalogEntry(entry *v1.MCPServerCatalogEntry) {
	// Extract and add env vars to Command Manifest
	if entry.Spec.CommandManifest.Server.Command != "" {
		// Keep track of existing env vars in the command manifest to avoid duplicates
		existingCmd := make(map[string]struct{})
		for _, env := range entry.Spec.CommandManifest.Server.Env {
			existingCmd[env.Key] = struct{}{}
		}

		// Extract variables from command
		extractedCmd := make(map[string]struct{})
		for _, v := range extractEnvVars(entry.Spec.CommandManifest.Server.Command) {
			extractedCmd[v] = struct{}{}
		}

		// Extract variables from args
		for _, arg := range entry.Spec.CommandManifest.Server.Args {
			for _, v := range extractEnvVars(arg) {
				extractedCmd[v] = struct{}{}
			}
		}

		// Add any new vars to the Command Manifest's Env list
		for v := range extractedCmd {
			if _, exists := existingCmd[v]; !exists {
				entry.Spec.CommandManifest.Server.Env = append(entry.Spec.CommandManifest.Server.Env, types.MCPEnv{
					MCPHeader: types.MCPHeader{
						Name:        v,
						Key:         v,
						Description: "Automatically detected variable",
						Sensitive:   true,
						Required:    true,
					},
				})
			}
		}
	}

	// Extract and add env vars to URL Manifest
	if entry.Spec.URLManifest.Server.URL != "" {
		// Keep track of existing env vars in the URL manifest to avoid duplicates
		existingURL := make(map[string]struct{})
		for _, env := range entry.Spec.URLManifest.Server.Env {
			existingURL[env.Key] = struct{}{}
		}

		// Extract variables from URL
		extractedURL := make(map[string]struct{})
		for _, v := range extractEnvVars(entry.Spec.URLManifest.Server.URL) {
			extractedURL[v] = struct{}{}
		}

		// Add any new vars to the URL Manifest's Env list
		for v := range extractedURL {
			if _, exists := existingURL[v]; !exists {
				entry.Spec.URLManifest.Server.Env = append(entry.Spec.URLManifest.Server.Env, types.MCPEnv{
					MCPHeader: types.MCPHeader{
						Name:        v,
						Key:         v,
						Description: "Automatically detected variable",
						Sensitive:   true,
						Required:    true,
					},
				})
			}
		}
	}
}

func convertMCPServer(server v1.MCPServer, credEnv map[string]string) types.MCPServer {
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

	// Check for missing required headers
	for _, header := range server.Spec.Manifest.Headers {
		if !header.Required {
			continue
		}

		if _, ok := credEnv[header.Key]; !ok {
			missingHeaders = append(missingHeaders, header.Key)
		}
	}

	return types.MCPServer{
		Metadata:               MetadataFrom(&server),
		MissingRequiredEnvVars: missingEnvVars,
		MissingRequiredHeaders: missingHeaders,
		Configured:             len(missingEnvVars) == 0 && len(missingHeaders) == 0,
		MCPServerManifest:      server.Spec.Manifest,
		CatalogID:              server.Spec.MCPServerCatalogEntryName,
	}
}
