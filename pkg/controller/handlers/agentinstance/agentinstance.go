package agentinstance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/obot-platform/nah/pkg/router"
	"github.com/obot-platform/obot/pkg/gateway/client"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/substrate"
	pb "github.com/obot-platform/obot/pkg/substrate/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Handler struct {
	Substrate *substrate.Client
	Gateway   *client.Client
}

func (h *Handler) Reconcile(req router.Request, resp router.Response) error {
	instance := req.Object.(*v1.AgentInstance)
	previous := instance.Status
	ctx, cancel := context.WithTimeout(req.Ctx, 30*time.Second)
	defer cancel()

	state, err := h.reconcile(ctx, instance)
	instance.Status = v1.AgentInstanceStatus{State: state, ObservedGeneration: instance.Generation}
	if err != nil {
		// Do not copy RPC errors into user-visible state: upstream validation may contain environment values.
		slog.ErrorContext(ctx, "Agent reconciliation failed", "instance", instance.Name, "code", status.Code(err))
		instance.Status.Error = "Reconciliation pending; check the Substrate control plane and worker status. An active turn must finish before suspension."
	}
	if previous != instance.Status {
		if err := req.Client.Status().Update(req.Ctx, instance); err != nil {
			return err
		}
	}
	resp.RetryAfter(5 * time.Second)

	return nil
}

func (h *Handler) reconcile(ctx context.Context, instance *v1.AgentInstance) (string, error) {
	credential, err := h.Gateway.RevealCredential(ctx, []string{"substrate-agent"}, instance.Name)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user, err := h.Gateway.UserByID(ctx, instance.Spec.UserID)
		if err != nil {
			return "Pending", err
		}
		if err := h.Gateway.CreateAgentCredential(ctx, user.ID, instance.Name, instance.Spec.MCPServerIDs); err != nil {
			return "Pending", err
		}
		credential, err = h.Gateway.RevealCredential(ctx, []string{"substrate-agent"}, instance.Name)
		if err != nil {
			return "Pending", err
		}
	} else if err != nil {
		return "Pending", err
	}

	actor, err := h.Substrate.Ensure(ctx, instance, credential.Secrets)
	if err != nil {
		return "Pending", err
	}
	state := actor.GetStatus().GetState()
	ref := substrate.Ref(instance)
	if instance.Spec.Suspended {
		if state == pb.ActorState_ACTOR_STATE_RUNNING {
			if err := h.Substrate.RuntimeControl(ctx, instance, credential.Secrets["token"], "quiesce"); err != nil {
				return "Suspending", err
			}
			_, err = h.Substrate.Control.SuspendActor(ctx, &pb.SuspendActorRequest{Actor: ref})
			return "Suspending", err
		}
	} else {
		switch state {
		case pb.ActorState_ACTOR_STATE_SUSPENDED, pb.ActorState_ACTOR_STATE_PAUSED:
			_, err = h.Substrate.Control.ResumeActor(ctx, &pb.ResumeActorRequest{Actor: ref})
			return "Resuming", err
		case pb.ActorState_ACTOR_STATE_RUNNING:
			if err := h.Substrate.RuntimeControl(ctx, instance, credential.Secrets["token"], "activate"); err != nil {
				return "Resuming", err
			}
		}
	}

	return strings.TrimPrefix(state.String(), "ACTOR_STATE_"), nil
}

func (h *Handler) Cleanup(req router.Request, resp router.Response) error {
	instance := req.Object.(*v1.AgentInstance)
	ctx, cancel := context.WithTimeout(req.Ctx, 30*time.Second)
	defer cancel()

	credential, err := h.Gateway.RevealCredential(ctx, []string{"substrate-agent"}, instance.Name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		keyID, err := strconv.ParseUint(credential.Secrets["keyID"], 10, 64)
		if err != nil {
			return err
		}
		if err := h.Gateway.RevokeAPIKeyByID(ctx, uint(keyID)); err != nil {
			return err
		}
	}

	ref := substrate.Ref(instance)
	_, err = h.Substrate.Control.DeleteActor(ctx, &pb.DeleteActorRequest{Actor: ref, AnyState: true})
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	_, err = h.Substrate.Control.GetActor(ctx, &pb.GetActorRequest{Actor: ref})
	if err == nil {
		resp.RetryAfter(5 * time.Second)
		return fmt.Errorf("waiting for actor deletion")
	}
	if status.Code(err) != codes.NotFound {
		return err
	}
	_, err = h.Substrate.Control.DeleteActorTemplate(ctx, &pb.DeleteActorTemplateRequest{ActorTemplate: ref})
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	_, err = h.Gateway.DeleteCredential(ctx, "substrate-agent", instance.Name)

	return err
}
