package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	isoDuration "github.com/sosodev/duration"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

const TelemetrySubsystem = "telemetry"

// Sensor ID pattern constants for platform environment metrics parsing
const (
	// Sensor ID prefixes
	sensorPrefixHGXGPU             = "HGX_GPU_"
	sensorPrefixHGXGPUSXM          = "HGX_GPU_SXM_"
	sensorPrefixHGXChassis         = "HGX_Chassis_"
	sensorPrefixProcessorModule    = "ProcessorModule_"
	sensorPrefixHGXProcessorModule = "HGX_ProcessorModule_"
	sensorPrefixHGXBMC             = "HGX_BMC_"
	sensorPrefixHGX                = "HGX_"

	// Sensor ID infixes - metric types
	sensorInfixDRAM            = "_DRAM_"
	sensorInfixEnergy          = "_Energy_"
	sensorInfixPower           = "_Power_"
	sensorInfixTemp            = "_Temp_"
	sensorInfixCPU             = "_CPU_"
	sensorInfixVREGCPU         = "_VREG_CPU_Power_"
	sensorInfixVREGSOC         = "_VREG_SOC_Power_"
	sensorInfixVreg0CpuPower   = "_Vreg_0_CpuPower_"
	sensorInfixVreg0SocPower   = "_Vreg_0_SocPower_"
	sensorInfixTempAvg         = "_TempAvg_"
	sensorInfixTempLimit       = "_TempLimit_"
	sensorInfixEDPCurrentLimit = "_EDP_Current_Limit_"
	sensorInfixEDPPeakLimit    = "_EDP_Peak_Limit_"
	sensorInfixEnforcedEDPc    = "_EnforcedEDPc_"
	sensorInfixEnforcedEDPp    = "_EnforcedEDPp_"
	sensorInfixInletTemp       = "_Inlet_Temp_"
	sensorInfixExhaustTemp     = "_Exhaust_Temp_"
	sensorInfixTEMP0           = "_TEMP_0"
	sensorInfixTEMP1           = "_TEMP_1"
	sensorInfixTemp0           = "_Temp_0"
	sensorInfixTotalGPUPower   = "TotalGPU_Power"

	// Report ID patterns
	reportIDProcessorGPMMetrics   = "HGX_ProcessorGPMMetrics"
	reportIDProcessorMetrics      = "HGX_ProcessorMetrics"
	reportIDProcessorResetMetrics = "HGX_ProcessorResetMetrics"
	reportIDProcessorPortMetrics  = "HGX_ProcessorPortMetrics"
	reportIDMemoryMetrics         = "HGX_MemoryMetrics"
	reportIDPlatformEnvMetrics    = "HGX_PlatformEnvironmentMetrics"

	// ISO8601 duration prefix
	iso8601DurationPrefix = "P"
)

var (
	telemetryBaseLabels     = []string{"system_id", "gpu_id"}
	telemetryMemoryLabels   = []string{"system_id", "gpu_id", "memory_id"}
	telemetryPortLabels     = []string{"system_id", "gpu_id", "port_id"}
	telemetryInstanceLabels = []string{"system_id", "gpu_id", "instance_id"}
	telemetryCPULabels      = []string{"system_id", "cpu_id"}
	telemetryAmbientLabels  = []string{"system_id", "location_id", "sensor_id"}
	telemetryBMCLabels      = []string{"system_id"}
	telemetryMetrics        = createTelemetryMetricMap()

	// GPM instance metrics - these are the only metrics that have per-instance values
	gpmInstanceMetrics = map[string]bool{
		"NVDecInstanceUtilizationPercent": true,
		"NVJpgInstanceUtilizationPercent": true,
	}
)

func createTelemetryMetricMap() map[string]Metric {
	metrics := make(map[string]Metric)

	// Cache ECC errors (L2/SRAM) - different from memory DRAM ECC
	addToMetricMap(metrics, TelemetrySubsystem, "cache_ecc_correctable_total", "Total correctable ECC errors in GPU cache (L2/SRAM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cache_ecc_uncorrectable_total", "Total uncorrectable ECC errors in GPU cache (L2/SRAM)", telemetryBaseLabels)

	// PCIe error counters
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_correctable_errors_total", "Total PCIe correctable errors", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_nonfatal_errors_total", "Total PCIe non-fatal errors", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_fatal_errors_total", "Total PCIe fatal errors", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_l0_to_recovery_total", "Total PCIe L0 to recovery transitions", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_replay_total", "Total PCIe replay events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_replay_rollover_total", "Total PCIe replay rollover events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_nak_sent_total", "Total PCIe NAK sent", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_nak_received_total", "Total PCIe NAK received", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pcie_unsupported_request_total", "Total PCIe unsupported requests", telemetryBaseLabels)

	// Throttling durations
	addToMetricMap(metrics, TelemetrySubsystem, "power_throttle_duration_seconds_total", "Total time GPU was throttled due to power limits", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "thermal_throttle_duration_seconds_total", "Total time GPU was throttled due to thermal limits", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "hardware_violation_throttle_duration_seconds_total", "Total time GPU was throttled due to hardware violations", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "software_violation_throttle_duration_seconds_total", "Total time GPU was throttled due to software violations", telemetryBaseLabels)

	// Memory metrics from HGX_MemoryMetrics_0
	addToMetricMap(metrics, TelemetrySubsystem, "memory_ecc_correctable_lifetime_total", "Lifetime correctable DRAM ECC errors", telemetryMemoryLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "memory_ecc_uncorrectable_lifetime_total", "Lifetime uncorrectable DRAM ECC errors", telemetryMemoryLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "memory_bandwidth_percent", "Memory bandwidth utilization percentage", telemetryMemoryLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "memory_capacity_utilization_percent", "Memory capacity utilization percentage", telemetryMemoryLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "memory_operating_speed_mhz", "Memory operating speed in MHz", telemetryMemoryLabels)

	// Reset metrics from HGX_ProcessorResetMetrics_0
	addToMetricMap(metrics, TelemetrySubsystem, "conventional_reset_entry_total", "Total conventional reset entry events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "conventional_reset_exit_total", "Total conventional reset exit events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "fundamental_reset_entry_total", "Total fundamental reset entry events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "fundamental_reset_exit_total", "Total fundamental reset exit events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "irot_reset_exit_total", "Total IRoT (Internal Root of Trust) reset exit events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pf_flr_reset_entry_total", "Total PF FLR (Physical Function Function-Level Reset) entry events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "pf_flr_reset_exit_total", "Total PF FLR (Physical Function Function-Level Reset) exit events", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "last_reset_type_info", "Last reset type (1=Conventional, 2=Fundamental, 3=IRoT, 4=PF_FLR)", telemetryBaseLabels)

	// Port metrics from HGX_ProcessorPortMetrics_0 - NVLink and PCIe
	// Link state and speed
	addToMetricMap(metrics, TelemetrySubsystem, "port_current_speed_gbps", "Current port link speed in Gbps", telemetryPortLabels)

	// Standard networking metrics
	addToMetricMap(metrics, TelemetrySubsystem, "port_rx_bytes_total", "Total bytes received on port", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_tx_bytes_total", "Total bytes transmitted on port", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_rx_errors_total", "Total receive errors on port", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_rx_frames_total", "Total frames received on port", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_tx_frames_total", "Total frames transmitted on port", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_tx_discards_total", "Total transmit discards on port", telemetryPortLabels)

	// NVIDIA OEM metrics - link reliability
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_intentional_link_down_count_total", "Total intentional link down events (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_unintentional_link_down_count_total", "Total unintentional link down events (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_link_down_reason_code", "Last link down reason code (NVIDIA OEM)", telemetryPortLabels)

	// NVIDIA OEM metrics - error counters
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_neighbor_mtu_discards_total", "Total neighbor MTU discards (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_qp1_dropped_total", "Total QP1 packets dropped (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_rx_remote_physical_errors_total", "Total RX remote physical errors (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_rx_switch_relay_errors_total", "Total RX switch relay errors (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_vl15_dropped_total", "Total VL15 packets dropped (NVIDIA OEM)", telemetryPortLabels)

	// NVIDIA OEM metrics - no-protocol bytes (non-data traffic)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_rx_no_protocol_bytes_total", "Total RX bytes without protocol (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_tx_no_protocol_bytes_total", "Total TX bytes without protocol (NVIDIA OEM)", telemetryPortLabels)

	// NVIDIA OEM metrics - VL15 (management traffic)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_vl15_tx_bytes_total", "Total VL15 bytes transmitted (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_vl15_tx_packets_total", "Total VL15 packets transmitted (NVIDIA OEM)", telemetryPortLabels)

	// NVIDIA OEM metrics - link width and wait
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_rx_width", "Current receive link width (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_tx_width", "Current transmit link width (NVIDIA OEM)", telemetryPortLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_tx_wait_total", "Total TX wait time (NVIDIA OEM)", telemetryPortLabels)

	// NVIDIA OEM metrics - bit error rate
	addToMetricMap(metrics, TelemetrySubsystem, "port_nvidia_total_raw_ber", "Total raw bit error rate (NVIDIA OEM)", telemetryPortLabels)

	// GPM metrics from HGX_ProcessorGPMMetrics_0 - GPU Performance Monitoring (all NVIDIA OEM)
	// Compute unit utilization
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_tensor_core_activity_percent", "Tensor core activity percentage (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_sm_activity_percent", "Streaming Multiprocessor activity percentage (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_sm_occupancy_percent", "Streaming Multiprocessor occupancy percentage (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_fp16_activity_percent", "FP16 floating point activity percentage (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_fp32_activity_percent", "FP32 floating point activity percentage (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_fp64_activity_percent", "FP64 floating point activity percentage (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_integer_activity_percent", "Integer operation activity percentage (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_dmma_utilization_percent", "Double precision Matrix Multiply-Accumulate utilization (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_hmma_utilization_percent", "Half precision Matrix Multiply-Accumulate utilization (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_imma_utilization_percent", "Integer Matrix Multiply-Accumulate utilization (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_graphics_engine_activity_percent", "Graphics engine activity percentage (NVIDIA GPM)", telemetryBaseLabels)

	// Media engine utilization - aggregate
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvdec_utilization_percent", "Video decoder overall utilization (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvjpg_utilization_percent", "JPEG decoder overall utilization (NVIDIA GPM)", telemetryBaseLabels)

	// Media engine utilization - per instance
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvdec_instance_utilization_percent", "Video decoder instance utilization (NVIDIA GPM)", telemetryInstanceLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvjpg_instance_utilization_percent", "JPEG decoder instance utilization (NVIDIA GPM)", telemetryInstanceLabels)

	// Network bandwidth
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvlink_data_rx_bandwidth_gbps", "NVLink data receive bandwidth in Gbps (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvlink_data_tx_bandwidth_gbps", "NVLink data transmit bandwidth in Gbps (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvlink_raw_rx_bandwidth_gbps", "NVLink raw receive bandwidth in Gbps including overhead (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvlink_raw_tx_bandwidth_gbps", "NVLink raw transmit bandwidth in Gbps including overhead (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_nvofa_utilization_percent", "NVIDIA Optimized Fabrics Adapter utilization (NVIDIA GPM)", telemetryBaseLabels)

	// PCIe bandwidth
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_pcie_raw_rx_bandwidth_gbps", "PCIe raw receive bandwidth in Gbps (NVIDIA GPM)", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "nvidia_pcie_raw_tx_bandwidth_gbps", "PCIe raw transmit bandwidth in Gbps (NVIDIA GPM)", telemetryBaseLabels)

	// Platform Environment metrics from HGX_PlatformEnvironmentMetrics_0
	// Backward-compatible metrics (maintain exact names from GPU/Chassis collectors)
	// These use the same label structure as the original collectors for dashboard compatibility
	addToMetricMap(metrics, GPUSubsystem, "memory_power_watts", "GPU memory (DRAM) power consumption in watts", []string{"system_id", "gpu_id", "memory_id"})
	addToMetricMap(metrics, GPUSubsystem, "temperature_tlimit_celsius", "GPU TLIMIT temperature headroom in Celsius", []string{"system_id", "gpu_id"})
	addToMetricMap(metrics, ChassisSubsystem, "gpu_total_power_watts", "Total GPU power consumption for all GPUs in chassis in watts", []string{"resource", "chassis_id"})

	// New telemetry_ prefixed GPU environment metrics
	addToMetricMap(metrics, TelemetrySubsystem, "gpu_energy_joules_total", "Total GPU energy consumption in joules", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "gpu_power_watts", "GPU power consumption in watts", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "gpu_temperature_celsius", "GPU core temperature in Celsius", telemetryBaseLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "gpu_memory_temperature_celsius", "GPU memory temperature in Celsius", telemetryMemoryLabels)

	// CPU environment metrics
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_energy_joules_total", "Total CPU energy consumption in joules", telemetryCPULabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_power_watts", "CPU power consumption in watts", telemetryCPULabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_vreg_cpu_power_watts", "CPU voltage regulator power in watts", telemetryCPULabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_vreg_soc_power_watts", "SoC voltage regulator power in watts", telemetryCPULabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_temperature_average_celsius", "Average CPU temperature in Celsius", telemetryCPULabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_temperature_limit_celsius", "CPU temperature limit in Celsius", telemetryCPULabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_edp_current_limit_watts", "CPU current EDP (Electrical Design Point) limit in watts", telemetryCPULabels)
	addToMetricMap(metrics, TelemetrySubsystem, "cpu_edp_peak_limit_watts", "CPU peak EDP (Electrical Design Point) limit in watts", telemetryCPULabels)

	// Ambient/Environment metrics
	addToMetricMap(metrics, TelemetrySubsystem, "ambient_inlet_temperature_celsius", "Ambient inlet temperature in Celsius", telemetryAmbientLabels)
	addToMetricMap(metrics, TelemetrySubsystem, "ambient_exhaust_temperature_celsius", "Ambient exhaust temperature in Celsius", telemetryAmbientLabels)

	// BMC metrics
	addToMetricMap(metrics, TelemetrySubsystem, "bmc_temperature_celsius", "BMC temperature in Celsius", telemetryBMCLabels)

	// 'Meta' metrics about the collector/collection process
	addToMetricMap(metrics, TelemetrySubsystem, "collection_stale_reports_last", "Quantity of stale reports discovered on the last collection loop", []string{})

	return metrics
}

// TelemetryCollector collects metrics from Redfish TelemetryService
type TelemetryCollector struct {
	redfishClient         *gofish.APIClient
	config                config.TelemetryCollectorConfig
	metrics               map[string]Metric
	logger                *slog.Logger
	collectorScrapeStatus *prometheus.GaugeVec
}

// NewTelemetryCollector creates a new TelemetryService collector
func NewTelemetryCollector(moduleName string, redfishClient *gofish.APIClient, logger *slog.Logger, config config.TelemetryCollectorConfig) (*TelemetryCollector, error) {
	return &TelemetryCollector{
		redfishClient: redfishClient,
		config:        config,
		metrics:       telemetryMetrics,
		logger:        logger.With(slog.String("collector", "TelemetryCollector")),
		collectorScrapeStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "collector_scrape_status",
				Help:      "collector_scrape_status",
			},
			[]string{"collector"},
		),
	}, nil
}

func (t *TelemetryCollector) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) {
	t.collect(ctx, ch)
}

// Describe implements prometheus.Collector
func (t *TelemetryCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range t.metrics {
		ch <- metric.desc
	}
	t.collectorScrapeStatus.Describe(ch)
}

// Collect implements prometheus.Collector
func (t *TelemetryCollector) Collect(ch chan<- prometheus.Metric) {
	t.collect(context.TODO(), ch)
}

func (t *TelemetryCollector) collect(ctx context.Context, ch chan<- prometheus.Metric) {
	if ctx.Err() != nil {
		t.logger.With("error", ctx.Err(), "collector", "telemetry").Debug("skipping collection")
		return
	}
	t.collectorScrapeStatus.WithLabelValues("telemetry").Set(float64(0))

	service := t.redfishClient.Service

	// Get TelemetryService
	telemetryService, err := service.TelemetryService()
	if err != nil {
		t.logger.Debug("failed to get telemetry service - may not be supported",
			slog.Any("error", err),
		)
		return
	}
	if telemetryService == nil {
		t.logger.Debug("telemetry service is nil - may not be supported")
		return
	}

	reportLinks, err := metricReportLinks(t.redfishClient.Service.GetClient(), telemetryService)
	if err != nil {
		// MetricReports may not be available on all TelemetryService implementations
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			t.logger.Debug("metric reports not available - may not be supported",
				slog.Any("error", err),
			)
		} else {
			t.logger.Error("failed to get metric reports",
				slog.Any("error", err),
			)
		}
		return
	}

	// Get systems for hostname mapping
	systems, err := service.Systems()
	if err != nil {
		t.logger.Error("failed to get systems",
			slog.Any("error", err),
		)
		return
	}

	// Create system ID to name mapping
	systemMap := make(map[string]string)
	for _, sys := range systems {
		systemMap[sys.ID] = sys.Name
		if systemMap[sys.ID] == "" {
			systemMap[sys.ID] = sys.ID
		}
	}

	// Fetch and process each metric report this collector parses. The collection can list
	// many it does not, and each is a request.
	var fetchFailed atomic.Bool
	eg := newRecoverGroup(ctx)
	for _, link := range reportLinks {
		if ctx.Err() != nil {
			t.logger.With("error", ctx.Err(), "collector", "telemetry").Debug("skipping further collection")
			continue
		}
		if t.reportParser(path.Base(link)) == nil {
			continue
		}
		eg.Go(func() error {
			report, err := schemas.GetMetricReport(t.redfishClient.Service.GetClient(), link)
			if err != nil {
				t.logger.Error("failed to get metric report", slog.String("report", link), slog.Any("error", err))
				fetchFailed.Store(true)
				return nil
			}
			if parse := t.reportParser(report.ID); parse != nil {
				parse(ch, report, systemMap)
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		t.logger.Error("goroutine error", slog.Any("error", err))
	}
	// A report that failed to fetch leaves the status at 0; the reports that answered are kept.
	if fetchFailed.Load() {
		return
	}
	t.collectorScrapeStatus.WithLabelValues("telemetry").Set(float64(1))
}

// metricReportLinks lists the members of the TelemetryService's MetricReports collection
// without fetching them.
func metricReportLinks(client schemas.Client, telemetryService *schemas.TelemetryService) ([]string, error) {
	var links struct {
		MetricReports schemas.Link `json:"MetricReports"`
	}
	if err := json.Unmarshal(telemetryService.RawData, &links); err != nil {
		return nil, err
	}
	if links.MetricReports == "" {
		return nil, nil
	}
	// A BMC can page the collection; a next link already read ends the walk.
	var reportLinks []string
	seen := map[string]bool{}
	for uri := links.MetricReports.String(); uri != "" && !seen[uri]; {
		seen[uri] = true
		collection, err := schemas.GetCollection(client, uri)
		if err != nil {
			return nil, err
		}
		reportLinks = append(reportLinks, collection.ItemLinks...)
		uri = collection.MembersNextLink
	}
	return reportLinks, nil
}

// reportParser returns the function that processes the metric report with this ID, or nil for
// a report this collector does not parse.
func (t *TelemetryCollector) reportParser(id string) func(chan<- prometheus.Metric, *schemas.MetricReport, map[string]string) {
	switch {
	case strings.Contains(id, reportIDProcessorGPMMetrics):
		return t.collectGPMMetrics
	case strings.Contains(id, reportIDProcessorMetrics) && !strings.Contains(id, reportIDProcessorResetMetrics) && !strings.Contains(id, reportIDProcessorPortMetrics):
		return t.collectProcessorMetrics
	case strings.Contains(id, reportIDMemoryMetrics):
		return t.collectMemoryMetrics
	case strings.Contains(id, reportIDProcessorResetMetrics):
		return t.collectResetMetrics
	case strings.Contains(id, reportIDProcessorPortMetrics):
		return t.collectPortMetrics
	case strings.Contains(id, reportIDPlatformEnvMetrics):
		return t.collectPlatformEnvironmentMetrics
	}
	return nil
}

// collectProcessorMetrics processes a single HGX_ProcessorMetrics report
func (t *TelemetryCollector) collectProcessorMetrics(ch chan<- prometheus.Metric, report *schemas.MetricReport, systemMap map[string]string) {

	t.logger.Debug("processing metric report",
		slog.String("report_id", report.ID),
		slog.Int("metric_count", len(report.MetricValues)),
	)

	// Group metrics by GPU ID
	metricsByGPU := make(map[string]map[string]float64)

	for _, metricValue := range report.MetricValues {
		// Extract GPU ID and metric name from MetricProperty
		// Format: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_SXM_1/ProcessorMetrics#/...
		gpuID, metricName := parseMetricProperty(metricValue.MetricProperty)
		if gpuID == "" || metricName == "" {
			continue
		}

		// Parse metric value
		value, err := parseMetricValue(metricValue.MetricValue)
		if err != nil {
			t.logger.Debug("failed to parse metric value",
				slog.String("metric", metricName),
				slog.String("value", metricValue.MetricValue),
				slog.Any("error", err),
			)
			continue
		}

		// Initialize GPU map if needed
		if metricsByGPU[gpuID] == nil {
			metricsByGPU[gpuID] = make(map[string]float64)
		}
		metricsByGPU[gpuID][metricName] = value
	}

	// Extract system ID from metric properties (all should be same system)
	systemID := extractSystemIDFromReport(report)

	// Emit metrics for each GPU
	for gpuID, metrics := range metricsByGPU {
		labels := []string{systemID, gpuID}
		t.emitGPUMetrics(ch, labels, metrics)
	}
}

// parseMetricProperty extracts GPU ID and metric name from a MetricProperty path
// Example: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_SXM_1/ProcessorMetrics#/CacheMetricsTotal/LifeTime/CorrectableECCErrorCount
// Returns: "GPU_SXM_1", "CacheMetricsTotal/LifeTime/CorrectableECCErrorCount"
func parseMetricProperty(property string) (gpuID string, metricName string) {
	// Split on '#' to separate resource path from property path
	parts := strings.Split(property, "#/")
	if len(parts) != 2 {
		return "", ""
	}

	resourcePath := parts[0]
	metricName = parts[1]

	// Extract GPU ID from resource path
	// Look for pattern: /Processors/GPU_SXM_X/
	if idx := strings.Index(resourcePath, "/Processors/"); idx != -1 {
		remainder := resourcePath[idx+len("/Processors/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			gpuID = remainder[:endIdx]
		}
	}

	return gpuID, metricName
}

// parseMetricValue converts a metric value string to float64
// Handles booleans, numeric values, and ISO 8601 durations
func parseMetricValue(value string) (float64, error) {
	// Handle boolean values
	if value == "true" {
		return 1.0, nil
	}
	if value == "false" {
		return 0.0, nil
	}

	// Check if it's an ISO 8601 duration (starts with "P")
	if strings.HasPrefix(value, iso8601DurationPrefix) || strings.HasPrefix(value, strings.ToLower(iso8601DurationPrefix)) {
		duration, err := isoDuration.Parse(value)
		if err != nil {
			// If duration parsing fails, try as float
			return strconv.ParseFloat(value, 64)
		}
		// Convert to seconds
		return duration.ToTimeDuration().Seconds(), nil
	}

	// Parse as float
	return strconv.ParseFloat(value, 64)
}

// extractSystemIDFromReport extracts the system ID from the first metric property in a report
func extractSystemIDFromReport(report *schemas.MetricReport) string {
	if len(report.MetricValues) == 0 {
		return ""
	}

	// Example: /redfish/v1/Systems/HGX_Baseboard_0/Processors/...
	property := report.MetricValues[0].MetricProperty
	if idx := strings.Index(property, "/Systems/"); idx != -1 {
		remainder := property[idx+len("/Systems/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			return remainder[:endIdx]
		}
	}

	return ""
}

// emitGPUMetrics emits Prometheus metrics for a single GPU
func (t *TelemetryCollector) emitGPUMetrics(ch chan<- prometheus.Metric, labels []string, metrics map[string]float64) {
	// Cache ECC errors
	if val, ok := metrics["CacheMetricsTotal/LifeTime/CorrectableECCErrorCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cache_ecc_correctable_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["CacheMetricsTotal/LifeTime/UncorrectableECCErrorCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cache_ecc_uncorrectable_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// PCIe errors
	if val, ok := metrics["PCIeErrors/CorrectableErrorCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_correctable_errors_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/NonFatalErrorCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_nonfatal_errors_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/FatalErrorCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_fatal_errors_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/L0ToRecoveryCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_l0_to_recovery_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/ReplayCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_replay_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/ReplayRolloverCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_replay_rollover_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/NAKSentCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_nak_sent_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/NAKReceivedCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_nak_received_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeErrors/UnsupportedRequestCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pcie_unsupported_request_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// Throttling durations - parseMetricValue handles ISO 8601 duration conversion
	if val, ok := metrics["PowerLimitThrottleDuration"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_power_throttle_duration_seconds_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["ThermalLimitThrottleDuration"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_thermal_throttle_duration_seconds_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/HardwareViolationThrottleDuration"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_hardware_violation_throttle_duration_seconds_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/GlobalSoftwareViolationThrottleDuration"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_software_violation_throttle_duration_seconds_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
}

// collectMemoryMetrics processes a single HGX_MemoryMetrics report
func (t *TelemetryCollector) collectMemoryMetrics(ch chan<- prometheus.Metric, report *schemas.MetricReport, systemMap map[string]string) {

	t.logger.Debug("processing memory metric report",
		slog.String("report_id", report.ID),
		slog.Int("metric_count", len(report.MetricValues)),
	)

	// Group metrics by GPU ID and Memory ID
	// MetricProperty format: /redfish/v1/Systems/HGX_Baseboard_0/Memory/GPU_SXM_1_DRAM_0/MemoryMetrics#/...
	metricsByMemory := make(map[string]map[string]map[string]float64) // systemID -> memoryID -> metricName -> value

	for _, metricValue := range report.MetricValues {
		// Parse the metric property to extract system ID, memory ID, and metric name
		systemID, memoryID, metricName := parseMemoryMetricProperty(metricValue.MetricProperty)
		if systemID == "" || memoryID == "" || metricName == "" {
			continue
		}

		// Parse metric value
		value, err := parseMetricValue(metricValue.MetricValue)
		if err != nil {
			t.logger.Debug("failed to parse metric value",
				slog.String("metric", metricName),
				slog.String("value", metricValue.MetricValue),
				slog.Any("error", err),
			)
			continue
		}

		// Initialize maps if needed
		if metricsByMemory[systemID] == nil {
			metricsByMemory[systemID] = make(map[string]map[string]float64)
		}
		if metricsByMemory[systemID][memoryID] == nil {
			metricsByMemory[systemID][memoryID] = make(map[string]float64)
		}
		metricsByMemory[systemID][memoryID][metricName] = value
	}

	// Emit metrics for each memory module
	for systemID, memoryMap := range metricsByMemory {
		for memoryID, metrics := range memoryMap {
			// Extract GPU ID from memory ID (e.g., "GPU_SXM_1_DRAM_0" -> "GPU_SXM_1")
			gpuID := extractGPUIDFromMemoryID(memoryID)
			labels := []string{systemID, gpuID, memoryID}
			t.emitMemoryMetrics(ch, labels, metrics)
		}
	}
}

// parseMemoryMetricProperty extracts system ID, memory ID, and metric name from a MetricProperty path
// Example: /redfish/v1/Systems/HGX_Baseboard_0/Memory/GPU_SXM_1_DRAM_0/MemoryMetrics#/LifeTime/CorrectableECCErrorCount
// Returns: "HGX_Baseboard_0", "GPU_SXM_1_DRAM_0", "LifeTime/CorrectableECCErrorCount"
func parseMemoryMetricProperty(property string) (systemID string, memoryID string, metricName string) {
	// Split on '#' to separate resource path from property path
	parts := strings.Split(property, "#/")
	if len(parts) != 2 {
		return "", "", ""
	}

	resourcePath := parts[0]
	metricName = parts[1]

	// Extract system ID: /Systems/SYSTEM_ID/
	if idx := strings.Index(resourcePath, "/Systems/"); idx != -1 {
		remainder := resourcePath[idx+len("/Systems/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			systemID = remainder[:endIdx]
		}
	}

	// Extract memory ID: /Memory/MEMORY_ID/
	if idx := strings.Index(resourcePath, "/Memory/"); idx != -1 {
		remainder := resourcePath[idx+len("/Memory/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			memoryID = remainder[:endIdx]
		}
	}

	return systemID, memoryID, metricName
}

// extractGPUIDFromMemoryID extracts GPU ID from memory ID
// Example: "GPU_SXM_1_DRAM_0" -> "GPU_SXM_1"
func extractGPUIDFromMemoryID(memoryID string) string {
	// Look for pattern GPU_SXM_N or GPU_N
	parts := strings.Split(memoryID, "_")
	if len(parts) >= 2 && parts[0] == "GPU" {
		if parts[1] == "SXM" && len(parts) >= 3 {
			// GPU_SXM_N_...
			return fmt.Sprintf("GPU_SXM_%s", parts[2])
		}
		// GPU_N_...
		return fmt.Sprintf("GPU_%s", parts[1])
	}
	return memoryID
}

// emitMemoryMetrics emits Prometheus metrics for a single memory module
func (t *TelemetryCollector) emitMemoryMetrics(ch chan<- prometheus.Metric, labels []string, metrics map[string]float64) {
	// Lifetime ECC errors
	if val, ok := metrics["LifeTime/CorrectableECCErrorCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_memory_ecc_correctable_lifetime_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["LifeTime/UncorrectableECCErrorCount"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_memory_ecc_uncorrectable_lifetime_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// Bandwidth and utilization
	if val, ok := metrics["BandwidthPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_memory_bandwidth_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["CapacityUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_memory_capacity_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["OperatingSpeedMHz"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_memory_operating_speed_mhz"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
}

// collectResetMetrics processes a single HGX_ProcessorResetMetrics report
func (t *TelemetryCollector) collectResetMetrics(ch chan<- prometheus.Metric, report *schemas.MetricReport, systemMap map[string]string) {

	t.logger.Debug("processing reset metric report",
		slog.String("report_id", report.ID),
		slog.Int("metric_count", len(report.MetricValues)),
	)

	// Group metrics by GPU ID
	metricsByGPU := make(map[string]map[string]interface{}) // interface{} to handle both float64 and string

	for _, metricValue := range report.MetricValues {
		// Extract GPU ID and metric name from MetricProperty
		// Format: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_SXM_1/Oem/Nvidia/ProcessorResetMetrics#/...
		gpuID, metricName := parseResetMetricProperty(metricValue.MetricProperty)
		if gpuID == "" || metricName == "" {
			continue
		}

		// For LastResetType, store the string value; for others, parse as float
		if metricName == "LastResetType" {
			// Initialize GPU map if needed
			if metricsByGPU[gpuID] == nil {
				metricsByGPU[gpuID] = make(map[string]interface{})
			}
			metricsByGPU[gpuID][metricName] = metricValue.MetricValue
		} else {
			// Parse numeric metric value
			value, err := parseMetricValue(metricValue.MetricValue)
			if err != nil {
				t.logger.Debug("failed to parse reset metric value",
					slog.String("metric", metricName),
					slog.String("value", metricValue.MetricValue),
					slog.Any("error", err),
				)
				continue
			}

			// Initialize GPU map if needed
			if metricsByGPU[gpuID] == nil {
				metricsByGPU[gpuID] = make(map[string]interface{})
			}
			metricsByGPU[gpuID][metricName] = value
		}
	}

	// Extract system ID from metric properties (all should be same system)
	systemID := extractSystemIDFromReport(report)

	// Emit metrics for each GPU
	for gpuID, metrics := range metricsByGPU {
		labels := []string{systemID, gpuID}
		t.emitResetMetrics(ch, labels, metrics)
	}
}

// parseResetMetricProperty extracts GPU ID and metric name from a reset MetricProperty path
// Example: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_SXM_1/Oem/Nvidia/ProcessorResetMetrics#/ConventionalResetEntryCount
// Returns: "GPU_SXM_1", "ConventionalResetEntryCount"
func parseResetMetricProperty(property string) (gpuID string, metricName string) {
	// Split on '#' to separate resource path from property path
	parts := strings.Split(property, "#/")
	if len(parts) != 2 {
		return "", ""
	}

	resourcePath := parts[0]
	metricName = parts[1]

	// Extract GPU ID from resource path
	// Look for pattern: /Processors/GPU_SXM_X/
	if idx := strings.Index(resourcePath, "/Processors/"); idx != -1 {
		remainder := resourcePath[idx+len("/Processors/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			gpuID = remainder[:endIdx]
		}
	}

	return gpuID, metricName
}

// emitResetMetrics emits Prometheus metrics for GPU reset events
func (t *TelemetryCollector) emitResetMetrics(ch chan<- prometheus.Metric, labels []string, metrics map[string]interface{}) {
	// Conventional reset counters
	if val, ok := metrics["ConventionalResetEntryCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_conventional_reset_entry_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["ConventionalResetExitCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_conventional_reset_exit_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// Fundamental reset counters
	if val, ok := metrics["FundamentalResetEntryCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_fundamental_reset_entry_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["FundamentalResetExitCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_fundamental_reset_exit_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// IRoT reset counter
	if val, ok := metrics["IRoTResetExitCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_irot_reset_exit_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// PF_FLR reset counters
	if val, ok := metrics["PF_FLR_ResetEntryCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pf_flr_reset_entry_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PF_FLR_ResetExitCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_pf_flr_reset_exit_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// LastResetType - convert string to numeric value
	if val, ok := metrics["LastResetType"].(string); ok {
		var resetTypeValue float64
		switch val {
		case "Conventional":
			resetTypeValue = 1.0
		case "Fundamental":
			resetTypeValue = 2.0
		case "IRoT":
			resetTypeValue = 3.0
		case "PF_FLR":
			resetTypeValue = 4.0
		default:
			// Unknown reset type, skip emitting
			return
		}

		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_last_reset_type_info"].desc,
			prometheus.GaugeValue,
			resetTypeValue,
			labels...,
		)
	}
}

// collectPortMetrics processes a single HGX_ProcessorPortMetrics report
func (t *TelemetryCollector) collectPortMetrics(ch chan<- prometheus.Metric, report *schemas.MetricReport, systemMap map[string]string) {

	t.logger.Debug("processing port metric report",
		slog.String("report_id", report.ID),
		slog.Int("metric_count", len(report.MetricValues)),
	)

	// Group metrics by GPU ID and Port ID
	// MetricProperty format: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_SXM_0/Ports/NVLink_0/Metrics#/RXBytes
	// or /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_SXM_0/Ports/PCIe/Metrics#/CurrentSpeedGbps
	metricsByPort := make(map[string]map[string]map[string]interface{}) // gpuID -> portID -> metricName -> value

	for _, metricValue := range report.MetricValues {
		// Extract GPU ID, port ID, and metric name from MetricProperty
		gpuID, portID, metricName := parsePortMetricProperty(metricValue.MetricProperty)
		if gpuID == "" || portID == "" || metricName == "" {
			continue
		}

		// For LinkDownReasonCode, store the string value; for others, parse as float
		if metricName == "Oem/Nvidia/LinkDownReasonCode" {
			// Initialize maps if needed
			if metricsByPort[gpuID] == nil {
				metricsByPort[gpuID] = make(map[string]map[string]interface{})
			}
			if metricsByPort[gpuID][portID] == nil {
				metricsByPort[gpuID][portID] = make(map[string]interface{})
			}
			metricsByPort[gpuID][portID][metricName] = metricValue.MetricValue
		} else {
			// Parse numeric metric value
			value, err := parseMetricValue(metricValue.MetricValue)
			if err != nil {
				t.logger.Debug("failed to parse port metric value",
					slog.String("metric", metricName),
					slog.String("value", metricValue.MetricValue),
					slog.Any("error", err),
				)
				continue
			}

			// Initialize maps if needed
			if metricsByPort[gpuID] == nil {
				metricsByPort[gpuID] = make(map[string]map[string]interface{})
			}
			if metricsByPort[gpuID][portID] == nil {
				metricsByPort[gpuID][portID] = make(map[string]interface{})
			}
			metricsByPort[gpuID][portID][metricName] = value
		}
	}

	// Extract system ID from metric properties (all should be same system)
	systemID := extractSystemIDFromReport(report)

	// Emit metrics for each port
	for gpuID, ports := range metricsByPort {
		for portID, metrics := range ports {
			labels := []string{systemID, gpuID, portID}
			t.emitPortMetrics(ch, labels, metrics)
		}
	}
}

// parsePortMetricProperty extracts GPU ID, port ID, and metric name from a port MetricProperty path
// Example: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_SXM_0/Ports/NVLink_0/Metrics#/RXBytes
// Returns: "GPU_SXM_0", "NVLink_0", "RXBytes"
func parsePortMetricProperty(property string) (gpuID string, portID string, metricName string) {
	// Split on '#' to separate resource path from property path
	parts := strings.Split(property, "#/")
	if len(parts) != 2 {
		return "", "", ""
	}

	resourcePath := parts[0]
	metricName = parts[1]

	// Extract GPU ID from resource path: /Processors/GPU_SXM_X/
	if idx := strings.Index(resourcePath, "/Processors/"); idx != -1 {
		remainder := resourcePath[idx+len("/Processors/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			gpuID = remainder[:endIdx]
		}
	}

	// Extract Port ID from resource path: /Ports/PORT_ID/
	if idx := strings.Index(resourcePath, "/Ports/"); idx != -1 {
		remainder := resourcePath[idx+len("/Ports/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			portID = remainder[:endIdx]
		}
	}

	return gpuID, portID, metricName
}

// emitPortMetrics emits Prometheus metrics for a single port
func (t *TelemetryCollector) emitPortMetrics(ch chan<- prometheus.Metric, labels []string, metrics map[string]interface{}) {
	// Link speed
	if val, ok := metrics["CurrentSpeedGbps"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_current_speed_gbps"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	// Standard networking metrics - cumulative counters
	if val, ok := metrics["RXBytes"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_rx_bytes_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["TXBytes"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_tx_bytes_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["RXErrors"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_rx_errors_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Networking/RXFrames"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_rx_frames_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Networking/TXFrames"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_tx_frames_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Networking/TXDiscards"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_tx_discards_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// NVIDIA OEM metrics - link reliability counters
	if val, ok := metrics["Oem/Nvidia/IntentionalLinkDownCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_intentional_link_down_count_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/UnintentionalLinkDownCount"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_unintentional_link_down_count_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// LinkDownReasonCode - convert string to numeric value if it exists
	if val, ok := metrics["Oem/Nvidia/LinkDownReasonCode"].(string); ok {
		// Parse as integer if possible, otherwise skip
		if reasonCode, err := strconv.ParseFloat(val, 64); err == nil {
			ch <- prometheus.MustNewConstMetric(
				t.metrics["telemetry_port_nvidia_link_down_reason_code"].desc,
				prometheus.GaugeValue,
				reasonCode,
				labels...,
			)
		}
	}

	// NVIDIA OEM metrics - error counters
	if val, ok := metrics["Oem/Nvidia/NeighborMTUDiscards"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_neighbor_mtu_discards_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/QP1Dropped"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_qp1_dropped_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/RXRemotePhysicalErrors"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_rx_remote_physical_errors_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/RXSwitchRelayErrors"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_rx_switch_relay_errors_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/VL15Dropped"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_vl15_dropped_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// NVIDIA OEM metrics - no-protocol bytes
	if val, ok := metrics["Oem/Nvidia/RXNoProtocolBytes"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_rx_no_protocol_bytes_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/TXNoProtocolBytes"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_tx_no_protocol_bytes_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// NVIDIA OEM metrics - VL15 (management traffic)
	if val, ok := metrics["Oem/Nvidia/VL15TXBytes"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_vl15_tx_bytes_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/VL15TXPackets"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_vl15_tx_packets_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// NVIDIA OEM metrics - link width (current state)
	if val, ok := metrics["Oem/Nvidia/RXWidth"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_rx_width"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["Oem/Nvidia/TXWidth"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_tx_width"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	// NVIDIA OEM metrics - TX wait (cumulative)
	if val, ok := metrics["Oem/Nvidia/TXWait"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_tx_wait_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	// NVIDIA OEM metrics - bit error rate (current state)
	if val, ok := metrics["Oem/Nvidia/TotalRawBER"].(float64); ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_port_nvidia_total_raw_ber"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
}

// collectGPMMetrics processes a single HGX_ProcessorGPMMetrics report
func (t *TelemetryCollector) collectGPMMetrics(ch chan<- prometheus.Metric, report *schemas.MetricReport, systemMap map[string]string) {

	t.logger.Debug("processing GPM metric report",
		slog.String("report_id", report.ID),
		slog.Int("metric_count", len(report.MetricValues)),
	)

	// Group metrics by GPU ID
	// MetricProperty format: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_0/ProcessorMetrics#/Oem/Nvidia/...
	metricsByGPU := make(map[string]map[string]float64)
	instanceMetricsByGPU := make(map[string]map[string]map[string]float64) // gpuID -> instanceID -> metricName -> value

	for _, metricValue := range report.MetricValues {
		// Parse the metric property
		gpuID, metricName := parseGPMMetricProperty(metricValue.MetricProperty)
		if gpuID == "" || metricName == "" {
			continue
		}

		// Parse metric value
		value, err := parseMetricValue(metricValue.MetricValue)
		if err != nil {
			t.logger.Debug("failed to parse GPM metric value",
				slog.String("metric", metricName),
				slog.String("value", metricValue.MetricValue),
				slog.Any("error", err),
			)
			continue
		}

		// Check if this is an instance metric (contains /0, /1, etc.)
		if strings.Contains(metricName, "/") {
			// Extract metric name and instance ID
			parts := strings.Split(metricName, "/")
			if len(parts) == 2 {
				baseMetricName := parts[0]
				instanceID := parts[1]

				// Validate this is a known instance metric
				if !gpmInstanceMetrics[baseMetricName] {
					t.logger.Debug("skipping unexpected instance-like metric",
						slog.String("metric", metricName),
						slog.String("gpu_id", gpuID))
					continue
				}

				// Validate instance ID is numeric 0-7
				if len(instanceID) != 1 || instanceID[0] < '0' || instanceID[0] > '7' {
					t.logger.Debug("invalid instance ID",
						slog.String("metric", metricName),
						slog.String("instance_id", instanceID))
					continue
				}

				// Initialize maps if needed
				if instanceMetricsByGPU[gpuID] == nil {
					instanceMetricsByGPU[gpuID] = make(map[string]map[string]float64)
				}
				if instanceMetricsByGPU[gpuID][instanceID] == nil {
					instanceMetricsByGPU[gpuID][instanceID] = make(map[string]float64)
				}
				instanceMetricsByGPU[gpuID][instanceID][baseMetricName] = value
			}
		} else {
			// Regular metric
			if metricsByGPU[gpuID] == nil {
				metricsByGPU[gpuID] = make(map[string]float64)
			}
			metricsByGPU[gpuID][metricName] = value
		}
	}

	// Extract system ID from metric properties (all should be same system)
	systemID := extractSystemIDFromReport(report)

	// Emit regular metrics for each GPU
	for gpuID, metrics := range metricsByGPU {
		labels := []string{systemID, gpuID}
		t.emitGPMMetrics(ch, labels, metrics)
	}

	// Emit instance metrics
	for gpuID, instances := range instanceMetricsByGPU {
		for instanceID, metrics := range instances {
			labels := []string{systemID, gpuID, instanceID}
			t.emitGPMInstanceMetrics(ch, labels, metrics)
		}
	}
}

// parseGPMMetricProperty extracts GPU ID and metric name from a GPM MetricProperty path
// Example: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_0/ProcessorMetrics#/Oem/Nvidia/TensorCoreActivityPercent
// Returns: "GPU_0", "TensorCoreActivityPercent"
// Example with instance: /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_0/ProcessorMetrics#/Oem/Nvidia/NVDecInstanceUtilizationPercent/0
// Returns: "GPU_0", "NVDecInstanceUtilizationPercent/0"
func parseGPMMetricProperty(property string) (gpuID string, metricName string) {
	// Split on '#' to separate resource path from property path
	parts := strings.Split(property, "#/")
	if len(parts) != 2 {
		return "", ""
	}

	resourcePath := parts[0]
	propertyPath := parts[1]

	// Extract GPU ID from resource path: /Processors/GPU_X/
	if idx := strings.Index(resourcePath, "/Processors/"); idx != -1 {
		remainder := resourcePath[idx+len("/Processors/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			gpuID = remainder[:endIdx]
		}
	}

	// Extract metric name from property path
	// Remove Oem/Nvidia/ prefix if present
	metricName = strings.TrimPrefix(propertyPath, "Oem/Nvidia/")

	return gpuID, metricName
}

// emitGPMMetrics emits Prometheus metrics for GPU Performance Monitoring (non-instance metrics)
func (t *TelemetryCollector) emitGPMMetrics(ch chan<- prometheus.Metric, labels []string, metrics map[string]float64) {
	// Compute unit utilization
	if val, ok := metrics["TensorCoreActivityPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_tensor_core_activity_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["SMActivityPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_sm_activity_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["SMOccupancyPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_sm_occupancy_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["FP16ActivityPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_fp16_activity_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["FP32ActivityPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_fp32_activity_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["FP64ActivityPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_fp64_activity_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["IntegerActivityUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_integer_activity_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["DMMAUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_dmma_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["HMMAUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_hmma_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["IMMAUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_imma_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["GraphicsEngineActivityPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_graphics_engine_activity_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	// Media engine utilization - aggregate
	if val, ok := metrics["NVDecUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvdec_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["NVJpgUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvjpg_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	// Network bandwidth
	if val, ok := metrics["NVLinkDataRxBandwidthGbps"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvlink_data_rx_bandwidth_gbps"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["NVLinkDataTxBandwidthGbps"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvlink_data_tx_bandwidth_gbps"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["NVLinkRawRxBandwidthGbps"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvlink_raw_rx_bandwidth_gbps"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["NVLinkRawTxBandwidthGbps"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvlink_raw_tx_bandwidth_gbps"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["NVOfaUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvofa_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	// PCIe bandwidth
	if val, ok := metrics["PCIeRawRxBandwidthGbps"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_pcie_raw_rx_bandwidth_gbps"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["PCIeRawTxBandwidthGbps"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_pcie_raw_tx_bandwidth_gbps"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
}

// emitGPMInstanceMetrics emits Prometheus metrics for GPU Performance Monitoring instance metrics (NVDec, NVJpg)
func (t *TelemetryCollector) emitGPMInstanceMetrics(ch chan<- prometheus.Metric, labels []string, metrics map[string]float64) {
	// Media engine utilization - per instance
	if val, ok := metrics["NVDecInstanceUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvdec_instance_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
	if val, ok := metrics["NVJpgInstanceUtilizationPercent"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_nvidia_nvjpg_instance_utilization_percent"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
}

// collectPlatformEnvironmentMetrics processes HGX_PlatformEnvironmentMetrics report
// This collects GPU, CPU, and ambient environmental metrics (power, temperature, energy)
func (t *TelemetryCollector) collectPlatformEnvironmentMetrics(ch chan<- prometheus.Metric, report *schemas.MetricReport, systemMap map[string]string) {

	t.logger.Debug("processing platform environment metric report",
		slog.String("report_id", report.ID),
		slog.Int("metric_count", len(report.MetricValues)),
	)

	// Group metrics by component to enable batch emission with all related values
	// This reduces the number of Prometheus metric emissions and improves efficiency
	gpuMetrics := make(map[string]map[string]float64)                // gpuID -> metricType -> value
	cpuMetrics := make(map[string]map[string]float64)                // cpuID -> metricType -> value
	ambientMetrics := make(map[string]map[string]map[string]float64) // locationID -> sensorID -> metricType -> value
	var bmcTemp float64
	var hasBMCTemp bool
	var totalGPUPower float64
	var hasTotalGPUPower bool
	var chassisID string

	staleMarker := []byte(`"MetricValueStale": true`)
	totalStaleReports := 0.0
	for _, metricValue := range report.MetricValues {
		if bytes.Contains(metricValue.OEM, staleMarker) || metricValue.MetricValue == "nan" {
			totalStaleReports += 1.0
			continue
		}

		sensorPath := metricValue.MetricProperty
		value, err := parseMetricValue(metricValue.MetricValue)
		if err != nil {
			t.logger.Debug("failed to parse platform environment metric value",
				slog.String("sensor", sensorPath),
				slog.String("value", metricValue.MetricValue),
				slog.Any("error", err),
			)
			continue
		}

		chassisIDFromPath, sensorID := parseSensorPath(sensorPath)
		if chassisIDFromPath == "" || sensorID == "" {
			t.logger.Debug("failed to parse sensor path",
				slog.String("sensor_path", sensorPath),
			)
			continue
		}

		// Capture chassis ID from first valid metric for system ID mapping
		if chassisID == "" {
			chassisID = chassisIDFromPath
		}

		// Route to component-specific parser based on sensor ID prefix
		if strings.HasPrefix(sensorID, sensorPrefixHGXGPU) {
			t.parseGPUSensorMetric(sensorID, value, gpuMetrics)
		} else if strings.HasPrefix(sensorID, sensorPrefixHGXChassis) && strings.Contains(sensorID, sensorInfixTotalGPUPower) {
			// Backward-compatible total GPU power metric (replaces chassis collector)
			totalGPUPower = value
			hasTotalGPUPower = true
		} else if strings.HasPrefix(sensorID, sensorPrefixProcessorModule) && strings.Contains(sensorID, sensorInfixCPU) {
			t.parseCPUSensorMetric(sensorID, value, cpuMetrics)
		} else if strings.HasPrefix(sensorID, sensorPrefixHGXProcessorModule) {
			t.parseAmbientSensorMetric(sensorID, value, ambientMetrics)
		} else if strings.HasPrefix(sensorID, sensorPrefixHGXBMC) && strings.Contains(sensorID, sensorInfixTemp) {
			// Single BMC temperature sensor per system
			bmcTemp = value
			hasBMCTemp = true
		}
	}

	// Map chassis ID to system ID for consistent labeling across metrics
	systemID := extractSystemIDFromChassis(chassisID)

	// Emit grouped metrics for each component
	for gpuID, metrics := range gpuMetrics {
		t.emitPlatformGPUMetrics(ch, systemID, gpuID, metrics)
	}

	for cpuID, metrics := range cpuMetrics {
		t.emitPlatformCPUMetrics(ch, systemID, cpuID, metrics)
	}

	for locationID, sensors := range ambientMetrics {
		for sensorID, metrics := range sensors {
			t.emitPlatformAmbientMetrics(ch, systemID, locationID, sensorID, metrics)
		}
	}

	if hasBMCTemp {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_bmc_temperature_celsius"].desc,
			prometheus.GaugeValue,
			bmcTemp,
			systemID,
		)
	}

	if hasTotalGPUPower {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["chassis_gpu_total_power_watts"].desc,
			prometheus.GaugeValue,
			totalGPUPower,
			"chassis", chassisID,
		)
	}

	ch <- prometheus.MustNewConstMetric(
		t.metrics["telemetry_collection_stale_reports_last"].desc,
		prometheus.GaugeValue,
		totalStaleReports,
	)
}

// parseSensorPath extracts chassis ID and sensor ID from a Redfish sensor path.
// Chassis ID is needed for system ID mapping, sensor ID determines metric routing.
// Example: /redfish/v1/Chassis/HGX_GPU_0/Sensors/HGX_GPU_0_Power_0 -> "HGX_GPU_0", "HGX_GPU_0_Power_0"
func parseSensorPath(sensorPath string) (chassisID string, sensorID string) {
	if idx := strings.Index(sensorPath, "/Chassis/"); idx != -1 {
		remainder := sensorPath[idx+len("/Chassis/"):]
		if endIdx := strings.Index(remainder, "/"); endIdx != -1 {
			chassisID = remainder[:endIdx]
		}
	}

	if idx := strings.Index(sensorPath, "/Sensors/"); idx != -1 {
		sensorID = sensorPath[idx+len("/Sensors/"):]
	}

	return chassisID, sensorID
}

// parseGPUSensorMetric parses GPU sensor IDs to extract GPU identifier and metric type.
// Exported for testability. Memory metrics include memoryID for DRAM-specific labeling.
// Example: "HGX_GPU_0_DRAM_0_Power_0" -> gpuID="GPU_0", metricType="memory_power", memoryID="GPU_0_DRAM_0"
func parseGPUSensorMetric(sensorID string) (string, string, string) {
	parts := strings.Split(sensorID, "_")
	if len(parts) < 3 {
		return "", "", ""
	}

	gpuID := ""

	if strings.HasPrefix(sensorID, sensorPrefixHGXGPUSXM) {
		// HGX_GPU_SXM_0_DRAM_0_Power_0 -> //GPU_0
		gpuID = fmt.Sprintf("%s_%s", parts[1], parts[3]) // GPU_0
	} else {
		// HGX_GPU_0_DRAM_0_Power_0 -> GPU_0
		gpuID = fmt.Sprintf("%s_%s", parts[1], parts[2]) // GPU_0
	}

	if strings.Contains(sensorID, sensorInfixDRAM) {
		memoryID := extractMemoryIDFromSensor(sensorID)
		if strings.Contains(sensorID, sensorInfixPower) {
			return gpuID, "memory_power", memoryID
		} else if strings.Contains(sensorID, sensorInfixTemp) {
			return gpuID, "memory_temp", memoryID
		}
	} else if strings.Contains(sensorID, sensorInfixEnergy) {
		return gpuID, "energy", ""
	} else if strings.Contains(sensorID, sensorInfixPower) {
		return gpuID, "power", ""
	} else if strings.Contains(sensorID, sensorInfixTEMP0) {
		return gpuID, "temp0", ""
	} else if strings.Contains(sensorID, sensorInfixTEMP1) {
		return gpuID, "temp1", ""
	}

	return gpuID, "", ""
}

// parseCPUSensorMetric parses CPU sensor IDs to extract CPU identifier and metric type.
// Exported for testability. Uses processor module number as CPU ID to match chassis organization.
// Example: "ProcessorModule_0_CPU_0_Power_0" -> cpuID="CPU_0", metricType="power"
// Vreg metrics are checked first since they contain "_Power_" but need distinct handling.
func parseCPUSensorMetric(sensorID string) (string, string) {
	parts := strings.Split(sensorID, "_")
	if len(parts) < 4 {
		return "", ""
	}

	// ProcessorModule_0 maps to CPU_0 (module number becomes CPU ID)
	cpuID := fmt.Sprintf("CPU_%s", parts[1])
	if strings.Contains(sensorID, sensorInfixVREGCPU) || strings.Contains(sensorID, sensorInfixVreg0CpuPower) {
		return cpuID, "vreg_cpu"
	} else if strings.Contains(sensorID, sensorInfixVREGSOC) || strings.Contains(sensorID, sensorInfixVreg0SocPower) {
		return cpuID, "vreg_soc"
	} else if strings.Contains(sensorID, sensorInfixEnergy) {
		return cpuID, "energy"
	} else if strings.Contains(sensorID, sensorInfixPower) && strings.Contains(sensorID, sensorInfixCPU) {
		return cpuID, "power"
	} else if strings.Contains(sensorID, sensorInfixTemp0) || strings.Contains(sensorID, sensorInfixTempAvg) {
		return cpuID, "temp"
	} else if strings.Contains(sensorID, sensorInfixTempLimit) {
		return cpuID, "temp_limit"
	} else if strings.Contains(sensorID, sensorInfixEDPCurrentLimit) || strings.Contains(sensorID, sensorInfixEnforcedEDPc) {
		return cpuID, "edp_current"
	} else if strings.Contains(sensorID, sensorInfixEDPPeakLimit) || strings.Contains(sensorID, sensorInfixEnforcedEDPp) {
		return cpuID, "edp_peak"
	}

	return cpuID, ""
}

// parseAmbientSensorMetric parses ambient temperature sensor IDs to extract location and metric type.
// Exported for testability. Returns full sensorID for sensor_id label (supports multiple sensors per location).
// Example: "HGX_ProcessorModule_0_Inlet_Temp_0" -> locationID="ProcessorModule_0", metricType="inlet", sensorID=(full ID)
func parseAmbientSensorMetric(sensorID string) (string, string, string) {
	parts := strings.Split(sensorID, "_")
	if len(parts) < 4 {
		return "", "", ""
	}

	locationID := fmt.Sprintf("%s_%s", parts[1], parts[2]) // ProcessorModule_0

	if strings.Contains(sensorID, sensorInfixInletTemp) {
		return locationID, "inlet", sensorID
	} else if strings.Contains(sensorID, sensorInfixExhaustTemp) {
		return locationID, "exhaust", sensorID
	}

	return locationID, "", sensorID
}

// extractSystemIDFromChassis maps chassis ID to system ID for consistent metric labeling.
// Uses hardcoded mapping since all HGX chassis belong to HGX_Baseboard_0 system.
// TODO: Query chassis->system relationship from Redfish API for more robust mapping.
func extractSystemIDFromChassis(chassisID string) string {
	if strings.HasPrefix(chassisID, sensorPrefixHGX) {
		return "HGX_Baseboard_0"
	}
	return chassisID
}

// parseGPUSensorMetric parses a GPU sensor metric and stores it
func (t *TelemetryCollector) parseGPUSensorMetric(sensorID string, value float64, gpuMetrics map[string]map[string]float64) {
	// Use standalone parsing function to extract GPU ID, metric type, and memory ID
	gpuID, metricType, memoryID := parseGPUSensorMetric(sensorID)
	if gpuID == "" || metricType == "" {
		// Parsing failed or unknown metric type - log at debug level
		t.logger.Debug("failed to parse GPU sensor metric",
			slog.String("sensor_id", sensorID),
		)
		return
	}

	// Initialize GPU map if needed
	if gpuMetrics[gpuID] == nil {
		gpuMetrics[gpuID] = make(map[string]float64)
	}

	// Map metric type to storage key
	var storageKey string
	switch metricType {
	case "energy":
		storageKey = "energy"
	case "power":
		storageKey = "power"
	case "temp0":
		// TEMP_0 is core temperature
		storageKey = "temp_core"
	case "temp1":
		// TEMP_1 is TLIMIT temperature headroom
		storageKey = "temp_tlimit"
	case "memory_power":
		storageKey = fmt.Sprintf("memory_power_%s", memoryID)
	case "memory_temp":
		storageKey = fmt.Sprintf("memory_temp_%s", memoryID)
	default:
		t.logger.Debug("unknown GPU metric type",
			slog.String("sensor_id", sensorID),
			slog.String("metric_type", metricType),
		)
		return
	}

	gpuMetrics[gpuID][storageKey] = value
}

// extractMemoryIDFromSensor extracts memory ID from sensor ID
// Example: "HGX_GPU_0_DRAM_0_Power_0" -> "GPU_0_DRAM_0"
func extractMemoryIDFromSensor(sensorID string) string {
	parts := strings.Split(sensorID, "_")
	if len(parts) >= 5 {
		if strings.HasPrefix(sensorID, sensorPrefixHGXGPUSXM) {
			// HGX_GPU_SXM_0_DRAM_0_... -> GPU_0_DRAM_0
			return fmt.Sprintf("%s_%s_%s_%s", parts[1], parts[3], parts[4], parts[5])
		}
		// HGX_GPU_0_DRAM_0_... -> GPU_0_DRAM_0
		return fmt.Sprintf("%s_%s_%s_%s", parts[1], parts[2], parts[3], parts[4])
	}
	return ""
}

// parseCPUSensorMetric parses a CPU sensor metric and stores it
func (t *TelemetryCollector) parseCPUSensorMetric(sensorID string, value float64, cpuMetrics map[string]map[string]float64) {
	// Use standalone parsing function to extract CPU ID and metric type
	cpuID, metricType := parseCPUSensorMetric(sensorID)
	if cpuID == "" || metricType == "" {
		// Parsing failed or unknown metric type - log at debug level
		t.logger.Debug("failed to parse CPU sensor metric",
			slog.String("sensor_id", sensorID),
		)
		return
	}

	// Initialize CPU map if needed
	if cpuMetrics[cpuID] == nil {
		cpuMetrics[cpuID] = make(map[string]float64)
	}

	// Map metric type to storage key
	// Note: Some metric types need different storage keys for backward compatibility
	var storageKey string
	switch metricType {
	case "energy":
		storageKey = "energy"
	case "power":
		storageKey = "power"
	case "temp":
		storageKey = "temp_avg"
	case "temp_limit":
		storageKey = "temp_limit"
	case "edp_current":
		storageKey = "edp_current"
	case "edp_peak":
		storageKey = "edp_peak"
	case "vreg_cpu":
		storageKey = "vreg_cpu_power"
	case "vreg_soc":
		storageKey = "vreg_soc_power"
	default:
		t.logger.Debug("unknown CPU metric type",
			slog.String("sensor_id", sensorID),
			slog.String("metric_type", metricType),
		)
		return
	}

	cpuMetrics[cpuID][storageKey] = value
}

// parseAmbientSensorMetric parses an ambient sensor metric and stores it
func (t *TelemetryCollector) parseAmbientSensorMetric(sensorID string, value float64, ambientMetrics map[string]map[string]map[string]float64) {
	// Use standalone parsing function to extract location ID, metric type, and full sensor ID
	locationID, metricType, fullSensorID := parseAmbientSensorMetric(sensorID)
	if locationID == "" || metricType == "" {
		// Parsing failed or unknown metric type - log at debug level
		t.logger.Debug("failed to parse ambient sensor metric",
			slog.String("sensor_id", sensorID),
		)
		return
	}

	// Extract sensor-specific ID (last part of sensor ID)
	parts := strings.Split(fullSensorID, "_")
	sensorSpecificID := parts[len(parts)-1] // "0" or "1"

	// Initialize maps if needed
	if ambientMetrics[locationID] == nil {
		ambientMetrics[locationID] = make(map[string]map[string]float64)
	}
	if ambientMetrics[locationID][sensorSpecificID] == nil {
		ambientMetrics[locationID][sensorSpecificID] = make(map[string]float64)
	}

	// Map metric type to storage key
	var storageKey string
	switch metricType {
	case "inlet":
		storageKey = "inlet_temp"
	case "exhaust":
		storageKey = "exhaust_temp"
	default:
		t.logger.Debug("unknown ambient metric type",
			slog.String("sensor_id", sensorID),
			slog.String("metric_type", metricType),
		)
		return
	}

	ambientMetrics[locationID][sensorSpecificID][storageKey] = value
}

// emitPlatformGPUMetrics emits GPU environment metrics
func (t *TelemetryCollector) emitPlatformGPUMetrics(ch chan<- prometheus.Metric, systemID, gpuID string, metrics map[string]float64) {
	baseLabels := []string{systemID, gpuID}

	// Energy (telemetry_ prefixed)
	if val, ok := metrics["energy"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_gpu_energy_joules_total"].desc,
			prometheus.CounterValue,
			val,
			baseLabels...,
		)
	}

	// Power (telemetry_ prefixed)
	if val, ok := metrics["power"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_gpu_power_watts"].desc,
			prometheus.GaugeValue,
			val,
			baseLabels...,
		)
	}

	// Core temperature (telemetry_ prefixed)
	if val, ok := metrics["temp_core"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_gpu_temperature_celsius"].desc,
			prometheus.GaugeValue,
			val,
			baseLabels...,
		)
	}

	// TLIMIT temperature (backward-compatible metric name in gpu subsystem)
	if val, ok := metrics["temp_tlimit"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["gpu_temperature_tlimit_celsius"].desc,
			prometheus.GaugeValue,
			val,
			baseLabels...,
		)
	}

	// Emit per-DRAM memory metrics (multiple DRAM modules per GPU)
	// Each DRAM module gets its own metric instance with unique memory_id label
	for metricName, value := range metrics {
		if !strings.HasPrefix(metricName, "memory_") {
			continue // Skip non-memory metrics
		}

		if strings.HasPrefix(metricName, "memory_power_") {
			memoryID := strings.TrimPrefix(metricName, "memory_power_")
			memoryLabels := []string{systemID, gpuID, memoryID}

			ch <- prometheus.MustNewConstMetric(
				t.metrics["gpu_memory_power_watts"].desc,
				prometheus.GaugeValue,
				value,
				memoryLabels...,
			)
		} else if strings.HasPrefix(metricName, "memory_temp_") {
			memoryID := strings.TrimPrefix(metricName, "memory_temp_")
			memoryLabels := []string{systemID, gpuID, memoryID}

			ch <- prometheus.MustNewConstMetric(
				t.metrics["telemetry_gpu_memory_temperature_celsius"].desc,
				prometheus.GaugeValue,
				value,
				memoryLabels...,
			)
		}
	}
}

// emitPlatformCPUMetrics emits CPU environment metrics
func (t *TelemetryCollector) emitPlatformCPUMetrics(ch chan<- prometheus.Metric, systemID, cpuID string, metrics map[string]float64) {
	labels := []string{systemID, cpuID}

	if val, ok := metrics["energy"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_energy_joules_total"].desc,
			prometheus.CounterValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["power"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_power_watts"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["vreg_cpu_power"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_vreg_cpu_power_watts"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["vreg_soc_power"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_vreg_soc_power_watts"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["temp_avg"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_temperature_average_celsius"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["temp_limit"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_temperature_limit_celsius"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["edp_current"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_edp_current_limit_watts"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["edp_peak"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_cpu_edp_peak_limit_watts"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
}

// emitPlatformAmbientMetrics emits ambient environment metrics
func (t *TelemetryCollector) emitPlatformAmbientMetrics(ch chan<- prometheus.Metric, systemID, locationID, sensorID string, metrics map[string]float64) {
	labels := []string{systemID, locationID, sensorID}

	if val, ok := metrics["inlet_temp"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_ambient_inlet_temperature_celsius"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}

	if val, ok := metrics["exhaust_temp"]; ok {
		ch <- prometheus.MustNewConstMetric(
			t.metrics["telemetry_ambient_exhaust_temperature_celsius"].desc,
			prometheus.GaugeValue,
			val,
			labels...,
		)
	}
}
