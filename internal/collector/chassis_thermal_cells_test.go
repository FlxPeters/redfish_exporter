package collector

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// newThermalCellsServer is a BMC with three chassis whose ThermalSubsystems link no
// LeakDetection; the guessed URL answers 404. HGX_GPU_SXM_1's ThermalMetrics repeats a
// temperature whose sensor HGX_GPU_0 owns.
func newThermalCellsServer(t *testing.T) *testRedfishServer {
	t.Helper()
	server := newTestRedfishServer(t)
	ids := []string{"1", "HGX_GPU_0", "HGX_GPU_SXM_1"}
	var members []map[string]any
	for _, id := range ids {
		chassisPath := "/redfish/v1/Chassis/" + id
		members = append(members, map[string]any{"@odata.id": chassisPath})
		server.addRoute(chassisPath, map[string]any{
			"@odata.id":        chassisPath,
			"@odata.type":      "#Chassis.v1_27_0.Chassis",
			"Id":               id,
			"Name":             id,
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
		owner := id
		if id == "HGX_GPU_SXM_1" {
			owner = "HGX_GPU_0"
		}
		server.addRoute(chassisPath+"/ThermalSubsystem/ThermalMetrics", map[string]any{
			"@odata.id":   chassisPath + "/ThermalSubsystem/ThermalMetrics",
			"@odata.type": "#ThermalMetrics.v1_3_0.ThermalMetrics",
			"Id":          "ThermalMetrics",
			"TemperatureReadingsCelsius": []map[string]any{{
				"DataSourceUri": "/redfish/v1/Chassis/" + owner + "/Sensors/" + owner + "_TEMP_0",
				"Reading":       40,
			}},
		})
		server.addErrorRoute(chassisPath+"/ThermalSubsystem/LeakDetection", http.StatusNotFound)
	}
	server.addRoute("/redfish/v1/Chassis", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis",
		"@odata.type": "#ChassisCollection.ChassisCollection",
		"Members":     members,
	})
	return server
}

// TestGetLeakDetectors_DefaultGuesses pins the default row: it requests the guessed
// LeakDetection URL that some leak-detecting BMCs need.
func TestGetLeakDetectors_DefaultGuesses(t *testing.T) {
	server := newThermalCellsServer(t)
	collectChassisWith(t, server, Default)
	require.Contains(t, server.requests, "GET /redfish/v1/Chassis/HGX_GPU_SXM_1/ThermalSubsystem/LeakDetection")
}
