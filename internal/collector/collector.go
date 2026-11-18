package collector

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish"
)

// ContextAwareCollector is mostly a prometheus.Collector, but cannot satisfy the Collector
// interface as it accepts an input context.Context.
// See https://github.com/prometheus/client_golang/issues/1538 for an upstream proposal
// that would be similar to what this interface means to achieve.
type ContextAwareCollector interface {
	// Collect satisfies traditional prometheus.Collector semantics; it uses a context.TODO as a parent
	Collect(ch chan<- prometheus.Metric)
	// CollectWithContext should result in the same collection output as Collect, but is context-aware so that
	// expensive collections may bail out
	CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric)
	// Describe satisfies traditional prometheus.Collector semantics
	Describe(ch chan<- *prometheus.Desc)
}

// NewCollectorFromModule creates and returns a prometheus.Collector from the Module, based on
// the Prober, and hands it the platform's cells when it routes any read by platform.
// Both redfish client and logger are common dependencies for any redfish_exporter collector,
// and must be provided as inputs.
func NewCollectorFromModule(moduleName string, m *config.Module, rfClient *gofish.APIClient, logger *slog.Logger, platform Platform) (ContextAwareCollector, error) {
	construct := newCollectorFromModule
	if override, ok := platform.Collectors[m.Prober]; ok {
		construct = override
	}
	c, err := construct(moduleName, m, rfClient, logger)
	if err != nil {
		return nil, err
	}
	if aware, ok := c.(PlatformAware); ok {
		aware.SetPlatform(platform)
	}
	return c, nil
}

func newCollectorFromModule(moduleName string, m *config.Module, rfClient *gofish.APIClient, logger *slog.Logger) (ContextAwareCollector, error) {
	switch m.Prober {
	case "chassis_collector":
		return NewChassisCollector(moduleName, rfClient, logger, m.ChassisCollector)
	case "gpu_collector":
		return NewGPUCollector(moduleName, rfClient, logger, m.GPUCollector)
	case "json_collector":
		return NewJSONCollector(moduleName, rfClient, logger, m.JSONCollector)
	case "manager_collector":
		return NewManagerCollector(moduleName, rfClient, logger, m.ManagerCollector)
	case "system_collector":
		return NewSystemCollector(moduleName, rfClient, logger, m.SystemCollector)
	case "telemetry_collector":
		return NewTelemetryCollector(moduleName, rfClient, logger, m.TelemetryCollector)
	case "powershelf_collector":
		return NewPowershelfCollector(moduleName, rfClient, logger, m.PowershelfCollector)
	default:
	}
	return nil, fmt.Errorf("prober type %s is not known to redfish_exporter", m.Prober)
}
