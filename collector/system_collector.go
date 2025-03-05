package collector

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/redfish"
)

// SystemSubsystem is the system subsystem
var (
	SystemSubsystem                   = "system"
	SystemLabelNames                  = []string{"odataid", "hostname", "resource", "system_id"}
	SystemMemoryLabelNames            = []string{"odataid", "hostname", "resource", "memory", "memory_id"}
	SystemProcessorLabelNames         = []string{"odataid", "hostname", "resource", "processor", "processor_id"}
	SystemVolumeLabelNames            = []string{"odataid", "hostname", "resource", "volume", "volume_id"}
	SystemDriveLabelNames             = []string{"odataid", "hostname", "resource", "drive", "drive_id"}
	SystemStorageControllerLabelNames = []string{"odataid", "hostname", "resource", "storage_controller", "storage_controller_id"}
	SystemPCIeDeviceLabelNames        = []string{"odataid", "hostname", "resource", "pcie_device", "pcie_device_id", "pcie_device_partnumber", "pcie_device_type", "pcie_serial_number"}
	SystemNetworkInterfaceLabelNames  = []string{"odataid", "hostname", "resource", "network_interface", "network_interface_id"}
	SystemEthernetInterfaceLabelNames = []string{"odataid", "hostname", "resource", "ethernet_interface", "ethernet_interface_id", "ethernet_interface_speed"}
	SystemPCIeFunctionLabelNames      = []string{"odataid", "hostname", "resource", "pcie_function_name", "pcie_function_id", "pci_function_deviceclass", "pci_function_type"}

	SystemLogServiceLabelNames = []string{"system_id", "log_service", "log_service_id", "log_service_enabled", "log_service_overwrite_policy"}

	systemMetrics = createSystemMetricMap()
)

// SystemCollector implements the prometheus.Collector.
type SystemCollector struct {
	redfishClient *gofish.APIClient
	metrics       map[string]Metric
	logger        *slog.Logger
	prometheus.Collector
	collectorScrapeStatus *prometheus.GaugeVec
}

func createSystemMetricMap() map[string]Metric {
	systemMetrics := make(map[string]Metric)

	addToMetricMap(systemMetrics, SystemSubsystem, "state", fmt.Sprintf("system state,%s", CommonStateHelp), SystemLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "health_state", fmt.Sprintf("system health,%s", CommonHealthHelp), SystemLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "power_state", "system power state", SystemLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "total_memory_state", fmt.Sprintf("system overall memory state,%s", CommonStateHelp), SystemLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "total_memory_health_state", fmt.Sprintf("system overall memory health,%s", CommonHealthHelp), SystemLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "total_memory_size", "system total memory size, GiB", SystemLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "total_processor_state", fmt.Sprintf("system overall processor state,%s", CommonStateHelp), SystemLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "total_processor_health_state", fmt.Sprintf("system overall processor health,%s", CommonHealthHelp), SystemLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "total_processor_count", "system total processor count", SystemLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "memory_state", fmt.Sprintf("system memory state,%s", CommonStateHelp), SystemMemoryLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "memory_health_state", fmt.Sprintf("system memory health state,%s", CommonHealthHelp), SystemMemoryLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "memory_capacity", "system memory capacity, MiB", SystemMemoryLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "processor_state", fmt.Sprintf("system processor state,%s", CommonStateHelp), SystemProcessorLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "processor_health_state", fmt.Sprintf("system processor health state,%s", CommonHealthHelp), SystemProcessorLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "processor_total_threads", "system processor total threads", SystemProcessorLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "processor_total_cores", "system processor total cores", SystemProcessorLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "storage_volume_state", fmt.Sprintf("system storage volume state,%s", CommonStateHelp), SystemVolumeLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "storage_volume_health_state", fmt.Sprintf("system storage volume health state,%s", CommonHealthHelp), SystemVolumeLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "storage_volume_capacity", "system storage volume capacity, Bytes", SystemVolumeLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "storage_drive_state", fmt.Sprintf("system storage drive state,%s", CommonStateHelp), SystemDriveLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "storage_drive_health_state", fmt.Sprintf("system storage drive health state,%s", CommonHealthHelp), SystemDriveLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "storage_drive_capacity", "system storage drive capacity, Bytes", SystemDriveLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "storage_controller_state", fmt.Sprintf("system storage controller state,%s", CommonStateHelp), SystemStorageControllerLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "storage_controller_health_state", fmt.Sprintf("system storage controller health state,%s", CommonHealthHelp), SystemStorageControllerLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "pcie_device_state", fmt.Sprintf("system pcie device state,%s", CommonStateHelp), SystemPCIeDeviceLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "pcie_device_health_state", fmt.Sprintf("system pcie device health state,%s", CommonHealthHelp), SystemPCIeDeviceLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "pcie_function_state", fmt.Sprintf("system pcie function state,%s", CommonStateHelp), SystemPCIeFunctionLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "pcie_function_health_state", fmt.Sprintf("system pcie device function state,%s", CommonHealthHelp), SystemPCIeFunctionLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "network_interface_state", fmt.Sprintf("system network interface state,%s", CommonStateHelp), SystemNetworkInterfaceLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "network_interface_health_state", fmt.Sprintf("system network interface health state,%s", CommonHealthHelp), SystemNetworkInterfaceLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "ethernet_interface_state", fmt.Sprintf("system ethernet interface state,%s", CommonStateHelp), SystemEthernetInterfaceLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "ethernet_interface_health_state", fmt.Sprintf("system ethernet interface health state,%s", CommonHealthHelp), SystemEthernetInterfaceLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "ethernet_interface_link_status", fmt.Sprintf("system ethernet interface link status,%s", CommonLinkHelp), SystemEthernetInterfaceLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "ethernet_interface_link_enabled", "system ethernet interface if the link is enabled", SystemEthernetInterfaceLabelNames)

	addToMetricMap(systemMetrics, SystemSubsystem, "log_service_state", fmt.Sprintf("system log service state,%s", CommonStateHelp), SystemLogServiceLabelNames)
	addToMetricMap(systemMetrics, SystemSubsystem, "log_service_health_state", fmt.Sprintf("system log service health state,%s", CommonHealthHelp), SystemLogServiceLabelNames)

	return systemMetrics
}

// NewSystemCollector returns a collector that collecting memory statistics
func NewSystemCollector(redfishClient *gofish.APIClient, logger *slog.Logger) *SystemCollector {
	return &SystemCollector{
		redfishClient: redfishClient,
		metrics:       systemMetrics,
		logger:        logger,
		collectorScrapeStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "collector_scrape_status",
				Help:      "collector_scrape_status",
			},
			[]string{"collector"},
		),
	}
}

// Describe implements prometheus.Collector.
func (s *SystemCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range s.metrics {
		ch <- metric.desc
	}
	s.collectorScrapeStatus.Describe(ch)
}

// Collect implements prometheus.Collector.
func (s *SystemCollector) Collect(ch chan<- prometheus.Metric) {

	logger := s.logger.With(slog.String("collector", "SystemCollector"))
	service := s.redfishClient.Service

	// get a list of systems from service
	if systems, err := service.Systems(); err != nil {
		logger.Error("error getting systems from service", slog.String("operation", "service.Systems()"), slog.Any("error", err))
	} else {
		for _, system := range systems {
			systemLogger := logger.With(slog.String("System", system.ID))
			systemLogger.Info("collector scrape started")

			// overall system metrics
			systemODataID := system.ODataID
			SystemID := system.ID
			systemHostName := system.HostName
			systemPowerState := system.PowerState
			systemState := system.Status.State
			systemHealthState := system.Status.Health
			systemTotalProcessorCount := system.ProcessorSummary.Count
			systemTotalProcessorsState := system.ProcessorSummary.Status.State
			systemTotalProcessorsHealthState := system.ProcessorSummary.Status.Health
			systemTotalMemoryState := system.MemorySummary.Status.State
			systemTotalMemoryHealthState := system.MemorySummary.Status.Health
			systemTotalMemoryAmount := system.MemorySummary.TotalSystemMemoryGiB

			systemLabelValues := []string{systemODataID, systemHostName, "system", SystemID}
			if systemHealthStateValue, ok := parseCommonStatusHealth(systemHealthState); ok {
				ch <- prometheus.MustNewConstMetric(s.metrics["system_health_state"].desc, prometheus.GaugeValue, systemHealthStateValue, systemLabelValues...)
			}
			if systemStateValue, ok := parseCommonStatusState(systemState); ok {
				ch <- prometheus.MustNewConstMetric(s.metrics["system_state"].desc, prometheus.GaugeValue, systemStateValue, systemLabelValues...)
			}
			if systemPowerStateValue, ok := parseCommonPowerState(systemPowerState); ok {
				ch <- prometheus.MustNewConstMetric(s.metrics["system_power_state"].desc, prometheus.GaugeValue, systemPowerStateValue, systemLabelValues...)
			}
			if systemTotalProcessorsStateValue, ok := parseCommonStatusState(systemTotalProcessorsState); ok {
				ch <- prometheus.MustNewConstMetric(s.metrics["system_total_processor_state"].desc, prometheus.GaugeValue, systemTotalProcessorsStateValue, systemLabelValues...)
				ch <- prometheus.MustNewConstMetric(s.metrics["system_total_processor_count"].desc, prometheus.GaugeValue, float64(systemTotalProcessorCount), systemLabelValues...)
			}
			if systemTotalProcessorsHealthStateValue, ok := parseCommonStatusHealth(systemTotalProcessorsHealthState); ok {
				ch <- prometheus.MustNewConstMetric(s.metrics["system_total_processor_health_state"].desc, prometheus.GaugeValue, systemTotalProcessorsHealthStateValue, systemLabelValues...)
			}
			if systemTotalMemoryStateValue, ok := parseCommonStatusState(systemTotalMemoryState); ok {
				ch <- prometheus.MustNewConstMetric(s.metrics["system_total_memory_state"].desc, prometheus.GaugeValue, systemTotalMemoryStateValue, systemLabelValues...)
				ch <- prometheus.MustNewConstMetric(s.metrics["system_total_memory_size"].desc, prometheus.GaugeValue, float64(systemTotalMemoryAmount), systemLabelValues...)
			}
			if systemTotalMemoryHealthStateValue, ok := parseCommonStatusHealth(systemTotalMemoryHealthState); ok {
				ch <- prometheus.MustNewConstMetric(s.metrics["system_total_memory_health_state"].desc, prometheus.GaugeValue, systemTotalMemoryHealthStateValue, systemLabelValues...)
			}

			wg1 := &sync.WaitGroup{}
			wg2 := &sync.WaitGroup{}
			wg3 := &sync.WaitGroup{}
			wg4 := &sync.WaitGroup{}
			wg5 := &sync.WaitGroup{}
			wg6 := &sync.WaitGroup{}
			wg7 := &sync.WaitGroup{}
			wg8 := &sync.WaitGroup{}
			wg9 := &sync.WaitGroup{}
			wg10 := &sync.WaitGroup{}

			// process memory metrics
			memories, err := system.Memory()
			if err != nil {
				systemLogger.Error("error getting memory data from system", slog.String("operation", "system.Memory()"), slog.Any("error", err))
			} else if memories == nil {
				systemLogger.Info("no memory data found", slog.String("operation", "system.Memory()"))
			} else {
				wg1.Add(len(memories))
				for _, memory := range memories {
					go parseMemory(ch, systemHostName, memory, wg1)
				}
			}

			// process processor metrics
			processors, err := system.Processors()
			if err != nil {
				systemLogger.Error("error getting processor data from system", slog.String("operation", "system.Processors()"), slog.Any("error", err))
			} else if processors == nil {
				systemLogger.Info("no processor data found", slog.String("operation", "system.Processors()"))
			} else {
				wg2.Add(len(processors))
				for _, processor := range processors {
					go parseProcessor(ch, systemHostName, processor, wg2)
				}
			}

			// process storage
			storages, err := system.Storage()
			if err != nil {
				systemLogger.Error("error getting storage data from system", slog.String("operation", "system.Storage()"), slog.Any("eeror", err))
			} else if storages == nil {
				systemLogger.Info("no storage data found", slog.String("operation", "system.Storage()"))
			} else {
				for _, storage := range storages {
					if volumes, err := storage.Volumes(); err != nil {
						systemLogger.Error("error getting storage data from system", slog.String("operation", "system.Volumes()"), slog.Any("wrror", err))
					} else {
						wg3.Add(len(volumes))

						for _, volume := range volumes {
							go parseVolume(ch, systemHostName, volume, wg3)
						}
					}

					drives, err := storage.Drives()
					if err != nil {
						systemLogger.Error("error getting drive data from system", slog.String("operation", "system.Drives()"), slog.Any("error", err))
					} else if drives == nil {
						systemLogger.Info("no drive data found", slog.String("operation", "system.Drives()"), slog.String("storage", storage.ID))
					} else {
						wg4.Add(len(drives))
						for _, drive := range drives {
							go parseDrive(ch, systemHostName, drive, wg4)
						}
					}
				}
			}
			// process pci devices
			pcieDevices, err := system.PCIeDevices()
			if err != nil {
				systemLogger.Error("error getting PCI-E device data from system", slog.String("operation", "system.PCIeDevices()"), slog.Any("error", err))
			} else if pcieDevices == nil {
				systemLogger.Info("no PCI-E device data found", slog.String("operation", "system.PCIeDevices()"))
			} else {
				wg5.Add(len(pcieDevices))
				for _, pcieDevice := range pcieDevices {
					go parsePcieDevice(ch, systemHostName, pcieDevice, wg5)
				}
			}

			// process networkinterfaces
			networkInterfaces, err := system.NetworkInterfaces()
			if err != nil {
				systemLogger.Error("error getting network interface data from system", slog.String("operation", "system.NetworkInterfaces()"), slog.Any("error", err))
			} else if networkInterfaces == nil {
				systemLogger.Info("no network interface data found", slog.String("operation", "system.NetworkInterfaces"))
			} else {
				wg6.Add(len(networkInterfaces))
				for _, networkInterface := range networkInterfaces {
					go parseNetworkInterface(ch, systemHostName, networkInterface, wg6)
				}
			}

			// process ethernetinterfaces
			ethernetInterfaces, err := system.EthernetInterfaces()
			if err != nil {
				systemLogger.Error("error getting ethernet interface data from system", slog.String("operation", "system.EthernetInterfaces()"), slog.Any("error", err))
			} else if ethernetInterfaces == nil {
				systemLogger.Info("no ethernet interface data found", slog.String("operation", "system.PCIeDevices()"))
			} else {
				wg7.Add(len(ethernetInterfaces))
				for _, ethernetInterface := range ethernetInterfaces {
					go parseEthernetInterface(ch, systemHostName, ethernetInterface, wg7)
				}
			}

			// process pci functions
			pcieFunctions, err := system.PCIeFunctions()
			if err != nil {
				systemLogger.Error("error getting PCI-E device function data from system", slog.String("operation", "system.PCIeFunctions()"), slog.Any("error", err))
			} else if pcieFunctions == nil {
				systemLogger.Info("no PCI-E device function data found", slog.String("operation", "system.PCIeFunctions()"))
			} else {
				wg9.Add(len(pcieFunctions))
				for _, pcieFunction := range pcieFunctions {
					go parsePcieFunction(ch, systemHostName, pcieFunction, wg9)
				}
			}

			// process log services
			logServices, err := system.LogServices()
			if err != nil {
				systemLogger.Error("error getting log services from system", slog.String("operation", "system.LogServices()"), slog.Any("error", err))
			} else if logServices == nil {
				systemLogger.Info("no log services found", slog.String("operation", "system.LogServices()"))
			} else {
				wg10.Add(len(logServices))

				for _, logService := range logServices {
					if err = parseLogService(ch, systemMetrics, SystemSubsystem, SystemID, logService, wg10); err != nil {
						systemLogger.Error("error getting log entries from log service", slog.String("operation", "system.LogServices()"), slog.Any("error", err))
					}
				}
			}

			wg1.Wait()
			wg2.Wait()
			wg3.Wait()
			wg4.Wait()
			wg5.Wait()
			wg6.Wait()
			wg7.Wait()
			wg8.Wait()
			wg9.Wait()
			wg10.Wait()

			systemLogger.Info("collector scrape completed")
		}
		s.collectorScrapeStatus.WithLabelValues("system").Set(float64(1))
	}
}

func parseMemory(ch chan<- prometheus.Metric, systemHostName string, memory *redfish.Memory, wg *sync.WaitGroup) {
	defer wg.Done()
	memoryODataID := memory.ODataID
	memoryName := memory.Name
	memoryID := memory.ID
	memoryCapacityMiB := memory.CapacityMiB
	memoryState := memory.Status.State
	memoryHealthState := memory.Status.Health

	systemMemoryLabelValues := []string{memoryODataID, systemHostName, "memory", memoryName, memoryID}
	if memoryStateValue, ok := parseCommonStatusState(memoryState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_memory_state"].desc, prometheus.GaugeValue, memoryStateValue, systemMemoryLabelValues...)
	}
	if memoryHealthStateValue, ok := parseCommonStatusHealth(memoryHealthState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_memory_health_state"].desc, prometheus.GaugeValue, memoryHealthStateValue, systemMemoryLabelValues...)
	}
	ch <- prometheus.MustNewConstMetric(systemMetrics["system_memory_capacity"].desc, prometheus.GaugeValue, float64(memoryCapacityMiB), systemMemoryLabelValues...)

}

func parseProcessor(ch chan<- prometheus.Metric, systemHostName string, processor *redfish.Processor, wg *sync.WaitGroup) {
	defer wg.Done()
	processorODataID := processor.ODataID
	processorName := processor.Name
	processorID := processor.ID
	processorTotalCores := processor.TotalCores
	processorTotalThreads := processor.TotalThreads
	processorState := processor.Status.State
	processorHelathState := processor.Status.Health

	systemProcessorLabelValues := []string{processorODataID, systemHostName, "processor", processorName, processorID}

	if processorStateValue, ok := parseCommonStatusState(processorState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_processor_state"].desc, prometheus.GaugeValue, processorStateValue, systemProcessorLabelValues...)
	}
	if processorHelathStateValue, ok := parseCommonStatusHealth(processorHelathState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_processor_health_state"].desc, prometheus.GaugeValue, processorHelathStateValue, systemProcessorLabelValues...)
	}
	ch <- prometheus.MustNewConstMetric(systemMetrics["system_processor_total_threads"].desc, prometheus.GaugeValue, float64(processorTotalThreads), systemProcessorLabelValues...)
	ch <- prometheus.MustNewConstMetric(systemMetrics["system_processor_total_cores"].desc, prometheus.GaugeValue, float64(processorTotalCores), systemProcessorLabelValues...)
}

func parseVolume(ch chan<- prometheus.Metric, systemHostName string, volume *redfish.Volume, wg *sync.WaitGroup) {
	defer wg.Done()
	volumeODataID := volume.ODataID
	volumeName := volume.Name
	volumeID := volume.ID
	volumeCapacityBytes := volume.CapacityBytes
	volumeState := volume.Status.State
	volumeHealthState := volume.Status.Health
	systemVolumeLabelValues := []string{volumeODataID, systemHostName, "volume", volumeName, volumeID}
	if volumeStateValue, ok := parseCommonStatusState(volumeState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_storage_volume_state"].desc, prometheus.GaugeValue, volumeStateValue, systemVolumeLabelValues...)
	}
	if volumeHealthStateValue, ok := parseCommonStatusHealth(volumeHealthState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_storage_volume_health_state"].desc, prometheus.GaugeValue, volumeHealthStateValue, systemVolumeLabelValues...)
	}
	ch <- prometheus.MustNewConstMetric(systemMetrics["system_storage_volume_capacity"].desc, prometheus.GaugeValue, float64(volumeCapacityBytes), systemVolumeLabelValues...)
}

func parseDrive(ch chan<- prometheus.Metric, systemHostName string, drive *redfish.Drive, wg *sync.WaitGroup) {
	defer wg.Done()
	driveODataID := drive.ODataID
	driveName := drive.Name
	driveID := drive.ID
	driveCapacityBytes := drive.CapacityBytes
	driveState := drive.Status.State
	driveHealthState := drive.Status.Health
	systemdriveLabelValues := []string{driveODataID, systemHostName, "drive", driveName, driveID}
	if driveStateValue, ok := parseCommonStatusState(driveState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_storage_drive_state"].desc, prometheus.GaugeValue, driveStateValue, systemdriveLabelValues...)
	}
	if driveHealthStateValue, ok := parseCommonStatusHealth(driveHealthState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_storage_drive_health_state"].desc, prometheus.GaugeValue, driveHealthStateValue, systemdriveLabelValues...)
	}
	ch <- prometheus.MustNewConstMetric(systemMetrics["system_storage_drive_capacity"].desc, prometheus.GaugeValue, float64(driveCapacityBytes), systemdriveLabelValues...)
}

func parsePcieDevice(ch chan<- prometheus.Metric, systemHostName string, pcieDevice *redfish.PCIeDevice, wg *sync.WaitGroup) {
	defer wg.Done()
	pcieDeviceODataID := pcieDevice.ODataID
	pcieDeviceName := pcieDevice.Name
	pcieDeviceID := pcieDevice.ID
	pcieDeviceState := pcieDevice.Status.State
	pcieDeviceHealthState := pcieDevice.Status.Health
	pcieDevicePartNumber := pcieDevice.PartNumber
	pcieDeviceType := fmt.Sprint(pcieDevice.DeviceType)
	pcieSerialNumber := pcieDevice.SerialNumber
	systemPCIeDeviceLabelValues := []string{pcieDeviceODataID, systemHostName, "pcie_device", pcieDeviceName, pcieDeviceID, pcieDevicePartNumber, pcieDeviceType, pcieSerialNumber}

	if pcieStateVaule, ok := parseCommonStatusState(pcieDeviceState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_pcie_device_state"].desc, prometheus.GaugeValue, pcieStateVaule, systemPCIeDeviceLabelValues...)
	}
	if pcieHealthStateVaule, ok := parseCommonStatusHealth(pcieDeviceHealthState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_pcie_device_health_state"].desc, prometheus.GaugeValue, pcieHealthStateVaule, systemPCIeDeviceLabelValues...)
	}
}

func parseNetworkInterface(ch chan<- prometheus.Metric, systemHostName string, networkInterface *redfish.NetworkInterface, wg *sync.WaitGroup) {
	defer wg.Done()
	networkInterfaceODataID := networkInterface.ODataID
	networkInterfaceName := networkInterface.Name
	networkInterfaceID := networkInterface.ID
	networkInterfaceState := networkInterface.Status.State
	networkInterfaceHealthState := networkInterface.Status.Health
	systemNetworkInterfaceLabelValues := []string{networkInterfaceODataID, systemHostName, "network_interface", networkInterfaceName, networkInterfaceID}

	if networknetworkInterfaceStateVaule, ok := parseCommonStatusState(networkInterfaceState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_network_interface_state"].desc, prometheus.GaugeValue, networknetworkInterfaceStateVaule, systemNetworkInterfaceLabelValues...)
	}
	if networknetworkInterfaceHealthStateVaule, ok := parseCommonStatusHealth(networkInterfaceHealthState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_network_interface_health_state"].desc, prometheus.GaugeValue, networknetworkInterfaceHealthStateVaule, systemNetworkInterfaceLabelValues...)
	}
}

func parseEthernetInterface(ch chan<- prometheus.Metric, systemHostName string, ethernetInterface *redfish.EthernetInterface, wg *sync.WaitGroup) {
	defer wg.Done()

	ethernetInterfaceODataID := ethernetInterface.ODataID
	ethernetInterfaceName := ethernetInterface.Name
	ethernetInterfaceID := ethernetInterface.ID
	ethernetInterfaceLinkStatus := ethernetInterface.LinkStatus
	ethernetInterfaceEnabled := ethernetInterface.InterfaceEnabled
	ethernetInterfaceSpeed := fmt.Sprintf("%d Mbps", ethernetInterface.SpeedMbps)
	ethernetInterfaceState := ethernetInterface.Status.State
	ethernetInterfaceHealthState := ethernetInterface.Status.Health
	systemEthernetInterfaceLabelValues := []string{ethernetInterfaceODataID, systemHostName, "ethernet_interface", ethernetInterfaceName, ethernetInterfaceID, ethernetInterfaceSpeed}
	if ethernetInterfaceStateValue, ok := parseCommonStatusState(ethernetInterfaceState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_ethernet_interface_state"].desc, prometheus.GaugeValue, ethernetInterfaceStateValue, systemEthernetInterfaceLabelValues...)
	}
	if ethernetInterfaceHealthStateValue, ok := parseCommonStatusHealth(ethernetInterfaceHealthState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_ethernet_interface_health_state"].desc, prometheus.GaugeValue, ethernetInterfaceHealthStateValue, systemEthernetInterfaceLabelValues...)
	}
	if ethernetInterfaceLinkStatusValue, ok := parseLinkStatus(ethernetInterfaceLinkStatus); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_ethernet_interface_link_status"].desc, prometheus.GaugeValue, ethernetInterfaceLinkStatusValue, systemEthernetInterfaceLabelValues...)
	}

	ch <- prometheus.MustNewConstMetric(systemMetrics["system_ethernet_interface_link_enabled"].desc, prometheus.GaugeValue, boolToFloat64(ethernetInterfaceEnabled), systemEthernetInterfaceLabelValues...)
}

func parsePcieFunction(ch chan<- prometheus.Metric, systemHostName string, pcieFunction *redfish.PCIeFunction, wg *sync.WaitGroup) {
	defer wg.Done()

	pcieFunctionODataID := pcieFunction.ODataID
	pcieFunctionName := pcieFunction.Name
	pcieFunctionID := fmt.Sprint(pcieFunction.ID)
	pciFunctionDeviceclass := fmt.Sprint(pcieFunction.DeviceClass)
	pciFunctionType := fmt.Sprint(pcieFunction.FunctionType)
	pciFunctionState := pcieFunction.Status.State
	pciFunctionHealthState := pcieFunction.Status.Health

	systemPCIeFunctionLabelLabelValues := []string{pcieFunctionODataID, systemHostName, "pcie_function", pcieFunctionName, pcieFunctionID, pciFunctionDeviceclass, pciFunctionType}

	if pciFunctionStateValue, ok := parseCommonStatusState(pciFunctionState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_pcie_function_state"].desc, prometheus.GaugeValue, pciFunctionStateValue, systemPCIeFunctionLabelLabelValues...)
	}

	if pciFunctionHealthStateValue, ok := parseCommonStatusHealth(pciFunctionHealthState); ok {
		ch <- prometheus.MustNewConstMetric(systemMetrics["system_pcie_function_health_state"].desc, prometheus.GaugeValue, pciFunctionHealthStateValue, systemPCIeFunctionLabelLabelValues...)
	}
}
