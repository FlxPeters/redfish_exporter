//go:build platform_examples

package collector

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// exampleSKUAFW2 is exampleSKUA's server type on newer firmware that behaves the same, so it
// copies that row and changes only the key. Platform.Collectors is a map and the copy shares it:
// give the copy its own map before changing an entry, or the change reaches exampleSKUA too.
var exampleSKUAFW2 = func() Platform {
	p := exampleSKUA
	p.Name = "EXAMPLE-SKU-A-EXAMPLE-FW-2"
	return p
}()

// TestExampleSKUAFW2 checks that the copied row has its own key, leaves exampleSKUA's alone, and
// reaches the same cell and collector.
func TestExampleSKUAFW2(t *testing.T) {
	require.Equal(t, "EXAMPLE-SKU-A-EXAMPLE-FW-2", exampleSKUAFW2.Name)
	require.Equal(t, "EXAMPLE-SKU-A-EXAMPLE-FW-1", exampleSKUA.Name)
	requireExamplePowerCell(t, collectChassisWith(t, newExamplePowerServer(t), exampleSKUAFW2))
	require.IsType(t, &exampleSystemCollector{}, buildExampleSystemCollector(t, exampleSKUAFW2))
}
