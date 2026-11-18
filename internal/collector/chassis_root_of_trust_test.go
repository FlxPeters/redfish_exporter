package collector

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// newRootOfTrustServer is a BMC whose chassis collection lists two root-of-trust chassis beside
// the host and a GPU.
func newRootOfTrustServer(t *testing.T) *testRedfishServer {
	t.Helper()
	server := newTestRedfishServer(t)
	var members []map[string]any
	for _, id := range []string{"1", "HGX_GPU_0", "HGX_IRoT_GPU_0", "HGX_ERoT_BMC_0"} {
		chassisPath := "/redfish/v1/Chassis/" + id
		members = append(members, map[string]any{"@odata.id": chassisPath})
		server.addRoute(chassisPath, map[string]any{
			"@odata.id":   chassisPath,
			"@odata.type": "#Chassis.v1_27_0.Chassis",
			"Id":          id,
			"Name":        id,
			"Status":      map[string]any{"Health": "OK", "State": "Enabled"},
		})
	}
	server.addRoute("/redfish/v1/Chassis", map[string]any{
		"@odata.id":   "/redfish/v1/Chassis",
		"@odata.type": "#ChassisCollection.ChassisCollection",
		"Members":     members,
	})
	return server
}

// TestListChassis_DefaultReadsAll pins the default row: every chassis the service lists.
func TestListChassis_DefaultReadsAll(t *testing.T) {
	server := newRootOfTrustServer(t)
	require.Len(t, collectChassisWith(t, server, Default)["redfish_chassis_health"], 4)
}
