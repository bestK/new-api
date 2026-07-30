package service

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/tidwall/gjson"
)

// PassthroughMaxCostUSD is the hard upper bound (in USD) for a single request's
// upstream-reported cost. Upstream cost values are untrusted, so any amount
// above this ceiling is rejected to prevent runaway or wrapped-negative charges.
const PassthroughMaxCostUSD = 100.0

// fallbackPassthroughCostPath is used when a channel neither configures a path
// nor matches a known channel-type default.
const fallbackPassthroughCostPath = "usage.cost"

// passthroughCostPathByChannelType maps a channel type to the gjson path where
// that upstream reports the per-request cost. Channels may override it with an
// explicit PassthroughCostPath.
var passthroughCostPathByChannelType = map[int]string{
	constant.ChannelTypeOpenRouter: "usage.cost",
	constant.ChannelTypeAnthropic:  "usage.credit_usage",
	constant.ChannelTypeOpenAI:     "usage.cost",
}

// DefaultPassthroughCostPath returns the cost path for a channel type, used as
// the default when the channel has no explicit override.
func DefaultPassthroughCostPath(channelType int) string {
	if path, ok := passthroughCostPathByChannelType[channelType]; ok {
		return path
	}
	return fallbackPassthroughCostPath
}

// ExtractPassthroughCost reads the upstream-reported cost from the raw response
// body and, when valid, stores it (normalized to USD) on usage.UpstreamCostUSD
// for passthrough billing at settle time.
//
// Safe to call per streaming chunk: the cost is reported on an intermediate
// event (Anthropic puts credit_usage on message_delta, not the terminal
// message_stop), so callers feed every chunk through and the last chunk that
// carries a valid amount wins. A chunk without a cost never clears a value
// already extracted from an earlier one.
//
// It is a no-op unless the request's channel has passthrough billing enabled.
// Upstream cost values are untrusted, so the extracted amount is bounded:
// NaN/Inf and negative values are rejected, and any amount above
// PassthroughMaxCostUSD is dropped (billing falls back to the local
// ratio/price) and logged, so a single request can never produce a runaway or
// wrapped-negative charge.
func ExtractPassthroughCost(info *relaycommon.RelayInfo, usage *dto.Usage, responseBody []byte) {
	if info == nil || usage == nil || len(responseBody) == 0 {
		return
	}
	// ChannelMeta is an embedded pointer only populated after InitChannelMeta.
	if info.ChannelMeta == nil || !info.ChannelOtherSettings.PassthroughBillingEnabled {
		return
	}

	path := info.ChannelOtherSettings.PassthroughCostPath
	if path == "" {
		path = DefaultPassthroughCostPath(info.ChannelType)
	}

	result := gjson.GetBytes(responseBody, path)
	if !result.Exists() {
		// Streaming sends several usage-bearing events (Anthropic's message_start
		// carries usage but never credit_usage), so a per-chunk miss is normal and
		// must not be reported here. The last usage seen is remembered instead and
		// ReportPassthroughPathMissIfUnresolved decides once the response is done.
		if usageNode := findUsageNode(responseBody); usageNode != "" {
			info.PassthroughLastUsageSeen = usageNode
		}
		return
	}

	// Accept JSON numbers and numeric strings; anything else is not a cost.
	switch result.Type {
	case gjson.Number, gjson.String:
	default:
		common.SysError(fmt.Sprintf("passthrough billing: channel #%d model %s cost at %q is not a number (%s), falling back to local pricing", info.ChannelId, info.OriginModelName, path, truncateForLog(result.Raw, 120)))
		return
	}
	cost := result.Float()

	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		common.SysError(fmt.Sprintf("passthrough billing: channel #%d model %s upstream cost is not finite (%v), falling back to local pricing", info.ChannelId, info.OriginModelName, result.Raw))
		return
	}
	// Normalize to USD before any bound check so the cap always means USD.
	if strings.EqualFold(info.ChannelOtherSettings.PassthroughCostUnit, dto.PassthroughCostUnitCents) {
		cost /= 100
	}
	if cost < 0 {
		common.SysError(fmt.Sprintf("passthrough billing: channel #%d model %s upstream cost is negative (%g), falling back to local pricing", info.ChannelId, info.OriginModelName, cost))
		return
	}
	if cost > PassthroughMaxCostUSD {
		common.SysError(fmt.Sprintf("passthrough billing: channel #%d model %s upstream cost %g USD exceeds hard cap %g, falling back to local pricing", info.ChannelId, info.OriginModelName, cost, PassthroughMaxCostUSD))
		return
	}

	usage.UpstreamCostUSD = &cost
}

// logPassthroughPathMiss reports an enabled-but-unproductive extraction: the
// channel opted into passthrough billing yet the configured path yielded
// nothing, so the request silently falls back to local ratios. The actual
// usage object is included so the correct path can be identified from the log
// instead of having to capture upstream traffic.
//
// Streaming feeds every chunk through the extractor and most chunks legitimately
// carry no usage, so only bodies that look like they should hold a cost (i.e.
// they contain a usage object) are reported; otherwise every stream would emit
// dozens of false alarms.
// ReportPassthroughPathMissIfUnresolved is called once a response is fully read.
// It reports the case where a channel opted into passthrough billing, the
// upstream did report usage, yet no cost was found at the configured path — so
// the request silently falls back to local ratios. The observed usage object is
// included so the correct path can be read straight from the log.
func ReportPassthroughPathMissIfUnresolved(info *relaycommon.RelayInfo, usage *dto.Usage) {
	if info == nil || info.ChannelMeta == nil || !info.ChannelOtherSettings.PassthroughBillingEnabled {
		return
	}
	if usage != nil && usage.UpstreamCostUSD != nil {
		return // resolved
	}
	if info.PassthroughLastUsageSeen == "" {
		return // upstream never reported usage; nothing to diagnose
	}
	path := info.ChannelOtherSettings.PassthroughCostPath
	if path == "" {
		path = DefaultPassthroughCostPath(info.ChannelType)
	}
	common.SysError(fmt.Sprintf(
		"passthrough billing: channel #%d model %s found no cost at %q; upstream usage was %s. Set the channel's passthrough cost path to the correct field, or disable passthrough billing for this channel.",
		info.ChannelId, info.OriginModelName, path, info.PassthroughLastUsageSeen,
	))
}

// findUsageNode returns the raw upstream usage object for diagnostics, checking
// both the top level and the nesting Anthropic uses on message_start
// (message.usage). Empty means the body reported no usage at all.
func findUsageNode(responseBody []byte) string {
	for _, candidate := range []string{"usage", "message.usage"} {
		if node := gjson.GetBytes(responseBody, candidate); node.Exists() {
			return truncateForLog(node.Raw, 400)
		}
	}
	return ""
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
