package handlers

import (
	"fmt"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MCPWebhookValidationHandler struct{}

func NewMCPWebhookValidationHandler() *MCPWebhookValidationHandler {
	return &MCPWebhookValidationHandler{}
}

func (m *MCPWebhookValidationHandler) List(req api.Context) error {
	var list v1.MCPWebhookValidationList
	if err := req.List(&list); err != nil {
		return fmt.Errorf("failed to list mcp webhook validations: %w", err)
	}

	items := make([]types.MCPWebhookValidation, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, convertMCPWebhookValidation(item))
	}

	return req.Write(types.MCPWebhookValidationList{Items: items})
}

func (m *MCPWebhookValidationHandler) Get(req api.Context) error {
	var validation v1.MCPWebhookValidation
	if err := req.Get(&validation, req.PathValue("mcp_webhook_validation_id")); err != nil {
		return err
	}

	return req.Write(convertMCPWebhookValidation(validation))
}

func (m *MCPWebhookValidationHandler) Create(req api.Context) error {
	var manifest types.MCPWebhookValidationManifest
	if err := req.Read(&manifest); err != nil {
		return types.NewErrBadRequest("failed to read manifest: %v", err)
	}

	validation := v1.MCPWebhookValidation{
		ObjectMeta: metav1.ObjectMeta{
			//GenerateName: system.MCPWebhookValidationPrefix,
			Namespace: req.Namespace(),
		},
		Spec: v1.MCPWebhookValidationSpec{
			Manifest: manifest,
		},
	}

	if err := manifest.Validate(); err != nil {
		return types.NewErrBadRequest("invalid manifest: %v", err)
	}

	if err := req.Create(&validation); err != nil {
		return fmt.Errorf("failed to create mcp webhook validation: %w", err)
	}

	return req.Write(convertMCPWebhookValidation(validation))
}

func (m *MCPWebhookValidationHandler) Update(req api.Context) error {
	var validation v1.MCPWebhookValidation
	if err := req.Get(&validation, req.PathValue("mcp_webhook_validation_id")); err != nil {
		return err
	}

	var manifest types.MCPWebhookValidationManifest
	if err := req.Read(&manifest); err != nil {
		return types.NewErrBadRequest("failed to read manifest: %v", err)
	}

	if err := manifest.Validate(); err != nil {
		return types.NewErrBadRequest("invalid manifest: %v", err)
	}

	validation.Spec.Manifest = manifest

	if err := req.Update(&validation); err != nil {
		return fmt.Errorf("failed to update mcp webhook validation: %w", err)
	}

	return req.Write(convertMCPWebhookValidation(validation))
}

func (m *MCPWebhookValidationHandler) Delete(req api.Context) error {
	var validation v1.MCPWebhookValidation
	if err := req.Get(&validation, req.PathValue("mcp_webhook_validation_id")); err != nil {
		return err
	}

	if err := req.Delete(&validation); err != nil {
		return fmt.Errorf("failed to delete mcp webhook validation: %w", err)
	}

	return req.Write(convertMCPWebhookValidation(validation))
}

func convertMCPWebhookValidation(validation v1.MCPWebhookValidation) types.MCPWebhookValidation {
	return types.MCPWebhookValidation{
		Metadata:                     MetadataFrom(&validation),
		MCPWebhookValidationManifest: validation.Spec.Manifest,
	}
}
