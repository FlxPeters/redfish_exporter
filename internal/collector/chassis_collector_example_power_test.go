//go:build platform_examples

package collector

import (
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"
)

var exampleChassisPoweredOnDesc = prometheus.NewDesc(
	"redfish_example_chassis_powered_on",
	"1 when the chassis reports PowerState On. Only the example cell emits it.",
	[]string{"chassis_id"},
	nil,
)

// collectExamplePower is a ChassisPower cell written outside the collector's file. A cell is a
// method on the collector whose signature, receiver first, is the cell field's type, so the row
// names it as (*ChassisCollector).collectExamplePower. It compiles into package collector, so
// it calls the collector's unexported reads as Default's cells do: this one runs the default
// power read, collectPower in chassis_collector.go, then adds a series from a field the chassis
// read already fetched.
func (c *ChassisCollector) collectExamplePower(ch chan<- prometheus.Metric, chassis *schemas.Chassis, logger *slog.Logger) {
	c.collectPower(ch, chassis, logger)

	poweredOn := 0.0
	if chassis.PowerState == schemas.OnPowerState {
		poweredOn = 1
	}
	ch <- prometheus.MustNewConstMetric(exampleChassisPoweredOnDesc, prometheus.GaugeValue, poweredOn, chassis.ID)
}

// newExamplePowerServer serves one chassis that reports PowerState On, with the fake BMC and
// helpers every test in this package uses. A private build's tests use them the same way.
func newExamplePowerServer(t *testing.T) *testRedfishServer {
	t.Helper()
	server := newTestRedfishServer(t)
	server.addRoute("/redfish/v1/Chassis", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis",
		"@odata.type": "#ChassisCollection.ChassisCollection",
		"Members":     []map[string]any{{"@odata.id": "/redfish/v1/Chassis/1"}},
	})
	server.addRoute("/redfish/v1/Chassis/1", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis/1",
		"@odata.type": "#Chassis.v1_27_0.Chassis",
		"Id":          "1",
		"Name":        "1",
		"PowerState":  "On",
		"Status":      map[string]any{"Health": "OK", "State": "Enabled"},
	})
	return server
}

// requireExamplePowerCell checks for the series only the example cell emits.
func requireExamplePowerCell(t *testing.T, metrics map[string][]collectedMetric) {
	t.Helper()
	poweredOn := requireMetric(t, metrics, "redfish_example_chassis_powered_on")
	require.Equal(t, map[string]string{"chassis_id": "1"}, poweredOn.labels)
	require.InDelta(t, 1, poweredOn.value, 0.01)
}

// TestExamplePowerCell looks exampleSKUA up in the platform table by its key, runs the chassis
// collector as that row and finds the example cell's series, so the row is in the table and
// routed ChassisPower to the cell.
func TestExamplePowerCell(t *testing.T) {
	row, known := PlatformByName("EXAMPLE-SKU-A-EXAMPLE-FW-1")
	require.True(t, known, "the example rows are not in the platform table")
	requireExamplePowerCell(t, collectChassisWith(t, newExamplePowerServer(t), row))
}
