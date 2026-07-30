package service

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractPassthroughCost(t *testing.T) {
	const model = "passthrough-extract-model"

	// Enable passthrough for this model and restore afterwards so other tests
	// see the default (ratio) mode.
	billing_setting.SetBillingMode(model, billing_setting.BillingModePassthrough)
	t.Cleanup(func() { billing_setting.SetBillingMode(model, "") })

	tests := []struct {
		name     string
		model    string
		costPath string
		body     string
		wantCost *float64
	}{
		{
			name:     "default path usage.cost numeric",
			model:    model,
			body:     `{"usage":{"prompt_tokens":10,"cost":0.0123}}`,
			wantCost: floatPtr(0.0123),
		},
		{
			name:     "numeric string cost is parsed",
			model:    model,
			body:     `{"usage":{"cost":"0.5"}}`,
			wantCost: floatPtr(0.5),
		},
		{
			name:     "custom gjson path",
			model:    model,
			costPath: "data.billing.amount",
			body:     `{"data":{"billing":{"amount":1.25}}}`,
			wantCost: floatPtr(1.25),
		},
		{
			name:     "zero cost is valid",
			model:    model,
			body:     `{"usage":{"cost":0}}`,
			wantCost: floatPtr(0),
		},
		{
			name:     "at cap boundary is accepted",
			model:    model,
			body:     `{"usage":{"cost":100}}`,
			wantCost: floatPtr(billing_setting.PassthroughMaxCostUSD),
		},
		{
			name:     "above cap is rejected",
			model:    model,
			body:     `{"usage":{"cost":100.01}}`,
			wantCost: nil,
		},
		{
			name:     "negative cost is rejected",
			model:    model,
			body:     `{"usage":{"cost":-1}}`,
			wantCost: nil,
		},
		{
			name:     "missing field yields no cost",
			model:    model,
			body:     `{"usage":{"prompt_tokens":10}}`,
			wantCost: nil,
		},
		{
			name:     "non-numeric type is ignored",
			model:    model,
			body:     `{"usage":{"cost":{"nested":1}}}`,
			wantCost: nil,
		},
		{
			name:     "boolean cost is ignored",
			model:    model,
			body:     `{"usage":{"cost":true}}`,
			wantCost: nil,
		},
		{
			name:     "non-passthrough model is skipped",
			model:    "ratio-model",
			body:     `{"usage":{"cost":0.5}}`,
			wantCost: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: tc.model,
				ChannelMeta:     &relaycommon.ChannelMeta{},
			}
			info.ChannelOtherSettings.PassthroughCostPath = tc.costPath
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

func TestExtractPassthroughCostNilSafety(t *testing.T) {
	// Must not panic on nil/empty inputs.
	ExtractPassthroughCost(nil, &dto.Usage{}, []byte(`{}`))
	ExtractPassthroughCost(&relaycommon.RelayInfo{}, nil, []byte(`{}`))
	ExtractPassthroughCost(&relaycommon.RelayInfo{OriginModelName: "x"}, &dto.Usage{}, nil)
}
