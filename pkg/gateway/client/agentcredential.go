package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/obot-platform/obot/pkg/gateway/types"
	"gorm.io/gorm"
)

// CreateAgentCredential commits the scoped key and its recoverable secret together.
// A competing create rolls back its key on the credential's unique constraint.
func (c *Client) CreateAgentCredential(ctx context.Context, userID uint, name string, serverIDs []string) error {
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		key, err := c.createAPIKey(tx, userID, "Agent "+name, "Claude Code POC", nil, types.APIKeyScopes{
			CanAccessLLMProxy: true,
			MCPServerIDs:      serverIDs,
		})
		if err != nil {
			return err
		}

		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}

		credential := types.Credential{
			Context: "substrate-agent",
			Name:    name,
			Secrets: map[string]string{
				"key":   key.Key,
				"keyID": strconv.FormatUint(uint64(key.ID), 10),
				"token": hex.EncodeToString(token[:]),
			},
		}
		if err := c.encryptCredential(ctx, &credential); err != nil {
			return err
		}

		return tx.Create(&credential).Error
	})
}
