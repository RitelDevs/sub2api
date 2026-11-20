//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetByKeyForAuthCarriesGroupForceOpenAIFast(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("fast-proj-group-%d", suffix), Platform: service.PlatformOpenAI,
		RateMultiplier: 1, ForceOpenAIFast: true, FreeOpenAIFast: true,
	})
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("fast-proj-%d@example.com", suffix), Concurrency: 5,
	})
	groupID := group.ID
	keyValue := fmt.Sprintf("sk-fast-proj-%d", suffix)
	apiKeyRepo := NewAPIKeyRepository(integrationEntClient, integrationDB)
	key := &service.APIKey{UserID: user.ID, GroupID: &groupID, Key: keyValue, Name: "fast-proj", Status: service.StatusActive}
	require.NoError(t, apiKeyRepo.Create(ctx, key))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')", keyValue)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
		require.NoError(t, err)
	})

	got, err := apiKeyRepo.GetByKeyForAuth(ctx, keyValue)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.ForceOpenAIFast)
	require.True(t, got.Group.FreeOpenAIFast)
}

func (s *APIKeyRepoSuite) TestUserForceOpenAIFastPersistenceAndAuthCache() {
	owner := s.mustCreateUser("fast-owner@example.test")
	other := s.mustCreateUser("fast-other@example.test")
	openaiGroup := s.client.Group.Create().SetName("fast-openai").SetPlatform(service.PlatformOpenAI).SaveX(s.ctx)
	otherGroup := s.client.Group.Create().SetName("fast-other").SetPlatform(service.PlatformAnthropic).SaveX(s.ctx)
	keys := service.NewAPIKeyService(s.repo, NewUserRepository(s.client, integrationDB),
		NewGroupRepository(s.client, integrationDB), nil, nil, nil,
		&config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L1Size: 128, L1TTLSeconds: 300}})

	_, err := keys.Create(s.ctx, owner.ID, service.CreateAPIKeyRequest{Name: "not-openai", GroupID: &otherGroup.ID, ForceOpenAIFast: true})
	s.Require().ErrorContains(err, "requires an OpenAI group")
	key, err := keys.Create(s.ctx, owner.ID, service.CreateAPIKeyRequest{Name: "plain", GroupID: &openaiGroup.ID})
	s.Require().NoError(err)
	load := func(want bool) {
		s.T().Helper()
		got, err := keys.GetByKey(s.ctx, key.Key)
		s.Require().NoError(err)
		s.Require().Equal(want, got.ForceOpenAIFast)
	}
	load(false)
	enabled, disabled := true, false
	_, err = keys.Update(s.ctx, key.ID, other.ID, service.UpdateAPIKeyRequest{ForceOpenAIFast: &enabled})
	s.Require().ErrorIs(err, service.ErrInsufficientPerms)
	_, err = keys.Update(s.ctx, key.ID, owner.ID, service.UpdateAPIKeyRequest{ForceOpenAIFast: &enabled})
	s.Require().NoError(err)
	load(true)
	load(true) // hot snapshot must preserve the preference
	_, err = keys.Update(s.ctx, key.ID, owner.ID, service.UpdateAPIKeyRequest{ForceOpenAIFast: &disabled})
	s.Require().NoError(err)
	load(false)

	created, err := keys.Create(s.ctx, owner.ID, service.CreateAPIKeyRequest{Name: "fast", GroupIDs: []int64{otherGroup.ID, openaiGroup.ID}, ForceOpenAIFast: true})
	s.Require().NoError(err)
	stored, err := s.repo.GetByKeyForAuth(s.ctx, created.Key)
	s.Require().NoError(err)
	s.Require().True(stored.ForceOpenAIFast)
	_, err = keys.Update(s.ctx, created.ID, owner.ID, service.UpdateAPIKeyRequest{GroupID: &otherGroup.ID})
	s.Require().NoError(err)
	stored, err = s.repo.GetByID(s.ctx, created.ID)
	s.Require().NoError(err)
	s.Require().False(stored.ForceOpenAIFast)
}
