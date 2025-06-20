package authz

import (
	"maps"
	"net/http"
	"slices"

	"k8s.io/apiserver/pkg/authentication/user"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AdminGroup           = "admin"
	AuthenticatedGroup   = "authenticated"
	MetricsGroup         = "metrics"
	UnauthenticatedGroup = "unauthenticated"

	// anyGroup is an internal group that allows access to any group
	anyGroup = "*"
)

var staticRules = map[string][]string{
	AdminGroup: {
		// Yay! Everything
		"/",
	},
	anyGroup: {
		// Allow access to the oauth2 endpoints
		"/oauth2/",

		"POST /api/webhooks/{namespace}/{id}",
		"GET /api/token-request/{id}",
		"POST /api/token-request",
		"GET /api/token-request/{id}/{service}",

		"GET /api/oauth/start/{id}/{namespace}/{name}",

		"GET /api/bootstrap",
		"POST /api/bootstrap/login",
		"POST /api/bootstrap/logout",

		"GET /api/app-oauth/authorize/{id}",
		"GET /api/app-oauth/refresh/{id}",
		"GET /api/app-oauth/callback/{id}",
		"GET /api/app-oauth/get-token/{id}",
		"GET /api/app-oauth/get-token",

		"POST /api/sendgrid",

		"GET /api/healthz",

		"GET /api/auth-providers",
		"GET /api/auth-providers/{id}",

		"POST /api/slack/events",

		// Allow public access to read display info for featured Obots
		// This is used in the unauthenticated landing page
		"GET /api/shares",
		"GET /api/templates",
		"GET /api/tool-references",

		"/mcp-connect/{mcp_server_id}",

		"GET /api/mcp/catalog",
		"GET /api/mcp/catalog/{id}",

		"/api/mcp/{mcp_server_id}",

		"GET /.well-known/",
		"POST /oauth/register",
		"GET /oauth/authorize",
		"POST /oauth/token",
	},

	AuthenticatedGroup: {
		"/api/oauth/redirect/{namespace}/{name}",
		"/api/assistants",
		"GET /api/me",
		"DELETE /api/me",
		"POST /api/llm-proxy/",
		"POST /api/prompt",
		"GET /api/models",
		"GET /api/version",
		"POST /api/image/generate",
		"POST /api/image/upload",
		"POST /api/logout-all",

		// Allow authenticated users to read and accept/reject project invitations.
		// The security depends on the code being an unguessable UUID string,
		// which is the project owner shares with the user that they are inviting.
		"GET /api/projectinvitations/{code}",
		"POST /api/projectinvitations/{code}",
		"DELETE /api/projectinvitations/{code}",

		// Allow authenticated users to read servers and entries from MCP catalogs.
		// The authz logic is handled in the routes themselves, for now.
		"GET /api/all-mcp-catalogs/entries",
		"GET /api/all-mcp-catalogs/entries/{entry_id}",
		"GET /api/all-mcp-catalogs/servers",
		"GET /api/all-mcp-catalogs/servers/{mcp_server_id}",
	},

	MetricsGroup: {
		"/debug/metrics",
	},
}

var devModeRules = map[string][]string{
	anyGroup: {
		"/node_modules/",
		"/@fs/",
		"/.svelte-kit/",
		"/@vite/",
		"/@id/",
		"/src/",
	},
}

type Authorizer struct {
	rules        []rule
	storage      kclient.Client
	apiResources *pathMatcher
	uiResources  *pathMatcher
}

func NewAuthorizer(storage kclient.Client, devMode bool) *Authorizer {
	return &Authorizer{
		rules:        defaultRules(devMode),
		storage:      storage,
		apiResources: newPathMatcher(apiResources...),
		uiResources:  newPathMatcher(uiResources...),
	}
}

func (a *Authorizer) Authorize(req *http.Request, user user.Info) bool {
	userGroups := user.GetGroups()
	for _, r := range a.rules {
		if r.group == anyGroup || slices.Contains(userGroups, r.group) {
			if _, pattern := r.mux.Handler(req); pattern != "" {
				return true
			}
		}
	}

	return a.authorizeAPIResources(req, user) || a.checkOAuthClient(req) || a.checkUI(req)
}

type rule struct {
	group string
	mux   *http.ServeMux
}

func defaultRules(devMode bool) []rule {
	var (
		rules []rule
		f     = (*fake)(nil)
	)

	for _, group := range slices.Sorted(maps.Keys(staticRules)) {
		rule := rule{
			group: group,
			mux:   http.NewServeMux(),
		}
		for _, url := range staticRules[group] {
			rule.mux.Handle(url, f)
		}
		rules = append(rules, rule)
	}

	if devMode {
		for _, group := range slices.Sorted(maps.Keys(devModeRules)) {
			rule := rule{
				group: group,
				mux:   http.NewServeMux(),
			}
			for _, url := range devModeRules[group] {
				rule.mux.Handle(url, f)
			}
			rules = append(rules, rule)
		}
	}

	return rules
}

// fake is a fake handler that does fake things
type fake struct{}

func (f *fake) ServeHTTP(http.ResponseWriter, *http.Request) {}
