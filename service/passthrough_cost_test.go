package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractPassthroughCost(t *testing.T) {
	tests := []struct {
		name        string
		enabled     bool
		channelType int
		costPath    string
		costUnit    string
		body        string
		wantCost    *float64
	}{
		{
			name:        "openrouter default path usage.cost",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"prompt_tokens":10,"cost":0.0123}}`,
			wantCost:    floatPtr(0.0123),
		},
		{
			// Real Anthropic non-stream shape: cost at $.usage.credit_usage.
			name:        "anthropic default path usage.credit_usage",
			enabled:     true,
			channelType: constant.ChannelTypeAnthropic,
			body:        `{"type":"message","usage":{"input_tokens":4395,"output_tokens":201,"credit_unit":"credit","credit_usage":0.04928818641791045}}`,
			wantCost:    floatPtr(0.04928818641791045),
		},
		{
			name:        "unknown channel type falls back to usage.cost",
			enabled:     true,
			channelType: 9999,
			body:        `{"usage":{"cost":0.75}}`,
			wantCost:    floatPtr(0.75),
		},
		{
			name:        "explicit path overrides channel-type default",
			enabled:     true,
			channelType: constant.ChannelTypeAnthropic,
			costPath:    "data.billing.amount",
			body:        `{"data":{"billing":{"amount":1.25}},"usage":{"credit_usage":9}}`,
			wantCost:    floatPtr(1.25),
		},
		{
			name:        "cents unit is normalized to USD",
			enabled:     true,
			channelType: constant.ChannelTypeAnthropic,
			costUnit:    dto.PassthroughCostUnitCents,
			body:        `{"usage":{"credit_usage":250}}`,
			wantCost:    floatPtr(2.5),
		},
		{
			name:        "numeric string cost is parsed",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"cost":"0.5"}}`,
			wantCost:    floatPtr(0.5),
		},
		{
			name:        "zero cost is valid",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"cost":0}}`,
			wantCost:    floatPtr(0),
		},
		{
			name:        "at cap boundary is accepted",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"cost":100}}`,
			wantCost:    floatPtr(PassthroughMaxCostUSD),
		},
		{
			name:        "above cap is rejected",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"cost":100.01}}`,
			wantCost:    nil,
		},
		{
			// 20000 cents = 200 USD, above the cap once normalized.
			name:        "cents above cap after normalization is rejected",
			enabled:     true,
			channelType: constant.ChannelTypeAnthropic,
			costUnit:    dto.PassthroughCostUnitCents,
			body:        `{"usage":{"credit_usage":20000}}`,
			wantCost:    nil,
		},
		{
			name:        "negative cost is rejected",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"cost":-1}}`,
			wantCost:    nil,
		},
		{
			name:        "missing field yields no cost",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"prompt_tokens":10}}`,
			wantCost:    nil,
		},
		{
			name:        "non-numeric type is ignored",
			enabled:     true,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"cost":{"nested":1}}}`,
			wantCost:    nil,
		},
		{
			name:        "disabled channel is skipped",
			enabled:     false,
			channelType: constant.ChannelTypeOpenRouter,
			body:        `{"usage":{"cost":0.5}}`,
			wantCost:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: "passthrough-model",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: tc.channelType},
			}
			info.ChannelOtherSettings.PassthroughBillingEnabled = tc.enabled
			info.ChannelOtherSettings.PassthroughCostPath = tc.costPath
			info.ChannelOtherSettings.PassthroughCostUnit = tc.costUnit
			usage := &dto.Usage{}

			ExtractPassthroughCost(info, usage, []byte(tc.body))

			if tc.wantCost == nil {
				assert.Nil(t, usage.UpstreamCostUSD)
				return
			}
			require.NotNil(t, usage.UpstreamCostUSD)
			assert.Equal(t, *tc.wantCost, *usage.UpstreamCostUSD)
		})
	}
}

// TestExtractPassthroughCostAnthropicStreamChunks feeds a real Anthropic SSE
// sequence chunk by chunk. credit_usage rides on message_delta, while the
// terminal message_stop carries no usage — so extracting only from the last
// chunk would silently lose the cost. The value from message_delta must survive.
func TestExtractPassthroughCostAnthropicStreamChunks(t *testing.T) {
	chunks := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":4395,"output_tokens":1}}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
		`{"delta":{"stop_reason":"end_turn","stop_sequence":null},"type":"message_delta","usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"credit_unit":"credit","credit_unit_plural":"credits","credit_usage":0.04928818641791045,"input_tokens":4395,"output_tokens":201}}`,
		`{"type":"message_stop"}`,
	}

	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-sonnet",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
	}
	info.ChannelOtherSettings.PassthroughBillingEnabled = true
	usage := &dto.Usage{}

	for _, chunk := range chunks {
		ExtractPassthroughCost(info, usage, []byte(chunk))
	}

	require.NotNil(t, usage.UpstreamCostUSD)
	assert.Equal(t, 0.04928818641791045, *usage.UpstreamCostUSD)
}

// TestExtractPassthroughCostPathMissLeavesCostUnset covers the diagnostic gap
// that made a misconfigured path indistinguishable from a disabled channel: a
// body carrying a usage object but no cost at the configured path must leave the
// cost unset (falling back to local ratios) rather than charging zero. Stream
// chunks without any usage object must stay silent so they do not spam the log.
func TestExtractPassthroughCostPathMissLeavesCostUnset(t *testing.T) {
	for _, body := range []string{
		// usage present but no cost at the configured path -> reported
		`{"usage":{"input_tokens":10,"output_tokens":2}}`,
		// no usage at all (typical mid-stream chunk) -> silently ignored
		`{"type":"content_block_delta","delta":{"text":"hi"}}`,
	} {
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-sonnet",
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
		}
		info.ChannelOtherSettings.PassthroughBillingEnabled = true
		usage := &dto.Usage{}

		ExtractPassthroughCost(info, usage, []byte(body))

		assert.Nil(t, usage.UpstreamCostUSD, "body: %s", body)
	}
}

// TestPassthroughPathMissDiagnostic pins the two-phase diagnostic: a per-chunk
// miss only records the usage it saw (Anthropic's message_start legitimately has
// usage without credit_usage, so reporting per chunk would spam the log), and the
// end-of-response reporter fires only when the whole response failed to resolve.
func TestPassthroughPathMissDiagnostic(t *testing.T) {
	newInfo := func() *relaycommon.RelayInfo {
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-sonnet",
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
		}
		info.ChannelOtherSettings.PassthroughBillingEnabled = true
		return info
	}

	t.Run("stream resolving on message_delta records no unresolved usage", func(t *testing.T) {
		info := newInfo()
		usage := &dto.Usage{}
		// message_start: usage present, no credit_usage -> remembered, not reported
		ExtractPassthroughCost(info, usage, []byte(`{"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":1}}}`))
		require.NotEmpty(t, info.PassthroughLastUsageSeen)
		// message_delta carries the cost -> resolved
		ExtractPassthroughCost(info, usage, []byte(`{"type":"message_delta","usage":{"credit_usage":0.0147,"input_tokens":4138,"output_tokens":43}}`))
		require.NotNil(t, usage.UpstreamCostUSD)

		// Resolved responses must not be reported even though a miss was recorded.
		ReportPassthroughPathMissIfUnresolved(info, usage)
	})

	t.Run("usage without cost anywhere stays unresolved", func(t *testing.T) {
		info := newInfo()
		usage := &dto.Usage{}
		ExtractPassthroughCost(info, usage, []byte(`{"usage":{"input_tokens":10,"output_tokens":2}}`))
		assert.Nil(t, usage.UpstreamCostUSD)
		assert.Contains(t, info.PassthroughLastUsageSeen, "input_tokens")
	})

	t.Run("chunks without usage record nothing", func(t *testing.T) {
		info := newInfo()
		ExtractPassthroughCost(info, &dto.Usage{}, []byte(`{"type":"content_block_delta","delta":{"text":"hi"}}`))
		assert.Empty(t, info.PassthroughLastUsageSeen)
	})
}

func TestExtractPassthroughCostNilSafety(t *testing.T) {
	// Must not panic on nil/empty inputs, including a nil ChannelMeta.
	ExtractPassthroughCost(nil, &dto.Usage{}, []byte(`{}`))
	ExtractPassthroughCost(&relaycommon.RelayInfo{}, nil, []byte(`{}`))
	ExtractPassthroughCost(&relaycommon.RelayInfo{}, &dto.Usage{}, []byte(`{"usage":{"cost":1}}`))
	ExtractPassthroughCost(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, &dto.Usage{}, nil)
}
