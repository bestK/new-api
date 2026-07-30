package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAttachUpstreamCostIsAdminOnly pins where the passthrough upstream cost is
// stored. It must live under other["admin_info"], never at the top level:
// model.formatUserLogs strips admin_info for non-admin log views, so top-level
// placement would leak upstream pricing to end users.
func TestAttachUpstreamCostToOther(t *testing.T) {
	t.Run("nests under admin_info", func(t *testing.T) {
		other := map[string]interface{}{"model_ratio": 2.5}

		attachUpstreamCostToOther(other, 0.12814219930348256)

		assert.NotContains(t, other, "upstream_cost_usd", "must not sit at top level")
		adminInfo, ok := other["admin_info"].(map[string]interface{})
		require.True(t, ok, "admin_info should be created")
		assert.Equal(t, 0.12814219930348256, adminInfo["upstream_cost_usd"])
	})

	t.Run("preserves existing admin_info entries", func(t *testing.T) {
		other := map[string]interface{}{
			"admin_info": map[string]interface{}{"usage_billing_path": "billing-usage-anthropic"},
		}

		attachUpstreamCostToOther(other, 0.5)

		adminInfo := other["admin_info"].(map[string]interface{})
		assert.Equal(t, "billing-usage-anthropic", adminInfo["usage_billing_path"])
		assert.Equal(t, 0.5, adminInfo["upstream_cost_usd"])
	})

	t.Run("nil other does not panic", func(t *testing.T) {
		attachUpstreamCostToOther(nil, 1)
	})
}
