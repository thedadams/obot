// Package substrate connects Obot's instance controller to Substrate v0.3.0.
package substrate

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	pb "github.com/obot-platform/obot/pkg/substrate/ateapipb"
	"github.com/obot-platform/obot/pkg/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const Finalizer = "obot.obot.ai/substrate-agent"

type Config struct {
	Address          string
	RouterURL        string
	CAFile           string
	TokenFile        string
	Image            string
	SnapshotLocation string
	ObotURL          string
}

type Client struct {
	Control pb.ControlClient
	Config  Config
	Router  *url.URL
}

type tokenFile string

func (f tokenFile) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	b, err := os.ReadFile(string(f))
	if err != nil {
		return nil, err
	}

	return map[string]string{"authorization": "Bearer " + strings.TrimSpace(string(b))}, nil
}

func (tokenFile) RequireTransportSecurity() bool { return true }

func New(ctx context.Context, config Config) (*Client, error) {
	if config.Address == "" {
		return nil, nil
	}
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("Substrate address must be host:port")
	}

	router, err := url.Parse(config.RouterURL)
	if err != nil || router.Host == "" || (router.Scheme != "http" && router.Scheme != "https") || router.User != nil {
		return nil, fmt.Errorf("invalid Substrate router URL")
	}
	obot, err := url.Parse(config.ObotURL)
	if err != nil || obot.Host == "" || (obot.Scheme != "http" && obot.Scheme != "https") || obot.User != nil || obot.RawQuery != "" || obot.Fragment != "" {
		return nil, fmt.Errorf("invalid Substrate Obot URL")
	}
	if net.ParseIP(obot.Hostname()) != nil || obot.Hostname() != strings.ToLower(obot.Hostname()) || strings.HasSuffix(obot.Hostname(), ".") {
		return nil, fmt.Errorf("Substrate Obot URL must use a lowercase DNS hostname for egress policy matching")
	}
	if obot.Port() != "" {
		port, err := strconv.Atoi(obot.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid Substrate Obot URL port")
		}
	}
	if config.CAFile == "" || config.TokenFile == "" {
		return nil, fmt.Errorf("Substrate CA and token files are required")
	}
	if !regexp.MustCompile(`@sha256:[a-f0-9]{64}$`).MatchString(config.Image) {
		return nil, fmt.Errorf("Substrate agent image must be pinned by sha256 digest")
	}
	snapshot, err := url.Parse(config.SnapshotLocation)
	if err != nil || (snapshot.Scheme != "s3" && snapshot.Scheme != "gs") || snapshot.Host == "" {
		return nil, fmt.Errorf("Substrate snapshot location must be an s3:// or gs:// bucket URL")
	}
	config.ObotURL = strings.TrimRight(config.ObotURL, "/")

	// Reload the projected trust bundle on each handshake, including CA rotations.
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: true} // Verification is performed below against the current bundle.
	tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("Substrate CA bundle contains no certificates")
		}
		intermediates := x509.NewCertPool()
		for _, cert := range cs.PeerCertificates[1:] {
			intermediates.AddCert(cert)
		}
		_, err = cs.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: intermediates})
		return err
	}
	conn, err := grpc.NewClient(config.Address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(tokenFile(config.TokenFile)))
	if err != nil {
		return nil, err
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	return &Client{Control: pb.NewControlClient(conn), Config: config, Router: router}, nil
}

func Ref(instance *v1.AgentInstance) *pb.ObjectRef {
	owner := sha256.Sum256([]byte(instance.Spec.UserID))
	return &pb.ObjectRef{Atespace: fmt.Sprintf("obot-%x", owner[:12]), Name: instance.Name}
}

func (c *Client) Ensure(ctx context.Context, instance *v1.AgentInstance, secrets map[string]string) (*pb.Actor, error) {
	ref := Ref(instance)
	_, err := c.Control.CreateAtespace(ctx, &pb.CreateAtespaceRequest{Atespace: &pb.Atespace{Metadata: &pb.ResourceMetadata{Name: ref.Atespace}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return nil, err
	}

	_, err = c.Control.GetActorTemplate(ctx, &pb.GetActorTemplateRequest{ActorTemplate: ref})
	if status.Code(err) == codes.NotFound {
		mcpServers := map[string]any{}
		for _, id := range instance.Spec.MCPServerIDs {
			mcpServers[id] = map[string]any{"type": "http", "url": system.MCPConnectURL(c.Config.ObotURL, id), "headers": map[string]string{"Authorization": "Bearer " + secrets["key"]}}
		}
		mcpJSON, err := json.Marshal(mcpServers)
		if err != nil {
			return nil, err
		}
		_, err = c.Control.CreateActorTemplate(ctx, &pb.CreateActorTemplateRequest{ActorTemplate: &pb.ActorTemplate{
			Metadata:       &pb.ResourceMetadata{Atespace: ref.Atespace, Name: ref.Name},
			WorkerSelector: &pb.Selector{MatchLabels: map[string]string{"obot.ai/agent-runtime": "claude-code"}},
			SandboxConfig:  &pb.SandboxConfig{SandboxClass: pb.SandboxClass_SANDBOX_CLASS_GVISOR, ConfigName: "gvisor-default"},
			Resources:      &pb.Resources{Limits: []*pb.Limits{{Name: "cpu", Quantity: "1"}, {Name: "memory", Quantity: "2Gi"}}},
			// Data snapshots preserve the workspace and SDK session, without restoring open HTTP streams.
			SnapshotConfig: &pb.SnapshotConfig{OnPause: pb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA, OnCommit: pb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA, OnResume: &pb.OnResumeConfig{FromData: pb.ResumeSource_RESUME_SOURCE_COLD_BOOT}, StorageLocation: c.Config.SnapshotLocation},
			Volumes:        []*pb.Volume{{Name: "workspace", DurableDir: &pb.DurableDirVolumeSource{}}},
			Containers: []*pb.Container{{Name: "claude", Image: c.Config.Image,
				VolumeMounts: []*pb.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
				WakeupProbe:  &pb.ContainerWakeupProbe{HttpGet: &pb.HTTPGetAction{Path: "/healthz", Port: 80}, TimeoutSeconds: 120},
				Env: []*pb.EnvVar{
					{Name: "ANTHROPIC_BASE_URL", Value: c.Config.ObotURL + "/api/llm-proxy/anthropic"},
					{Name: "ANTHROPIC_AUTH_TOKEN", Value: secrets["key"]},
					{Name: "OBOT_AGENT_TOKEN", Value: secrets["token"]},
					{Name: "OBOT_AGENT_MODEL", Value: instance.Spec.Model},
					{Name: "OBOT_AGENT_MCPS", Value: string(mcpJSON)},
				},
			}},
		}})
		if err != nil && status.Code(err) != codes.AlreadyExists {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	actor, err := c.Control.GetActor(ctx, &pb.GetActorRequest{Actor: ref})
	if status.Code(err) == codes.NotFound {
		actor, err = c.Control.CreateActor(ctx, &pb.CreateActorRequest{Actor: &pb.Actor{Metadata: &pb.ResourceMetadata{Atespace: ref.Atespace, Name: ref.Name}, ActorTemplate: ref}})
	}
	if err != nil {
		return nil, err
	}

	// Egress is restricted to Obot; model and MCP authorization stays in its gateway.
	obot, _ := url.Parse(c.Config.ObotURL)
	port := int32(80)
	if obot.Scheme == "https" {
		port = 443
	}
	if obot.Port() != "" {
		p, err := strconv.ParseInt(obot.Port(), 10, 32)
		if err != nil {
			return nil, err
		}
		port = int32(p)
	}
	rule := &pb.EgressRule{}
	ports := &pb.Ports{Numbers: []int32{port}}
	if obot.Scheme == "https" {
		rule.TlsPassthrough = &pb.TLSPassthroughRule{Hostnames: []string{obot.Hostname()}, Ports: ports}
	} else {
		rule.Http = &pb.HTTPRule{Hostnames: []string{obot.Hostname()}, Ports: ports}
	}
	_, err = c.Control.CreateActorEgressPolicy(ctx, &pb.CreateActorEgressPolicyRequest{Actor: ref, EgressPolicy: &pb.EgressPolicy{Metadata: &pb.ResourceMetadata{Atespace: ref.Atespace, Name: "default"}, Rules: []*pb.EgressRule{rule}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return nil, err
	}

	return actor, nil
}

func (c *Client) RuntimeControl(ctx context.Context, instance *v1.AgentInstance, token, action string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u := *c.Router
	u.Path = "/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("ate-target-actor", Ref(instance).Atespace+"/"+instance.Name)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("waiting for agent to finish its active turn (runtime status %d)", resp.StatusCode)
	}

	return nil
}
