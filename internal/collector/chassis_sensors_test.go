package collector

import (
	"context"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

// newSensorsServer is a BMC with no Thermal or Power resource: the host chassis reports its
// fans, temperatures and voltages only as Sensors, and a component chassis links Sensors too.
func newSensorsServer(t *testing.T) *testRedfishServer {
	t.Helper()
	server := newTestRedfishServer(t)
	server.addRoute("/redfish/v1/Chassis", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis",
		"@odata.type": "#ChassisCollection.ChassisCollection",
		"Members": []map[string]any{
			{"@odata.id": "/redfish/v1/Chassis/1"},
			{"@odata.id": "/redfish/v1/Chassis/HGX_GPU_0"},
		},
	})
	server.addRoute("/redfish/v1/Chassis/1", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis/1",
		"@odata.type": "#Chassis.v1_27_0.Chassis",
		"Id":          "1",
		"Name":        "Computer System Chassis",
		"ChassisType": "RackMount",
		"Status":      map[string]any{"Health": "OK", "State": "Enabled"},
		"Sensors":     map[string]any{"@odata.id": "/redfish/v1/Chassis/1/Sensors"},
	})
	server.addRoute("/redfish/v1/Chassis/HGX_GPU_0", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis/HGX_GPU_0",
		"@odata.type": "#Chassis.v1_27_0.Chassis",
		"Id":          "HGX_GPU_0",
		"Name":        "HGX_GPU_0",
		"ChassisType": "Component",
		"Status":      map[string]any{"Health": "OK", "State": "Enabled"},
		"Sensors":     map[string]any{"@odata.id": "/redfish/v1/Chassis/HGX_GPU_0/Sensors"},
	})
	server.addRoute("/redfish/v1/Chassis/1/Sensors", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis/1/Sensors",
		"@odata.type": "#SensorCollection.SensorCollection",
		"Members": []map[string]any{
			{
				"@odata.id": "/redfish/v1/Chassis/1/Sensors/CPU1Temp", "@odata.type": "#Sensor.v1_11_0.Sensor",
				"Id": "CPU1Temp", "Name": "CPU1 Temp", "ReadingType": "Temperature", "ReadingUnits": "Cel", "Reading": 41,
				"Status": map[string]any{"State": "Enabled", "Health": "OK"},
			},
			{
				"@odata.id": "/redfish/v1/Chassis/1/Sensors/MBFan1-4RPM", "@odata.type": "#Sensor.v1_11_0.Sensor",
				"Id": "MBFan1-4RPM", "Name": "MB Fan 1-4 RPM", "ReadingType": "Rotational", "ReadingUnits": "RPM", "Reading": 7980,
				"Thresholds": map[string]any{"LowerCritical": map[string]any{"Reading": 420, "Activation": "Decreasing"}},
				"Status":     map[string]any{"State": "Enabled", "Health": "OK"},
			},
			{
				"@odata.id": "/redfish/v1/Chassis/1/Sensors/CPU1_VCCIN", "@odata.type": "#Sensor.v1_11_0.Sensor",
				"Id": "CPU1_VCCIN", "Name": "CPU1_VCCIN", "ReadingType": "Voltage", "ReadingUnits": "V", "Reading": 1.81,
				"Status": map[string]any{"State": "Enabled", "Health": "OK"},
			},
			// A discrete sensor and a power sensor: neither is a legacy Thermal or Power family.
			{
				"@odata.id": "/redfish/v1/Chassis/1/Sensors/PS1Status", "@odata.type": "#Sensor.v1_11_0.Sensor",
				"Id": "PS1Status", "Name": "PS1 Status", "Reading": 1,
				"Status": map[string]any{"State": "Enabled", "Health": "OK"},
			},
			{
				"@odata.id": "/redfish/v1/Chassis/1/Sensors/PWConsumption", "@odata.type": "#Sensor.v1_11_0.Sensor",
				"Id": "PWConsumption", "Name": "PW Consumption", "ReadingType": "Power", "ReadingUnits": "W", "Reading": 3811,
				"Status": map[string]any{"State": "Enabled", "Health": "OK"},
			},
		},
	})
	server.addRoute("/redfish/v1/Chassis/HGX_GPU_0/Sensors", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis/HGX_GPU_0/Sensors",
		"@odata.type": "#SensorCollection.SensorCollection",
		"Members":     []map[string]any{},
	})
	return server
}

func collectChassisWith(t *testing.T, server *testRedfishServer, row Platform) map[string][]collectedMetric {
	t.Helper()
	client := connectToTestServer(t, server.Server)
	t.Cleanup(func() { client.Logout() })
	collector, err := NewChassisCollector(t.Name(), client, NewTestLogger(t, slog.LevelDebug), config.DefaultChassisCollector)
	require.NoError(t, err)
	collector.SetPlatform(row)
	ch := make(chan prometheus.Metric, 256)
	collector.CollectWithContext(context.Background(), ch)
	return drainMetrics(t, ch)
}

// TestCollectSensors_DefaultReadsNone pins that the default row never reads Sensors.
func TestCollectSensors_DefaultReadsNone(t *testing.T) {
	server := newSensorsServer(t)
	metrics := collectChassisWith(t, server, Default)
	require.NotContains(t, metrics, "redfish_chassis_temperature_celsius")
	require.NotContains(t, metrics, "redfish_chassis_fan_rpm")
	require.NotContains(t, metrics, "redfish_chassis_power_voltage_volts")
	for _, request := range server.requests {
		require.NotContains(t, request, "/Sensors")
	}
}
