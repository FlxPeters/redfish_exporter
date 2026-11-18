//go:build platform_examples

package collector

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// exampleSKUB is another server type whose BMC needs the same power read as exampleSKUA's. It
// names that cell where it is defined: every file compiles into one package, so a row can name
// a cell or collector written for another row. It keeps the public system collector, so it has
// no Collectors entry.
var exampleSKUB = Platform{
	Name: "EXAMPLE-SKU-B-EXAMPLE-FW-1",

	// Public reads, the same ones Default names, defined in gpu_collector.go
	// (nvlinkPortsExpand) and chassis_collector.go (the rest).
	NVLink:               (*GPUCollector).nvlinkPortsExpand,
	ChassisSensors:       (*ChassisCollector).skipSensors,
	ChassisLeakDetection: (*ChassisCollector).getLeakDetectors,
	ChassisTemperatures:  (*ChassisCollector).collectThermalMetrics,
	ChassisList:          (*ChassisCollector).listChassis,

	// The cell written for exampleSKUA: chassis_collector_example_power_test.go.
	ChassisPower: (*ChassisCollector).collectExamplePower,
}

// TestExampleSKUB runs exampleSKUB with the fake BMC and checks from exampleSKUA's tests: the
// chassis collector runs exampleSKUA's cell, and the system prober keeps the public collector.
func TestExampleSKUB(t *testing.T) {
	requireExamplePowerCell(t, collectChassisWith(t, newExamplePowerServer(t), exampleSKUB))
	require.IsType(t, &SystemCollector{}, buildExampleSystemCollector(t, exampleSKUB))
}
