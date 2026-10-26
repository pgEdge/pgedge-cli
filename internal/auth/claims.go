package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims are the JWT payload fields the CLI reads from a user's tokens.
type Claims struct {
	Subject   string   `json:"sub"`
	Email     string   `json:"email"`
	Expiry    int64    `json:"exp"`
	TenantID  string   `json:"pgedge_tenant_id"`
	TenantIDs []string `json:"pgedge_tenant_ids"`
}

// ParseClaims decodes a JWT's payload. The signature is not checked:
// the API verifies every token it receives, and the CLI only reads it.
func ParseClaims(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("auth: token is not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(
		strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("auth: decode token payload: %w", err)
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("auth: parse token payload: %w", err)
	}
	return &c, nil
}

// Tenants lists every tenant the token may act in.
func (c *Claims) Tenants() []string {
	if len(c.TenantIDs) > 0 {
		return c.TenantIDs
	}
	if c.TenantID != "" {
		return []string{c.TenantID}
	}
	return nil
}

// HasTenant reports whether id is one of Tenants, ignoring case.
func (c *Claims) HasTenant(id string) bool {
	for _, t := range c.Tenants() {
		if strings.EqualFold(t, id) {
			return true
		}
	}
	return false
}

// ExpiresAt is the exp claim as a time, zero when absent.
func (c *Claims) ExpiresAt() time.Time {
	if c.Expiry == 0 {
		return time.Time{}
	}
	return time.Unix(c.Expiry, 0)
}
