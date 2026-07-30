package service

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"

	"github.com/tidwall/gjson"
)

// defaultPassthroughCostPath is the gjson path used to read the upstream cost
// amount when the channel does not configure a custom path. OpenRouter and
// other OpenAI-compatible channels expose the per-request cost at usage.cost.
const defaultPassthroughCostPath = "usage.cost"

// ExtractPassthroughCost reads the upstream-reported cost (USD) from the raw
// response body and, when valid, stores it on usage.UpstreamCostUSD for
// passthrough billing at settle time.
//
// It is a no-op unless the request's model is configured with
// BillingModePassthrough. Upstream cost values are untrusted, so the extracted
// amount is bounded: NaN/Inf and negative values are rejected, and any amount
// above billing_setting.PassthroughMaxCostUSD is dropped (billing falls back to
// the local ratio/price) and logged, so a single request can never produce a
// runaway or wrapped-negative charge.
func ExtractPassthroughCost(info *relaycommon.RelayInfo, usage *dto.Usage, responseBody []byte) {
	if info == nil || usage == nil || len(responseBody) == 0 {
		return
	}
	if !billing_setting.IsPassthrough(info.OriginModelName) {
		return
	}

	path := defaultPassthroughCostPath
	// ChannelMeta is an embedded pointer that is only populated after
	// InitChannelMeta; guard against a nil meta before reading channel settings.
	if info.ChannelMeta != nil && info.ChannelOtherSettings.PassthroughCostPath != "" {
		path = info.ChannelOtherSettings.PassthroughCostPath
	}

	result := gjson.GetBytes(responseBody, path)
	if !result.Exists() {
		return
	}

	// Accept JSON numbers and numeric strings; anything else is not a cost.
	switch result.Type {
	case gjson.Number, gjson.String:
	default:
		return
	}
	cost := result.Float()

	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		common.SysError(fmt.Sprintf("passthrough billing: model %s upstream cost is not finite (%v), falling back to local pricing", info.OriginModelName, result.Raw))
		return
	}
	if cost < 0 {
		common.SysError(fmt.Sprintf("passthrough billing: model %s upstream cost is negative (%g), falling back to local pricing", info.OriginModelName, cost))
		return
	}
	if cost > billing_setting.PassthroughMaxCostUSD {
		common.SysError(fmt.Sprintf("passthrough billing: model %s upstream cost %g exceeds hard cap %g USD, falling back to local pricing", info.OriginModelName, cost, billing_setting.PassthroughMaxCostUSD))
		return
	}

	usage.UpstreamCostUSD = &cost
}
