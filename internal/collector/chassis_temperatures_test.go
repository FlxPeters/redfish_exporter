package collector

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// thermalChassis is one chassis of a fake BMC and the sensors its ThermalMetrics lists, each as
// the chassis that owns the sensor and the sensor's name.
type thermalChassis struct {
	id      string
	sensors [][2]string
}

// repeatingThermalChassis lists sensors the way the lab BMC does: the baseboard zone lists component
// chassis' sensors, HGX_GPU_SXM_1 lists HGX_GPU_0's and its own, and HGX_ConnectX_0 its own.
var repeatingThermalChassis = []thermalChassis{
	{"1", [][2]string{{"1", "CPU1Temp"}}},
	{"HGX_Chassis_0", [][2]string{{"HGX_ConnectX_0", "HGX_ConnectX_0_TEMP_0"}, {"HGX_GPU_SXM_1", "HGX_GPU_SXM_1_TEMP_0"}}},
	{"HGX_GPU_0", [][2]string{{"HGX_GPU_0", "HGX_GPU_0_TEMP_0"}}},
	{"HGX_GPU_SXM_1", [][2]string{{"HGX_GPU_0", "HGX_GPU_0_TEMP_0"}, {"HGX_GPU_SXM_1", "HGX_GPU_SXM_1_TEMP_0"}}},
	{"HGX_ConnectX_0", [][2]string{{"HGX_ConnectX_0", "HGX_ConnectX_0_TEMP_0"}}},
}

// newTemperaturesServer serves the chassis given, each with a ThermalSubsystem whose
// ThermalMetrics lists their sensors. A sensor reads the same wherever it is listed.
func newTemperaturesServer(t *testing.T, chassis []thermalChassis) *testRedfishServer {
	t.Helper()
	server := newTestRedfishServer(t)
	var members []map[string]any
	for _, c := range chassis {
		chassisPath := "/redfish/v1/Chassis/" + c.id
		members = append(members, map[string]any{"@odata.id": chassisPath})
		server.addRoute(chassisPath, map[string]any{
			"@odata.id":        chassisPath,
			"@odata.type":      "#Chassis.v1_27_0.Chassis",
			"Id":               c.id,
			"Name":             c.id,
			"Status":           map[string]any{"Health": "OK", "State": "Enabled"},
			"ThermalSubsystem": map[string]any{"@odata.id": chassisPath + "/ThermalSubsystem"},
		})
		server.addRoute(chassisPath+"/ThermalSubsystem", map[string]any{
			"@odata.id":      chassisPath + "/ThermalSubsystem",
			"@odata.type":    "#ThermalSubsystem.v1_3_0.ThermalSubsystem",
			"Id":             "ThermalSubsystem",
			"Status":         map[string]any{"Health": "OK", "HealthRollup": "OK", "State": "Enabled"},
			"ThermalMetrics": map[string]any{"@odata.id": chassisPath + "/ThermalSubsystem/ThermalMetrics"},
		})
		var readings []map[string]any
		for _, sensor := range c.sensors {
			readings = append(readings, map[string]any{
				"DataSourceUri": "/redfish/v1/Chassis/" + sensor[0] + "/Sensors/" + sensor[1],
				"DeviceName":    sensor[1],
				"Reading":       float64(len(sensor[1])),
			})
		}
		server.addRoute(chassisPath+"/ThermalSubsystem/ThermalMetrics", map[string]any{
			"@odata.id":                  chassisPath + "/ThermalSubsystem/ThermalMetrics",
			"@odata.type":                "#ThermalMetrics.v1_0_0.ThermalMetrics",
			"Id":                         "ThermalMetrics",
			"TemperatureReadingsCelsius": readings,
		})
	}
	server.addRoute("/redfish/v1/Chassis", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis",
		"@odata.type": "#ChassisCollection.ChassisCollection",
		"Members":     members,
	})
	return server
}

// TestCollectThermalMetrics_DefaultReadsAll pins the default row: every chassis' ThermalMetrics.
func TestCollectThermalMetrics_DefaultReadsAll(t *testing.T) {
	server := newTemperaturesServer(t, repeatingThermalChassis)
	collectChassisWith(t, server, Default)
	for _, c := range repeatingThermalChassis {
		require.Contains(t, server.requests, "GET /redfish/v1/Chassis/"+c.id+"/ThermalSubsystem/ThermalMetrics")
	}
}

// TestChassisCollectorWriteOnce pins the per-scrape record a cell uses to write a series once.
func TestChassisCollectorWriteOnce(t *testing.T) {
	var c ChassisCollector
	require.True(t, c.writeOnce("HGX_ConnectX_0 /redfish/v1/Chassis/HGX_ConnectX_0/Sensors/HGX_ConnectX_0_TEMP_0"))
	require.False(t, c.writeOnce("HGX_ConnectX_0 /redfish/v1/Chassis/HGX_ConnectX_0/Sensors/HGX_ConnectX_0_TEMP_0"), "already written")
	require.True(t, c.writeOnce("HGX_Chassis_0 /redfish/v1/Chassis/HGX_ConnectX_0/Sensors/HGX_ConnectX_0_TEMP_0"))
}
