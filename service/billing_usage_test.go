package service

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEffectiveBillingUsagePreservesPassthroughCost pins a regression that made
// passthrough billing silently ineffective on channels reporting structured
// BillingUsage (the common case for Claude): effectiveBillingUsage rebuilds a
// fresh Usage from token fields only, so the extracted upstream cost was dropped
// and settlement fell back to per-token local ratios.
func TestEffectiveBillingUsagePreservesPassthroughCost(t *testing.T) {
	cost := 0.12814219930348256

	tests := []struct {
		name  string
		usage *dto.Usage
	}{
		{
			name: "anthropic billing usage",
			usage: &dto.Usage{
				PromptTokens:     6913,
				CompletionTokens: 325,
				UpstreamCostUSD:  &cost,
				BillingUsage: &dto.BillingUsage{
					Source:   dto.BillingUsageSourceClaudeMessages,
					Semantic: dto.BillingUsageSemanticAnthropic,
					ClaudeUsage: &dto.ClaudeUsage{
						InputTokens:  6913,
						OutputTokens: 325,
					},
				},
			},
		},
		{
			name: "openai billing usage",
			usage: &dto.Usage{
				PromptTokens:     100,
				CompletionTokens: 20,
				UpstreamCostUSD:  &cost,
				BillingUsage: &dto.BillingUsage{
					Source:      dto.BillingUsageSourceOAIChat,
					Semantic:    dto.BillingUsageSemanticOpenAI,
					OpenAIUsage: &dto.Usage{PromptTokens: 100, CompletionTokens: 20},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			effective := effectiveBillingUsage(tc.usage)

			// Confirm the remap actually happened (otherwise the test is vacuous).
			require.NotSame(t, tc.usage, effective)
			require.NotNil(t, effective.UpstreamCostUSD)
			assert.Equal(t, cost, *effective.UpstreamCostUSD)
		})
	}
}

// TestEffectiveBillingUsageWithoutPassthroughCostStaysNil ensures the carry-over
// does not invent a cost when none was extracted.
func TestEffectiveBillingUsageWithoutPassthroughCostStaysNil(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens: 10,
		BillingUsage: &dto.BillingUsage{
			Source:      dto.BillingUsageSourceClaudeMessages,
			Semantic:    dto.BillingUsageSemanticAnthropic,
			ClaudeUsage: &dto.ClaudeUsage{InputTokens: 10, OutputTokens: 1},
		},
	}

	effective := effectiveBillingUsage(usage)

	require.NotSame(t, usage, effective)
	assert.Nil(t, effective.UpstreamCostUSD)
}
