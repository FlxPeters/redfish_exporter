package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	isoDuration "github.com/sosodev/duration"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

// GPUSubsystem is the GPU subsystem name
const GPUSubsystem = "gpu"

var (
	// gpuBaseLabels are labels expected on all series emitted by this collector
	gpuBaseLabels = []string{"system_id", "gpu_id"}
	// gpuMemoryLabels appends memory_id to gpuBaseLabels, as an expected label for memory-related series
	gpuMemoryLabels = baseWithExtraLabels([]string{"memory_id"})
	// gpuPortLabels appends NVLink labels gpuBaseLabels for NVLink-related series
	gpuPortLabels = baseWithExtraLabels([]string{"port_id", "port_type", "port_protocol"})
	// gpuInfoLabels appends a S/N and UUID to gpuBaseLabels for the redfish_gpu_info series
	gpuInfoLabels = baseWithExtraLabels([]string{"firmware_version", "serial_number", "uuid"})
	gpuMetrics    = createGPUMetricMap()
)

func baseWithExtraLabels(extra []string) []string {
	gpuBaseLabelsCopy := make([]string, len(gpuBaseLabels))
	copy(gpuBaseLabelsCopy, gpuBaseLabels)
	return append(gpuBaseLabelsCopy, extra...)
}

// SystemGPU is a type embedding [*schemas.Processor], with support for
// extra fields related to the owning system: System Name and System ID
type SystemGPU struct {
	*schemas.Processor
	SystemName string
	SystemID   string
}

// GPUCollector is responsible for collecting Nvidia GPU telemetry
type GPUCollector struct {
	redfishClient         *gofish.APIClient
	config                config.GPUCollectorConfig
	metrics               map[string]Metric
	logger                *slog.Logger
	collectorScrapeStatus *prometheus.GaugeVec
	oemClient             *NvidiaOEMClient
	// nvlink is the platform's NVLink read; see Platform.
	nvlink func(*GPUCollector, context.Context, SystemGPU) (GPUNVLinkCollection, error)
}

func createGPUMetricMap() map[string]Metric {
	gpuMetrics := make(map[string]Metric)

	// Basic GPU metrics from main branch
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_ecc_correctable", "current correctable memory ecc errors reported on the gpu", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_ecc_uncorrectable", "current uncorrectable memory ecc errors reported on the gpu", gpuMemoryLabels)

	// GPU info metric
	addToMetricMap(gpuMetrics, GPUSubsystem, "info", "GPU information with serial number and UUID", gpuInfoLabels)

	// GPU Memory metrics
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_capacity_mib", "GPU memory capacity in MiB", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_state", fmt.Sprintf("GPU memory state,%s", CommonStateHelp), gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_health", fmt.Sprintf("GPU memory health,%s", CommonHealthHelp), gpuMemoryLabels)

	// Nvidia GPU Memory OEM metrics
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_row_remapping_failed", "GPU memory row remapping failed status (1 if failed)", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_row_remapping_pending", "GPU memory row remapping pending status (1 if pending)", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_correctable_row_remapping_count", "GPU memory correctable row remapping count", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_uncorrectable_row_remapping_count", "GPU memory uncorrectable row remapping count", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_high_availability_bank_count", "GPU memory high availability bank count", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_low_availability_bank_count", "GPU memory low availability bank count", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_no_availability_bank_count", "GPU memory no availability bank count", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_partial_availability_bank_count", "GPU memory partial availability bank count", gpuMemoryLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "memory_max_availability_bank_count", "GPU memory max availability bank count", gpuMemoryLabels)

	// GPU Processor metrics
	addToMetricMap(gpuMetrics, GPUSubsystem, "state", fmt.Sprintf("GPU processor state,%s", CommonStateHelp), gpuBaseLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "health", fmt.Sprintf("GPU processor health,%s", CommonHealthHelp), gpuBaseLabels)

	// Nvidia GPU Processor OEM metrics
	// Note: SM utilization, activity, occupancy, tensor/FP activity, and PCIe bandwidth metrics
	// are now collected via TelemetryService (HGX_ProcessorGPMMetrics_0) for better performance
	addToMetricMap(gpuMetrics, GPUSubsystem, "sram_ecc_error_threshold_exceeded", "GPU SRAM ECC error threshold exceeded (1 if exceeded)", gpuBaseLabels)

	// NVLink Port metrics
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_state", fmt.Sprintf("NVLink port state,%s", CommonStateHelp), gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_health", fmt.Sprintf("NVLink port health,%s", CommonHealthHelp), gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_link_status", fmt.Sprintf("NVLink port link status,%s", CommonNVLinkPortLinkHelp), gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_runtime_error", "NVLink runtime error status (1 if error)", gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_training_error", "NVLink training error status (1 if error)", gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_link_error_recovery_count", "NVLink error recovery count", gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_link_downed_count", "NVLink link downed count", gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_symbol_errors", "NVLink symbol error count", gpuPortLabels)
	addToMetricMap(gpuMetrics, GPUSubsystem, "nvlink_bit_error_rate", "NVLink bit error rate", gpuPortLabels)

	// GPU Context Utilization metrics
	// Note: GPU temperature and memory power metrics are now collected via TelemetryService (HGX_PlatformEnvironmentMetrics_0)
	addToMetricMap(gpuMetrics, GPUSubsystem, "context_utilization_seconds_total", "Accumulated GPU context utilization duration in seconds", gpuBaseLabels)

	return gpuMetrics
}

// NewGPUCollector creates a new GPU collector
func NewGPUCollector(collectorName string, redfishClient *gofish.APIClient, logger *slog.Logger, config config.GPUCollectorConfig) (*GPUCollector, error) {
	return &GPUCollector{
		redfishClient: redfishClient,
		metrics:       gpuMetrics,
		config:        config,
		logger:        logger.With(slog.String("collector", "GPUCollector")),
		collectorScrapeStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "collector_scrape_status",
				Help:      "collector_scrape_status",
			},
			[]string{"collector"},
		),
		oemClient: NewNvidiaOEMClient(redfishClient.GetService().GetClient(), logger),
		nvlink:    Default.NVLink,
	}, nil
}

// SetPlatform takes the cells this collector routes on. It keeps the functions, not the
// Platform, so nothing in the collector can branch on a platform's name.
func (g *GPUCollector) SetPlatform(p Platform) {
	g.nvlink = p.NVLink
}

func (g *GPUCollector) whileContextAllows(ctx context.Context, fns ...func()) {
	for _, fn := range fns {
		if ctx.Err() != nil {
			g.logger.With("error", ctx.Err()).Warn("unable to run function as context is done")
			return
		}
		fn()
	}
}

// Describe implements prometheus.Collector
func (g *GPUCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range g.metrics {
		ch <- metric.desc
	}
	g.collectorScrapeStatus.Describe(ch)
}

// CollectWithContext operates much like Collect, but propagates the provided [context.Context] down
func (g *GPUCollector) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) {
	g.collect(ctx, ch)
}

// Collect implements prometheus.Collector. It uses a [context.TODO], and care should be
// taken such that concurrent scrape requests do not pile up.
func (g *GPUCollector) Collect(ch chan<- prometheus.Metric) {
	g.collect(context.TODO(), ch)
}

// collect supports context-aware metrics collection for a GPU Collector.
// Context done-ness is checked immediately as well as at the start of per-GPU collection.
// GPU collection encompasses the following areas:
// 1) Health/state of the GPU itself
// 2) Memory metrics (including health/state)
// 3) Nvidia OEM
func (g *GPUCollector) collect(ctx context.Context, ch chan<- prometheus.Metric) {
	if ctx.Err() != nil {
		g.logger.With("error", ctx.Err().Error()).Debug("skipping gpu collection")
		return
	}
	g.collectorScrapeStatus.WithLabelValues("gpu").Set(float64(0))

	gpus, err := g.gatherGPUs(ctx)
	if err != nil {
		g.logger.With("error", err, "operation", "gatherGPUs").Error("unable to gather gpus")
		return
	}

	for _, gpu := range gpus {
		if ctx.Err() != nil {
			g.logger.With("error", ctx.Err().Error()).Debug("skipping further gpu collection")
			return
		}
		procBaseLabels := []string{gpu.SystemName, gpu.ID}
		g.whileContextAllows(ctx,
			func() { g.emitGPUMemoryMetrics(ch, gpu, []string{gpu.SystemID, gpu.ID}) },
			func() { g.emitHealthInfo(ch, gpu, procBaseLabels) },
			func() { g.emitGPUOem(ch, gpu, procBaseLabels) },
			func() { g.emitNVLinkTelemetry(ctx, ch, gpu) },
		)
	}

	g.collectorScrapeStatus.WithLabelValues("gpu").Set(float64(1))
}

// gatherGPUs traverses all Redfish Systems, looking for Processors
// which are reportedly GPUs.
// To ease complexity on actual processing logic, gatherGPUs
// returns a [SystemGPU] containing the GPU and system name and ID.
func (g *GPUCollector) gatherGPUs(ctx context.Context) ([]SystemGPU, error) {
	var ret []SystemGPU
	if ctx.Err() != nil {
		return ret, ctx.Err()
	}
	systems, err := g.redfishClient.Service.Systems()
	if err != nil {
		return ret, fmt.Errorf("unable to obtain systems data: %w", err)
	}

	for _, sys := range systems {
		if strings.Contains(sys.Name, "HGX_") {
			procs, err := sys.Processors()
			if err != nil {
				g.logger.With("error", err, "system", sys.ODataID).Debug("unable to obtain system processors")
				continue
			}
			for _, gpu := range filterGPUs(procs) {
				ret = append(ret, SystemGPU{
					SystemName: sys.Name,
					SystemID:   sys.ID,
					Processor:  gpu,
				})
			}
		}
	}

	return ret, nil
}

// filterGPUs filters processors to return only GPU processors
func filterGPUs(cpus []*schemas.Processor) []*schemas.Processor {
	gpus := []*schemas.Processor{}
	for _, cpu := range cpus {
		if cpu.ProcessorType == schemas.GPUProcessorType {
			gpus = append(gpus, cpu)
		}
	}
	return gpus
}

// emitGPUMemoryMetrics iterates a slice of [*schemas.Memory] belonging to the provided [SystemGPU],
// performing a network request to the Redfish device to gather memory metrics.
// Collected metrics are emitted onto the provided channel.
func (g *GPUCollector) emitGPUMemoryMetrics(ch chan<- prometheus.Metric, gpu SystemGPU, commonLabels []string) {
	gpuMems, err := gpu.Memory()
	if err != nil {
		g.logger.With("error", err, "gpu_id", gpu.ID, "system_name", gpu.SystemName).Error("failed obtaining gpu memory, skipping")
		return
	}
	for _, mem := range gpuMems {
		memMetric, err := mem.Metrics()
		if err != nil {
			g.logger.With("error", err, "gpu_id", gpu.ID, "memory_id", mem.ID, "system_name", gpu.SystemName).Error("failed obtaining gpu memory metrics, skipping")
			continue
		}
		memLabels := make([]string, len(commonLabels))
		copy(memLabels, commonLabels)
		memLabels = append(memLabels, mem.ID)

		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_ecc_correctable"].desc,
			prometheus.CounterValue,
			float64(gofish.Deref(memMetric.LifeTime.CorrectableECCErrorCount)),
			memLabels...)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_ecc_uncorrectable"].desc,
			prometheus.CounterValue,
			float64(gofish.Deref(memMetric.LifeTime.UncorrectableECCErrorCount)),
			memLabels...)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_capacity_mib"].desc,
			prometheus.GaugeValue,
			float64(gofish.Deref(mem.CapacityMiB)),
			[]string{gpu.SystemName, gpu.ID, mem.ID}...,
		)
		if stateValue, ok := parseCommonStatusState(mem.Status.State); ok {
			ch <- prometheus.MustNewConstMetric(
				g.metrics["gpu_memory_state"].desc,
				prometheus.GaugeValue,
				stateValue,
				memLabels...,
			)
		}
		if healthValue, ok := parseCommonStatusHealth(mem.Status.Health); ok {
			ch <- prometheus.MustNewConstMetric(
				g.metrics["gpu_memory_health"].desc,
				prometheus.GaugeValue,
				healthValue,
				memLabels...,
			)
		}

		// Get Memory OEM metrics
		if memOEM, err := g.oemClient.GetMemoryOEMMetrics(mem.ODataID); err == nil {
			ch <- prometheus.MustNewConstMetric(
				g.metrics["gpu_memory_row_remapping_failed"].desc,
				prometheus.GaugeValue,
				boolToFloat64(memOEM.RowRemappingFailed),
				memLabels...,
			)
			ch <- prometheus.MustNewConstMetric(
				g.metrics["gpu_memory_row_remapping_pending"].desc,
				prometheus.GaugeValue,
				boolToFloat64(memOEM.RowRemappingPending),
				memLabels...,
			)
		} else {
			g.logger.Error("failed to get Memory OEM metrics",
				slog.String("memory_id", mem.ID),
				slog.Any("error", err),
			)
		}

		var oemMem MemoryMetricsResponse
		if err := json.Unmarshal(memMetric.OEM, &oemMem); err != nil {
			g.logger.With("error", err).Debug("unable to unmarshal OEM memory")
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_correctable_row_remapping_count"].desc,
			prometheus.GaugeValue,
			float64(oemMem.Nvidia.RowRemapping.CorrectableRowRemappingCount),
			memLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_uncorrectable_row_remapping_count"].desc,
			prometheus.GaugeValue,
			float64(oemMem.Nvidia.RowRemapping.UncorrectableRowRemappingCount),
			memLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_high_availability_bank_count"].desc,
			prometheus.GaugeValue,
			float64(oemMem.Nvidia.RowRemapping.HighAvailabilityBankCount),
			memLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_low_availability_bank_count"].desc,
			prometheus.GaugeValue,
			float64(oemMem.Nvidia.RowRemapping.LowAvailabilityBankCount),
			memLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_no_availability_bank_count"].desc,
			prometheus.GaugeValue,
			float64(oemMem.Nvidia.RowRemapping.NoAvailabilityBankCount),
			memLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_partial_availability_bank_count"].desc,
			prometheus.GaugeValue,
			float64(oemMem.Nvidia.RowRemapping.PartialAvailabilityBankCount),
			memLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_memory_max_availability_bank_count"].desc,
			prometheus.GaugeValue,
			float64(oemMem.Nvidia.RowRemapping.MaxAvailabilityBankCount),
			memLabels...,
		)
	}
}

func (g *GPUCollector) emitHealthInfo(ch chan<- prometheus.Metric, gpu SystemGPU, procBaseLabels []string) {
	if stateValue, ok := parseCommonStatusState(gpu.Status.State); ok {
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_state"].desc,
			prometheus.GaugeValue,
			stateValue,
			procBaseLabels...,
		)
	}
	if healthValue, ok := parseCommonStatusHealth(gpu.Status.Health); ok {
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_health"].desc,
			prometheus.GaugeValue,
			healthValue,
			procBaseLabels...,
		)
	}
	var gpuFirmware, gpuSerial, gpuUUID string
	if gpuSerial = gpu.SerialNumber; gpuSerial == "" {
		gpuSerial = "unknown"
	}
	if gpuUUID = gpu.UUID; gpuUUID == "" {
		gpuUUID = "unknown"
	}
	if gpuFirmware = gpu.FirmwareVersion; gpuFirmware == "" {
		gpuFirmware = "unknown"
	}

	infoLabels := []string{gpu.SystemName, gpu.ID, gpuFirmware, gpuSerial, gpuUUID}
	ch <- prometheus.MustNewConstMetric(
		g.metrics["gpu_info"].desc,
		prometheus.GaugeValue,
		1,
		infoLabels...,
	)
}

func (g *GPUCollector) emitGPUOem(ch chan<- prometheus.Metric, gpu SystemGPU, procBaseLabels []string) {
	gpuOEMMetrics, err := gpu.Metrics()
	if err != nil {
		g.logger.With("error", err, "gpu_id", gpu.ID, "system_name", gpu.SystemName).Error("failed obtaining gpu processor metrics, skipping")
		return
	}
	var gpuOEM ProcessorMetricsOEMResponse
	if err := json.Unmarshal(gpuOEMMetrics.OEM, &gpuOEM); err != nil {
		g.logger.With("error", err, "gpu_id", gpu.ID, "system_name", gpu.SystemName).Error("failed unmarshaling gpu processor metrics, skipping")
		return
	}
	ch <- prometheus.MustNewConstMetric(
		g.metrics["gpu_sram_ecc_error_threshold_exceeded"].desc,
		prometheus.GaugeValue,
		boolToFloat64(gpuOEM.Nvidia.SRAMECCErrorThresholdExceeded),
		procBaseLabels...,
	)
	if gpuOEM.Nvidia.AccumulatedGPUContextUtilizationDuration != "" {
		duration, err := isoDuration.Parse(gpuOEM.Nvidia.AccumulatedGPUContextUtilizationDuration)
		if err != nil {
			g.logger.With("error", err, "gpu_id", gpu.ID, "raw_duration", duration).Warn("unable to parse gpu context duration, setting to zero")
			duration = &isoDuration.Duration{
				Seconds: 0,
			}
		}
		labels := []string{gpu.SystemName, gpu.ID}
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_context_utilization_seconds_total"].desc,
			prometheus.CounterValue,
			duration.ToTimeDuration().Seconds(),
			labels...,
		)
	}
}

// nvlinkPortsExpand reads a GPU's Ports collection with the ports and their Metrics
// expanded inline, one request per GPU.
func (g *GPUCollector) nvlinkPortsExpand(ctx context.Context, gpu SystemGPU) (GPUNVLinkCollection, error) {
	var agg GPUNVLinkCollection
	err := getJSON(g.redfishClient.WithContext(ctx), fmt.Sprintf(`%s/Ports?$expand=.($levels=2)`, gpu.ODataID), &agg)
	return agg, err
}

// getJSON GETs a Redfish path and decodes the body into v.
func getJSON(rfClient *gofish.APIClient, rfPath string, v any) error {
	response, err := rfClient.Get(rfPath)
	if err != nil {
		return err
	}
	defer response.Body.Close() // nolint:errcheck // Close() errors on read are not actionable
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func (g *GPUCollector) emitNVLinkTelemetry(ctx context.Context, ch chan<- prometheus.Metric, gpu SystemGPU) {
	agg, err := g.nvlink(g, ctx, gpu)
	if err != nil {
		g.logger.With("error", err, "gpu_id", gpu.ID, "system_name", gpu.SystemName).Error("unable to gather NVLink data, skipping")
		return
	}
	for _, port := range agg.Members {
		if port.PortProtocol != schemas.NVLinkProtocol ||
			!strings.Contains(port.ID, "NVLink_") {
			continue
		}
		strProto := string(port.PortProtocol)
		portLabels := []string{gpu.SystemName, gpu.ID, port.ID, port.PortType, strProto}
		if stateValue, ok := parseCommonStatusState(port.Status.State); ok {
			ch <- prometheus.MustNewConstMetric(
				g.metrics["gpu_nvlink_state"].desc,
				prometheus.GaugeValue,
				stateValue,
				portLabels...,
			)
		}
		if healthValue, ok := parseCommonStatusHealth(port.Status.Health); ok {
			ch <- prometheus.MustNewConstMetric(
				g.metrics["gpu_nvlink_health"].desc,
				prometheus.GaugeValue,
				healthValue,
				portLabels...,
			)
		}
		if linkStatusValue, ok := parseNVLinkPortLinkStatus(port.LinkStatus); ok {
			ch <- prometheus.MustNewConstMetric(
				g.metrics["gpu_nvlink_link_status"].desc,
				prometheus.GaugeValue,
				linkStatusValue,
				portLabels...,
			)
		}
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_nvlink_runtime_error"].desc,
			prometheus.GaugeValue,
			boolToFloat64(port.Metrics.Oem.NVidiaOEM.NVLinkErrors.RuntimeError),
			portLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_nvlink_training_error"].desc,
			prometheus.GaugeValue,
			boolToFloat64(port.Metrics.Oem.NVidiaOEM.NVLinkErrors.TrainingError),
			portLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_nvlink_link_error_recovery_count"].desc,
			prometheus.GaugeValue,
			float64(port.Metrics.Oem.NVidiaOEM.LinkErrorRecoveryCount),
			portLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_nvlink_link_downed_count"].desc,
			prometheus.GaugeValue,
			float64(port.Metrics.Oem.NVidiaOEM.LinkDownedCount),
			portLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_nvlink_symbol_errors"].desc,
			prometheus.GaugeValue,
			float64(port.Metrics.Oem.NVidiaOEM.SymbolErrors),
			portLabels...,
		)
		ch <- prometheus.MustNewConstMetric(
			g.metrics["gpu_nvlink_bit_error_rate"].desc,
			prometheus.GaugeValue,
			port.Metrics.Oem.NVidiaOEM.BitErrorRate,
			portLabels...,
		)
	}
}
