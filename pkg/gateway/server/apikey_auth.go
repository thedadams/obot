package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/principal"
	"github.com/obot-platform/obot/pkg/system"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
)

// APIKeyAuthenticator authenticates requests using API keys.
// API key users have restricted access - they only get GroupAPIKey,
// not the full authenticated user groups.
type APIKeyAuthenticator struct {
	client *client.Client
}

// NewAPIKeyAuthenticator creates a new API key authenticator.
func NewAPIKeyAuthenticator(client *client.Client) *APIKeyAuthenticator {
	return &APIKeyAuthenticator{client: client}
}

// AuthenticateRequest implements authenticator.Request.
func (a *APIKeyAuthenticator) AuthenticateRequest(req *http.Request) (*authenticator.Response, bool, error) {
	authHeader := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if authHeader == "" {
		authHeader = req.Header.Get("X-API-Key")
		if authHeader == "" {
			return nil, false, nil
		}
	}

	// Check if this value uses the shared API key prefix.
	if !strings.HasPrefix(authHeader, system.APIKeyPrefix+"-") {
		return nil, false, nil
	}

	// Validate the API key
	apiKey, err := a.client.ValidateAPIKey(req.Context(), authHeader)
	if err != nil {
		// Return false, nil to let other authenticators try
		// This allows the chain to continue if the key is invalid
		return nil, false, nil
	}

	// Get the user from the database
	u, authProviderGroups, err := a.client.UserByIDWithEffectiveRole(req.Context(), apiKey.UserID)
	if err != nil {
		return nil, false, nil
	}

	attribution := principal.NewAPIKeyAttribution(apiKey.ID, apiKey.UserID, apiKey.Name)
	groups := apiKey.Groups(u)
	extra := map[string][]string{
		"email":                   {u.Email},
		"authorized_mcp_ids":      apiKey.MCPServerIDs,
		"obot_groups":             u.Role.RoleGroups(),
		principal.APIKeyIDExtra:   {fmt.Sprintf("%d", attribution.ID)},
		principal.APIKeyNameExtra: {attribution.Name},
	}

	if authProviderGroups != nil {
		extra["auth_provider_groups"] = authProviderGroups
	}

	return &authenticator.Response{
		User: &user.DefaultInfo{
			Name:   u.Username,
			UID:    fmt.Sprintf("%d", u.ID),
			Groups: groups,
			Extra:  extra,
		},
	}, true, nil
}
