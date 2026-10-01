package handlers

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"regexp"
	"slices"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/substrate"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AgentInstanceHandler struct{ Substrate *substrate.Client }

func (h *AgentInstanceHandler) Config(req api.Context) error {
	return req.Write(map[string]bool{"enabled": h.Substrate != nil})
}

func (h *AgentInstanceHandler) List(req api.Context) error {
	var instances v1.AgentInstanceList
	if err := req.List(&instances, kclient.MatchingFields{"spec.userID": req.User.GetUID()}); err != nil {
		return err
	}
	if instances.Items == nil {
		instances.Items = []v1.AgentInstance{}
	}

	return req.Write(instances)
}

func (h *AgentInstanceHandler) Create(req api.Context) error {
	if h.Substrate == nil {
		return types.NewErrHTTP(http.StatusServiceUnavailable, "Agent POC is disabled")
	}
	var input struct {
		DisplayName  string   `json:"displayName"`
		Model        string   `json:"model"`
		MCPServerIDs []string `json:"mcpServerIDs"`
	}
	if err := req.Read(&input); err != nil {
		return err
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	identifier := regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)
	if input.DisplayName == "" || len(input.DisplayName) > 128 || !identifier.MatchString(input.Model) || len(input.MCPServerIDs) > 32 {
		return types.NewErrBadRequest("name, model resource ID, and at most 32 MCP server IDs are required")
	}
	for _, id := range input.MCPServerIDs {
		if !identifier.MatchString(id) {
			return types.NewErrBadRequest("invalid MCP server ID")
		}
	}
	slices.Sort(input.MCPServerIDs)

	instance := v1.AgentInstance{
		GenerateName: "agi-",
		Namespace:    req.Namespace(),
		Finalizers:   []string{substrate.Finalizer},
		Spec:         v1.AgentInstanceSpec{UserID: req.User.GetUID(), DisplayName: input.DisplayName, Model: input.Model, MCPServerIDs: slices.Compact(input.MCPServerIDs)},
	}
	if err := req.Create(&instance); err != nil {
		return err
	}

	return req.WriteCreated(instance)
}

func (h *AgentInstanceHandler) owned(req api.Context) (*v1.AgentInstance, error) {
	var instance v1.AgentInstance
	if err := req.Get(&instance, req.PathValue("agent_instance_id")); err != nil {
		return nil, err
	}
	if instance.Spec.UserID != req.User.GetUID() {
		return nil, types.NewErrHTTP(http.StatusNotFound, "agent instance not found")
	}

	return &instance, nil
}

func (h *AgentInstanceHandler) Get(req api.Context) error {
	instance, err := h.owned(req)
	if err != nil {
		return err
	}

	return req.Write(instance)
}

func (h *AgentInstanceHandler) SetState(req api.Context) error {
	if h.Substrate == nil {
		return types.NewErrHTTP(http.StatusServiceUnavailable, "Agent POC is disabled")
	}
	instance, err := h.owned(req)
	if err != nil {
		return err
	}
	if instance.DeletionTimestamp != nil {
		return types.NewErrHTTP(http.StatusConflict, "agent is being deleted")
	}
	var input struct {
		Suspended bool `json:"suspended"`
	}
	if err := req.Read(&input); err != nil {
		return err
	}
	instance.Spec.Suspended = input.Suspended
	if err := req.Update(instance); err != nil {
		return err
	}

	return req.Write(instance)
}

func (h *AgentInstanceHandler) Delete(req api.Context) error {
	if h.Substrate == nil {
		return types.NewErrHTTP(http.StatusServiceUnavailable, "Agent POC is disabled")
	}
	instance, err := h.owned(req)
	if err != nil {
		return err
	}
	if err := req.Delete(instance); err != nil {
		return err
	}
	req.WriteHeader(http.StatusNoContent)

	return nil
}

func (h *AgentInstanceHandler) Proxy(req api.Context) error {
	if h.Substrate == nil {
		return types.NewErrHTTP(http.StatusServiceUnavailable, "Agent POC is disabled")
	}
	instance, err := h.owned(req)
	if err != nil {
		return err
	}
	action := req.PathValue("agent_action")
	if (req.Method == http.MethodGet && action != "history") || (req.Method == http.MethodPost && action != "chat" && action != "cancel") {
		return types.NewErrHTTP(http.StatusNotFound, "unknown agent action")
	}
	cancelling := action == "cancel" && (instance.Status.State == "RUNNING" || instance.Status.State == "Suspending")
	if instance.DeletionTimestamp != nil || (!cancelling && (instance.Spec.Suspended || instance.Status.State != "RUNNING" || instance.Status.ObservedGeneration != instance.Generation)) {
		return types.NewErrHTTP(http.StatusConflict, "agent must be running; resume it and wait for readiness")
	}
	credential, err := req.GatewayClient.RevealCredential(req.Context(), []string{"substrate-agent"}, instance.Name)
	if err != nil {
		return err
	}
	req.Request.Body = http.MaxBytesReader(req.ResponseWriter, req.Request.Body, 64<<10)
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(h.Substrate.Router)
			p.Out.URL.Path = "/" + action
			p.Out.URL.RawPath = ""
			p.Out.URL.RawQuery = ""
			p.Out.Header = http.Header{}
			p.Out.Header.Set("Content-Type", "application/json")
			p.Out.Header.Set("Authorization", "Bearer "+credential.Secrets["token"])
			p.Out.Header.Set("ate-target-actor", fmt.Sprintf("%s/%s", substrate.Ref(instance).Atespace, instance.Name))
		},
		ModifyResponse: func(r *http.Response) error {
			contentType := r.Header.Get("Content-Type")
			r.Header = http.Header{"Content-Type": []string{contentType}, "Cache-Control": []string{"no-store"}, "X-Content-Type-Options": []string{"nosniff"}}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Agent runtime unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(req.ResponseWriter, req.Request)

	return nil
}
