package collector

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish"
	"github.com/stretchr/testify/require"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

// TestPlatformTableComplete fails at test time, not at scrape time: a row that leaves a cell
// nil would otherwise panic inside a collector goroutine on the first host that selects it.
func TestPlatformTableComplete(t *testing.T) {
	require.Len(t, platforms, 1+len(extraPlatforms), "two rows share a key")
	for name, p := range platforms {
		require.Equal(t, strings.ToLower(p.Name), name, "table key must be the lowercased Platform.Name")
		v := reflect.ValueOf(p)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() != reflect.Func {
				continue // Collectors is optional: no entry means the default collector
			}
			require.False(t, v.Field(i).IsZero(), "platform %q leaves %s unset", name, v.Type().Field(i).Name)
		}
	}
}

func TestPlatformByName(t *testing.T) {
	p, known := PlatformByName(DefaultPlatformName)
	require.True(t, known)
	require.Equal(t, DefaultPlatformName, p.Name)

	p, known = PlatformByName("NOT-A-SKU-0.0.0")
	require.False(t, known)
	require.Equal(t, DefaultPlatformName, p.Name, "an unknown key runs as default")

	_, known = PlatformByName("DEFAULT")
	require.True(t, known, "keys are case-insensitive")

	require.Contains(t, PlatformNames(), DefaultPlatformName)
}

// TestGPUCollectorUsesPlatformCell pins the routing contract: the GPU collector reads NVLink
// ports through whatever function its platform names, and nothing else. A stub cell serves
// one port without touching the mock BMC, so no Ports request may appear in its log.
func TestGPUCollectorUsesPlatformCell(t *testing.T) {
	server, gpuPath := newNVLinkServer(t)
	client := connectToTestServer(t, server.Server)
	t.Cleanup(func() {
		client.Logout()
		server.Close()
	})

	collector, err := NewGPUCollector(t.Name(), client, NewTestLogger(t, slog.LevelDebug), config.DefaultGPUCollector)
	require.NoError(t, err)

	var stubCalls int
	collector.SetPlatform(Platform{
		Name: "stub",
		NVLink: func(_ *GPUCollector, _ context.Context, _ SystemGPU) (GPUNVLinkCollection, error) {
			stubCalls++
			var agg GPUNVLinkCollection
			err := json.Unmarshal([]byte(`{"Members":[{"Id":"NVLink_7","PortProtocol":"NVLink","PortType":"BidirectionalPort",
				"LinkStatus":"LinkUp","Status":{"State":"Enabled","Health":"OK"},"Metrics":{"Oem":{"Nvidia":{"LinkDownedCount":3}}}}]}`), &agg)
			return agg, err
		},
	})

	ch := make(chan prometheus.Metric, 256)
	collector.CollectWithContext(context.Background(), ch)
	metrics := drainMetrics(t, ch)

	require.Equal(t, 1, stubCalls)
	require.Equal(t, "NVLink_7", requireMetric(t, metrics, "redfish_gpu_nvlink_state").labels["port_id"])
	for _, request := range server.requests {
		require.NotContains(t, request, gpuPath+"/Ports", "the stub cell must be the only NVLink read")
	}
}

// TestNewCollectorFromModule_PlatformCollectors pins the whole-collector swap: a platform's
// Collectors entry for a prober replaces the collector the factory would build, with the
// module and prober names unchanged, and probers without an entry get the default.
func TestNewCollectorFromModule_PlatformCollectors(t *testing.T) {
	server, _ := newNVLinkServer(t)
	client := connectToTestServer(t, server.Server)
	t.Cleanup(func() {
		client.Logout()
		server.Close()
	})
	logger := NewTestLogger(t, slog.LevelDebug)
	gpuModule := &config.Module{Prober: "gpu_collector"}

	calls := 0
	swapped := Platform{
		Name:   "stub",
		NVLink: Default.NVLink,
		Collectors: map[string]CollectorConstructor{
			"gpu_collector": func(moduleName string, _ *config.Module, rfClient *gofish.APIClient, logger *slog.Logger) (ContextAwareCollector, error) {
				calls++
				// Any type will do; a chassis collector makes the swap unmistakable.
				return NewChassisCollector(moduleName, rfClient, logger, config.DefaultChassisCollector)
			},
		},
	}

	c, err := NewCollectorFromModule("gpu", gpuModule, client, logger, swapped)
	require.NoError(t, err)
	require.IsType(t, &ChassisCollector{}, c)
	require.Equal(t, 1, calls)

	c, err = NewCollectorFromModule("gpu", gpuModule, client, logger, Default)
	require.NoError(t, err)
	require.IsType(t, &GPUCollector{}, c, "the default platform builds the default collector")

	c, err = NewCollectorFromModule("system", &config.Module{Prober: "system_collector"}, client, logger, swapped)
	require.NoError(t, err)
	require.IsType(t, &SystemCollector{}, c, "a prober the platform does not replace gets the default collector")
	require.Equal(t, 1, calls)
}
