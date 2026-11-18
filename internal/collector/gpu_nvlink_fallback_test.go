package collector

import (
	"net/http"
	"testing"
)

// newNVLinkServer is a BMC whose Ports collection refuses every query string with
// 404, and whose processor expands with its ports and their Metrics inline. The mock mux
// ignores query strings, so the processor route answers both the plain read and the expanded
// one.
func newNVLinkServer(t *testing.T) (*testRedfishServer, string) {
	t.Helper()
	server := newTestRedfishServer(t)
	systemPath := "/redfish/v1/Systems/System1"
	gpuPath := systemPath + "/Processors/GPU_0"
	server.addRoute(systemPath, map[string]any{
		"@odata.id":   systemPath,
		"@odata.type": "#ComputerSystem.v1_20_0.ComputerSystem",
		"Id":          "System1",
		"Name":        "HGX_Baseboard_0",
		"Status":      map[string]any{"State": "Enabled", "Health": "OK"},
		"Processors":  map[string]any{"@odata.id": systemPath + "/Processors"},
	})
	server.addRoute(systemPath+"/Processors", map[string]any{
		"@odata.type": "#ProcessorCollection.ProcessorCollection",
		"Members":     []map[string]any{{"@odata.id": gpuPath}},
	})
	server.addRoute(gpuPath, map[string]any{
		"@odata.id":     gpuPath,
		"@odata.type":   "#Processor.v1_20_0.Processor",
		"Id":            "GPU_0",
		"Name":          "GPU_0",
		"ProcessorType": "GPU",
		"Status":        map[string]any{"State": "Enabled", "Health": "OK"},
		"Metrics":       map[string]any{"@odata.id": gpuPath + "/ProcessorMetrics"},
		"Ports": map[string]any{
			"@odata.id":   gpuPath + "/Ports",
			"@odata.type": "#PortCollection.PortCollection",
			"Members": []map[string]any{
				{
					"@odata.id":    gpuPath + "/Ports/NVLink_0",
					"Id":           "NVLink_0",
					"PortProtocol": "NVLink",
					"PortType":     "BidirectionalPort",
					"LinkStatus":   "LinkUp",
					"Status":       map[string]any{"State": "Enabled", "Health": "OK"},
					"Metrics": map[string]any{
						"@odata.id": gpuPath + "/Ports/NVLink_0/Metrics",
						"Oem": map[string]any{
							"Nvidia": map[string]any{
								"BitErrorRate":           1e-12,
								"LinkDownedCount":        1,
								"LinkErrorRecoveryCount": 2,
								"SymbolErrors":           3,
								"NVLinkErrors":           map[string]any{"RuntimeError": false, "TrainingError": true},
							},
						},
					},
				},
				{
					"@odata.id":    gpuPath + "/Ports/PCIe_0",
					"Id":           "PCIe_0",
					"PortProtocol": "PCIe",
					"PortType":     "BidirectionalPort",
					"LinkStatus":   "LinkUp",
					"Status":       map[string]any{"State": "Enabled", "Health": "OK"},
				},
			},
		},
	})
	// No context utilization duration, so every emitted sample is a gauge for drainMetrics.
	server.addRoute(gpuPath+"/ProcessorMetrics", map[string]any{
		"@odata.id":   gpuPath + "/ProcessorMetrics",
		"@odata.type": "#ProcessorMetrics.v1_6_0.ProcessorMetrics",
		"Id":          "ProcessorMetrics",
		"Oem":         map[string]any{"Nvidia": map[string]any{"SRAMECCErrorThresholdExceeded": false}},
	})
	server.addErrorRoute(gpuPath+"/Ports", http.StatusNotFound)
	return server, gpuPath
}
