//go:build platform_examples

package collector

import (
	"context"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

var exampleSystemPoweredOnDesc = prometheus.NewDesc(
	"redfish_example_system_powered_on",
	"1 when the system reports PowerState On. Only the example collector emits it.",
	[]string{"system_id"},
	nil,
)

// exampleSystemCollector is a collector written for a row. The row puts it in place of a public
// collector through Platform.Collectors, keyed by prober; module and prober names in config.yml
// stay the same.
type exampleSystemCollector struct {
	redfishClient *gofish.APIClient
	logger        *slog.Logger
}

// newExampleSystemCollector has the CollectorConstructor signature, so a row can name it.
func newExampleSystemCollector(moduleName string, _ *config.Module, rfClient *gofish.APIClient, logger *slog.Logger) (ContextAwareCollector, error) {
	return &exampleSystemCollector{
		redfishClient: rfClient,
		logger:        logger.With(slog.String("collector", moduleName)),
	}, nil
}

// Describe implements prometheus.Collector.
func (e *exampleSystemCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- exampleSystemPoweredOnDesc
}

// Collect implements prometheus.Collector.
func (e *exampleSystemCollector) Collect(ch chan<- prometheus.Metric) {
	e.CollectWithContext(context.TODO(), ch)
}

// CollectWithContext makes one Redfish call, so it starts no goroutine. A failed read is logged
// and the systems that did arrive are still collected.
func (e *exampleSystemCollector) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) {
	if ctx.Err() != nil {
		return
	}
	systems, err := e.redfishClient.Service.Systems()
	if err != nil {
		e.logger.Error("error getting systems", slog.String("operation", "service.Systems()"), slog.Any("error", err))
	}
	for _, system := range systems {
		poweredOn := 0.0
		if system.PowerState == schemas.OnPowerState {
			poweredOn = 1
		}
		ch <- prometheus.MustNewConstMetric(exampleSystemPoweredOnDesc, prometheus.GaugeValue, poweredOn, system.ID)
	}
}

// buildExampleSystemCollector builds the system module's collector the way a scrape does, as
// the row given, against a fake BMC with one system that reports PowerState On.
func buildExampleSystemCollector(t *testing.T, row Platform) ContextAwareCollector {
	t.Helper()
	server := newTestRedfishServer(t)
	server.addRoute("/redfish/v1/Systems/System1", map[string]any{
		"@odata.id":   "/redfish/v1/Systems/System1",
		"@odata.type": "#ComputerSystem.v1_20_0.ComputerSystem",
		"Id":          "System1",
		"Name":        "System1",
		"PowerState":  "On",
	})
	client := connectToTestServer(t, server.Server)
	t.Cleanup(func() { client.Logout() })

	c, err := NewCollectorFromModule("system", &config.Module{Prober: "system_collector"}, client, NewTestLogger(t, slog.LevelDebug), row)
	require.NoError(t, err)
	return c
}

// TestExampleSystemCollector builds the system module's collector as Default, which gets the
// public collector, and as exampleSKUA, which gets the example one; the example one collects
// from the fake BMC.
func TestExampleSystemCollector(t *testing.T) {
	require.IsType(t, &SystemCollector{}, buildExampleSystemCollector(t, Default))

	example := buildExampleSystemCollector(t, exampleSKUA)
	require.IsType(t, &exampleSystemCollector{}, example)

	ch := make(chan prometheus.Metric, 16)
	example.CollectWithContext(context.Background(), ch)
	poweredOn := requireMetric(t, drainMetrics(t, ch), "redfish_example_system_powered_on")
	require.Equal(t, map[string]string{"system_id": "System1"}, poweredOn.labels)
	require.InDelta(t, 1, poweredOn.value, 0.01)
}
