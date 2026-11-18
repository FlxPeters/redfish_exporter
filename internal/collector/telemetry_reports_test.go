package collector

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

// TestTelemetryCollector_FetchesOnlyParsedReports pins that a metric report the collector has
// no parser for is never requested, while a parsed one still is and still yields metrics.
func TestTelemetryCollector_FetchesOnlyParsedReports(t *testing.T) {
	const reports = "/redfish/v1/TelemetryService/MetricReports"
	routes := map[string]string{
		"/redfish/v1/":                                `{"@odata.id":"/redfish/v1/","@odata.type":"#ServiceRoot.v1_15_0.ServiceRoot","Id":"RootService","Systems":{"@odata.id":"/redfish/v1/Systems"},"TelemetryService":{"@odata.id":"/redfish/v1/TelemetryService"}}`,
		"/redfish/v1/Systems":                         `{"@odata.type":"#ComputerSystemCollection.ComputerSystemCollection","Members":[{"@odata.id":"/redfish/v1/Systems/HGX_Baseboard_0"}]}`,
		"/redfish/v1/Systems/HGX_Baseboard_0":         `{"@odata.id":"/redfish/v1/Systems/HGX_Baseboard_0","@odata.type":"#ComputerSystem.v1_20_0.ComputerSystem","Id":"HGX_Baseboard_0","Name":"HGX_Baseboard_0"}`,
		"/redfish/v1/TelemetryService":                `{"@odata.id":"/redfish/v1/TelemetryService","@odata.type":"#TelemetryService.v1_3_0.TelemetryService","Id":"TelemetryService","MetricReports":{"@odata.id":"` + reports + `"}}`,
		reports:                                       `{"@odata.type":"#MetricReportCollection.MetricReportCollection","Members":[{"@odata.id":"` + reports + `/AvgPowerConsumptionHour"}],"Members@odata.nextLink":"` + reports + `?$skip=1"}`,
		reports + "?$skip=1":                          `{"@odata.type":"#MetricReportCollection.MetricReportCollection","Members":[{"@odata.id":"` + reports + `/HGX_PlatformEnvironmentMetrics_0"}]}`,
		reports + "/HGX_PlatformEnvironmentMetrics_0": `{"@odata.id":"` + reports + `/HGX_PlatformEnvironmentMetrics_0","@odata.type":"#MetricReport.v1_4_2.MetricReport","Id":"HGX_PlatformEnvironmentMetrics_0","MetricValues":[{"MetricProperty":"/redfish/v1/Chassis/HGX_GPU_0/Sensors/HGX_GPU_0_DRAM_0_Temp_0","MetricValue":"36","Oem":{"Nvidia":{"MetricValueStale":false}}}]}`,
		reports + "/AvgPowerConsumptionHour":          `{"@odata.id":"` + reports + `/AvgPowerConsumptionHour","@odata.type":"#MetricReport.v1_4_2.MetricReport","Id":"AvgPowerConsumptionHour","MetricValues":[]}`,
	}
	var mu sync.Mutex
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requested = append(requested, r.URL.Path)
		mu.Unlock()
		body, ok := routes[r.URL.RequestURI()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	client := connectToTestServer(t, server)
	t.Cleanup(func() { client.Logout() })

	collector, err := NewTelemetryCollector(t.Name(), client, NewTestLogger(t, slog.LevelDebug), config.TelemetryCollectorConfig{})
	require.NoError(t, err)
	ch := make(chan prometheus.Metric, 64)
	collector.CollectWithContext(context.Background(), ch)
	close(ch)
	var telemetry int
	for m := range ch {
		if strings.Contains(m.Desc().String(), `fqName: "redfish_telemetry_`) {
			telemetry++
		}
	}

	require.Positive(t, telemetry, "the parsed report still yields metrics")
	require.Contains(t, requested, reports+"/HGX_PlatformEnvironmentMetrics_0", "a report on the collection's second page is fetched")
	require.NotContains(t, requested, reports+"/AvgPowerConsumptionHour", "a report with no parser is never fetched")
	require.Equal(t, float64(1), testutil.ToFloat64(collector.collectorScrapeStatus.WithLabelValues("telemetry")), "every parsed report was fetched")
}
