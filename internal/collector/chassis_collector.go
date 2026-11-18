package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"path"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

// ChassisSubsystem is the chassis subsystem
var (
	ChassisSubsystem             = "chassis"
	ChassisLabelNames            = []string{"resource", "chassis_id"}
	ChassisModel                 = []string{"resource", "chassis_id", "manufacturer", "model", "part_number", "sku"}
	ChassisTemperatureLabelNames = []string{"resource", "chassis_id", "sensor", "sensor_id"}
	ChassisFanLabelNames         = []string{"resource", "chassis_id", "fan", "fan_id", "fan_unit"}

	ChassisThermalSubsystemFanLabelNames         = []string{"resource", "chassis_id", "fan", "fan_id"}
	ChassisThermalSubsystemTemperatureLabelNames = []string{"resource", "chassis_id", "sensor", "sensor_id"}

	ChassisPowerVoltageLabelNames     = []string{"resource", "chassis_id", "power_voltage", "power_voltage_id"}
	ChassisPowerSupplyLabelNames      = []string{"resource", "chassis_id", "power_supply", "power_supply_id"}
	ChassisNetworkAdapterLabelNames   = []string{"resource", "chassis_id", "network_adapter", "network_adapter_id"}
	ChassisNetworkPortLabelNames      = []string{"resource", "chassis_id", "network_adapter", "network_adapter_id", "network_port", "network_port_id", "network_port_type", "network_port_speed", "network_port_connectiont_type", "network_physical_port_number"}
	ChassisPhysicalSecurityLabelNames = []string{"resource", "chassis_id", "intrusion_sensor_number", "intrusion_sensor_rearm"}
	ChassisLeakDetectorLabelNames     = []string{"resource", "chassis_id", "leak_detection_id", "leak_detector_id"}

	ChassisLogServiceLabelNames = []string{"chassis_id", "log_service", "log_service_id", "log_service_enabled", "log_service_overwrite_policy"}

	chassisMetrics = createChassisMetricMap()
)

// ChassisCollector implements the prometheus.Collector.
type ChassisCollector struct {
	redfishClient         *gofish.APIClient
	config                config.ChassisCollectorConfig
	metrics               map[string]Metric
	logger                *slog.Logger
	collectorScrapeStatus *prometheus.GaugeVec
	// power is the platform's chassis power read; see Platform.
	power func(*ChassisCollector, chan<- prometheus.Metric, *schemas.Chassis, *slog.Logger)
	// sensors is the platform's chassis Sensors read; see Platform.
	sensors func(*ChassisCollector, chan<- prometheus.Metric, *schemas.Chassis, *slog.Logger)
	// leakDetection is the platform's ThermalSubsystem leak detector read; see Platform.
	leakDetection func(*ChassisCollector, *schemas.ThermalSubsystem, *slog.Logger) []*schemas.LeakDetector
	// temperatures is the platform's ThermalSubsystem temperature read; see Platform.
	temperatures func(*ChassisCollector, chan<- prometheus.Metric, string, *schemas.ThermalSubsystem, *slog.Logger)
	// chassisList is the platform's read of the chassis to walk; see Platform.
	chassisList func(*ChassisCollector) ([]*schemas.Chassis, error)
	// written keys the series cells wrote through writeOnce. A collector serves one scrape.
	written sync.Map
}

// writeOnce reports whether this scrape has not yet written key, and records it. A cell that
// can meet the same series in the reads of two chassis writes it only when writeOnce is true,
// since a repeated series fails the scrape.
func (c *ChassisCollector) writeOnce(key string) bool {
	_, seen := c.written.LoadOrStore(key, true)
	return !seen
}

func createChassisMetricMap() map[string]Metric {
	chassisMetrics := make(map[string]Metric)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "health", fmt.Sprintf("health of chassis,%s", CommonHealthHelp), ChassisLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "health_rollup", fmt.Sprintf("health rollup of chassis,%s", CommonHealthHelp), ChassisLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "state", fmt.Sprintf("state of chassis,%s", CommonStateHelp), ChassisLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "model_info", "organization responsible for producing the chassis, the name by which the manufacturer generally refers to the chassis, and a part number and sku assigned by the organization that is responsible for producing or manufacturing the chassis", ChassisModel)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "temperature_sensor_state", fmt.Sprintf("status state of temperature on this chassis component,%s", CommonStateHelp), ChassisTemperatureLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "temperature_sensor_health", fmt.Sprintf("status health of temperature on this chassis component,%s", CommonHealthHelp), ChassisTemperatureLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "temperature_celsius", "celsius of temperature on this chassis component", ChassisTemperatureLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_health", fmt.Sprintf("fan health on this chassis component,%s", CommonHealthHelp), ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_state", fmt.Sprintf("fan state on this chassis component,%s", CommonStateHelp), ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm", "fan RPM or percentage on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_percentage", "fan RPM, as a percentage of the min-max RPMs possible, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_min", "lowest possible fan RPM or percentage, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_max", "highest possible fan RPM or percentage, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_lower_threshold_critical", "threshold below the normal range fan RPM or percentage, but not fatal, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_lower_threshold_non_critical", "threshold below the normal range fan RPM or percentage, but not critical, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_lower_threshold_fatal", "threshold below the normal range fan RPM or percentage, and is fatal, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_upper_threshold_critical", "threshold above the normal range fan RPM or percentage, but not fatal, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_upper_threshold_non_critical", "threshold above the normal range fan RPM or percentage, but not critical, on this chassis component", ChassisFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "fan_rpm_upper_threshold_fatal", "threshold above the normal range fan RPM or percentage, and is fatal, on this chassis component", ChassisFanLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_health", fmt.Sprintf("health of the chassis ThermalSubsystem,%s", CommonHealthHelp), ChassisLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_health_rollup", fmt.Sprintf("health rollup of the chassis ThermalSubsystem,%s", CommonHealthHelp), ChassisLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_state", fmt.Sprintf("state of the chassis ThermalSubsystem,%s", CommonStateHelp), ChassisLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_fan_health", fmt.Sprintf("fan health reported by the chassis ThermalSubsystem,%s", CommonHealthHelp), ChassisThermalSubsystemFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_fan_state", fmt.Sprintf("fan state reported by the chassis ThermalSubsystem,%s", CommonStateHelp), ChassisThermalSubsystemFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_fan_rpm", "fan rotational speed in RPM reported by the chassis ThermalSubsystem", ChassisThermalSubsystemFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_fan_speed_percentage", "fan speed as a percentage of its rated speed, reported by the chassis ThermalSubsystem", ChassisThermalSubsystemFanLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_fan_rpm_max", "rated maximum fan rotational speed in RPM, reported by the chassis ThermalSubsystem", ChassisThermalSubsystemFanLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "thermal_subsystem_temperature_celsius", "celsius of temperature reported by the chassis ThermalSubsystem metrics", ChassisThermalSubsystemTemperatureLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_voltage_state", fmt.Sprintf("power voltage state of chassis component,%s", CommonStateHelp), ChassisPowerVoltageLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_voltage_volts", "power voltage volts number of chassis component", ChassisPowerVoltageLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_average_consumed_watts", "power wattage watts number of chassis component", ChassisPowerVoltageLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_powersupply_state", fmt.Sprintf("powersupply state of chassis component,%s", CommonStateHelp), ChassisPowerSupplyLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_powersupply_health", fmt.Sprintf("powersupply health of chassis component,%s", CommonHealthHelp), ChassisPowerSupplyLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_powersupply_power_efficiency_percentage", "rated efficiency, as a percentage, of the associated power supply on this chassis", ChassisPowerSupplyLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_powersupply_last_power_output_watts", "average power output, measured in Watts, of the associated power supply on this chassis", ChassisPowerSupplyLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_powersupply_power_input_watts", "measured input power, in Watts, of powersupply on this chassis", ChassisPowerSupplyLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_powersupply_power_output_watts", "measured output power, in Watts, of powersupply on this chassis", ChassisPowerSupplyLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "power_powersupply_power_capacity_watts", "power_capacity_watts of powersupply on this chassis", ChassisPowerSupplyLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "network_adapter_state", fmt.Sprintf("chassis network adapter state,%s", CommonStateHelp), ChassisNetworkAdapterLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "network_adapter_health_state", fmt.Sprintf("chassis network adapter health state,%s", CommonHealthHelp), ChassisNetworkAdapterLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "network_port_state", fmt.Sprintf("chassis network port state,%s", CommonStateHelp), ChassisNetworkPortLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "network_port_health_state", fmt.Sprintf("chassis network port health state,%s", CommonHealthHelp), ChassisNetworkPortLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "network_port_link_state", fmt.Sprintf("chassis network port link state state,%s", CommonPortLinkHelp), ChassisNetworkPortLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "physical_security_sensor_state", fmt.Sprintf("indicates the known state of the physical security sensor, such as if it is hardware intrusion detected,%s", CommonIntrusionSensorHelp), ChassisPhysicalSecurityLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "log_service_state", fmt.Sprintf("chassis log service state,%s", CommonStateHelp), ChassisLogServiceLabelNames)
	addToMetricMap(chassisMetrics, ChassisSubsystem, "log_service_health_state", fmt.Sprintf("chassis log service health state,%s", CommonHealthHelp), ChassisLogServiceLabelNames)

	addToMetricMap(chassisMetrics, ChassisSubsystem, "leak_detector_health", fmt.Sprintf("chassis leak detector health state,%s", CommonHealthHelp), ChassisLeakDetectorLabelNames)

	// Note: chassis_gpu_total_power_watts is now collected via TelemetryService (HGX_PlatformEnvironmentMetrics_0)

	return chassisMetrics
}

// NewChassisCollector returns a collector that collecting chassis statistics
func NewChassisCollector(collectorName string, redfishClient *gofish.APIClient, logger *slog.Logger, config config.ChassisCollectorConfig) (*ChassisCollector, error) {
	// get service from redfish client

	return &ChassisCollector{
		redfishClient: redfishClient,
		metrics:       chassisMetrics,
		config:        config,
		logger:        logger,
		collectorScrapeStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "collector_scrape_status",
				Help:      "collector_scrape_status",
			},
			[]string{"collector"},
		),
		power:         Default.ChassisPower,
		sensors:       Default.ChassisSensors,
		leakDetection: Default.ChassisLeakDetection,
		temperatures:  Default.ChassisTemperatures,
		chassisList:   Default.ChassisList,
	}, nil
}

// SetPlatform takes the cells this collector routes on.
func (c *ChassisCollector) SetPlatform(p Platform) {
	c.power = p.ChassisPower
	c.sensors = p.ChassisSensors
	c.leakDetection = p.ChassisLeakDetection
	c.temperatures = p.ChassisTemperatures
	c.chassisList = p.ChassisList
}

// listChassis reads every chassis the service lists.
func (c *ChassisCollector) listChassis() ([]*schemas.Chassis, error) {
	return c.redfishClient.Service.Chassis()
}

func (c *ChassisCollector) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) {
	c.collect(ctx, ch)
}

// Collect implemented prometheus.Collector
func (c *ChassisCollector) Collect(ch chan<- prometheus.Metric) {
	c.collect(context.TODO(), ch)
}

func (c *ChassisCollector) collect(ctx context.Context, ch chan<- prometheus.Metric) {
	if ctx.Err() != nil {
		c.logger.With("error", ctx.Err(), "collector", "chassis").Debug("skipping collection")
		return
	}
	logger := c.logger.With(slog.String("collector", "ChassisCollector"))

	if ctx.Err() != nil {
		c.logger.With("error", ctx.Err(), "collector", "chassis").Debug("skipping collection")
		return
	}
	// get a list of chassis from service
	chassises, err := c.chassisList(c)
	if err != nil {
		// A collection error means some member failed, not that none of them arrived:
		// gofish still returns the chassis it did fetch. Discarding those made one flaky
		// chassis out of forty silently zero the whole chassis scrape, which reads as a
		// host with no chassis rather than as a host with a problem.
		logger.Error("error getting chassis from service", slog.String("operation", "service.Chassis()"), slog.Any("error", err))
	}

	// process the chassises
	for _, chassis := range chassises {
		if ctx.Err() != nil {
			c.logger.With("error", ctx.Err()).Warn("skipping further collection")
			continue
		}
		chassisLogger := logger.With(slog.String("Chassis", chassis.ID))
		chassisLogger.Info("collector scrape started")
		chassisID := chassis.ID
		chassisStatus := chassis.Status
		chassisStatusState := chassisStatus.State
		chassisStatusHealth := chassisStatus.Health
		chassisStatusHealthRollup := chassisStatus.HealthRollup
		ChassisLabelValues := []string{"chassis", chassisID}
		if chassisStatusHealthValue, ok := parseCommonStatusHealth(chassisStatusHealth); ok {
			ch <- prometheus.MustNewConstMetric(c.metrics["chassis_health"].desc, prometheus.GaugeValue, chassisStatusHealthValue, ChassisLabelValues...)
		}
		if chassisStatusHealthRollupValue, ok := parseCommonStatusHealth(chassisStatusHealthRollup); ok {
			ch <- prometheus.MustNewConstMetric(c.metrics["chassis_health_rollup"].desc, prometheus.GaugeValue, chassisStatusHealthRollupValue, ChassisLabelValues...)
		}
		if chassisStatusStateValue, ok := parseCommonStatusState(chassisStatusState); ok {
			ch <- prometheus.MustNewConstMetric(c.metrics["chassis_state"].desc, prometheus.GaugeValue, chassisStatusStateValue, ChassisLabelValues...)
		}

		chassisManufacturer := chassis.Manufacturer
		chassisModel := chassis.Model
		chassisPartNumber := chassis.PartNumber
		chassisSKU := chassis.SKU
		ChassisModelLabelValues := []string{"chassis", chassisID, chassisManufacturer, chassisModel, chassisPartNumber, chassisSKU}
		ch <- prometheus.MustNewConstMetric(c.metrics["chassis_model_info"].desc, prometheus.GaugeValue, 1, ChassisModelLabelValues...)

		chassisThermal, err := chassis.Thermal()
		if err != nil {
			chassisLogger.Error("error getting thermal data from chassis", slog.String("operation", "chassis.Thermal()"), slog.Any("error", err))
		} else if chassisThermal == nil {
			chassisLogger.Info("no thermal data found", slog.String("operation", "chassis.Thermal()"))
		} else {
			collectThermal(ch, chassisID, chassisThermal)
		}

		chassisThermalSubsystem, err := chassis.ThermalSubsystem()
		if err != nil {
			chassisLogger.Error("error getting thermal subsystem from chassis", slog.String("operation", "chassis.ThermalSubsystem()"), slog.Any("error", err))
		} else if chassisThermalSubsystem == nil {
			chassisLogger.Info("no thermal subsystem found", slog.String("operation", "chassis.ThermalSubsystem()"))
		} else {
			c.collectThermalSubsystem(ctx, ch, chassisID, chassisThermalSubsystem, chassisLogger)
		}

		c.power(c, ch, chassis, chassisLogger)
		c.sensors(c, ch, chassis, chassisLogger)

		// process NetworkAdapter
		networkAdapters, err := chassis.NetworkAdapters()
		if err != nil {
			chassisLogger.Error("error getting network adapters data from chassis", slog.String("operation", "chassis.NetworkAdapters()"), slog.Any("error", err))
		} else if networkAdapters == nil {
			chassisLogger.Info("no network adapters data found", slog.String("operation", "chassis.NetworkAdapters()"))
		} else {
			egNA := newRecoverGroup(ctx)
			for _, networkAdapter := range networkAdapters {
				egNA.Go(func() error {
					return parseNetworkAdapter(ch, chassisID, networkAdapter)
				})
			}
			if err := egNA.Wait(); err != nil {
				chassisLogger.Error("error getting network ports from network adapter", slog.String("operation", "chassis.NetworkAdapters()"), slog.Any("error", err))
			}
		}

		physicalSecurity := chassis.PhysicalSecurity
		if physicalSecurity != (schemas.PhysicalSecurity{}) {
			physicalSecurityIntrusionSensor := physicalSecurity.IntrusionSensor
			physicalSecurityIntrusionSensorNumber := fmt.Sprint(physicalSecurity.IntrusionSensorNumber) //nolint:staticcheck
			physicalSecurityIntrusionSensorReArmMethod := string(physicalSecurity.IntrusionSensorReArm)

			if phySecIntrusionSensor, ok := parsePhySecIntrusionSensor(physicalSecurityIntrusionSensor); ok {
				ChassisPhysicalSecurityLabelValues := []string{"physical_security", chassisID, physicalSecurityIntrusionSensorNumber, physicalSecurityIntrusionSensorReArmMethod}
				ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_physical_security_sensor_state"].desc, prometheus.GaugeValue, phySecIntrusionSensor, ChassisPhysicalSecurityLabelValues...)
			}
		}

		chassisLogger.Info("collector scrape completed")
	}

	c.collectorScrapeStatus.WithLabelValues("chassis").Set(float64(1))
}

// Describe implemented prometheus.Collector
func (c *ChassisCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range c.metrics {
		ch <- metric.desc
	}
	c.collectorScrapeStatus.Describe(ch)

}

// collectThermal emits fan and temperature metrics from the chassis Thermal resource.
//
// Redfish deprecated Thermal in release 2020.4 in favour of ThermalSubsystem -- the Redfish
// Resource and Schema Guide lists it as "Thermal 1.7.0 (deprecated)", see
// https://redfish.dmtf.org/schemas/DSP2046_2020.4.html -- but BMCs still serve it, so both
// are collected. collectThermalSubsystem reads the replacement.
func collectThermal(ch chan<- prometheus.Metric, chassisID string, thermal *schemas.Thermal) {
	for _, chassisTemperature := range thermal.Temperatures {
		parseChassisTemperature(ch, chassisID, chassisTemperature)
	}
	for _, chassisFan := range thermal.Fans {
		parseChassisFan(ch, chassisID, chassisFan)
	}
}

// collectThermalSubsystem emits status, fan, temperature and leak detector metrics from the
// chassis ThermalSubsystem resource, the replacement for the Thermal resource read by
// collectThermal. Fans, ThermalMetrics and LeakDetection are each a separate GET, so they are
// fetched concurrently.
func (c *ChassisCollector) collectThermalSubsystem(ctx context.Context, ch chan<- prometheus.Metric, chassisID string, thermalSubsystem *schemas.ThermalSubsystem, logger *slog.Logger) {
	// Status came with the ThermalSubsystem body the caller already fetched, so it needs no
	// goroutine of its own.
	labelValues := []string{"thermal_subsystem", chassisID}
	if healthValue, ok := parseCommonStatusHealth(thermalSubsystem.Status.Health); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_health"].desc, prometheus.GaugeValue, healthValue, labelValues...)
	}
	if healthRollupValue, ok := parseCommonStatusHealth(thermalSubsystem.Status.HealthRollup); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_health_rollup"].desc, prometheus.GaugeValue, healthRollupValue, labelValues...)
	}
	if stateValue, ok := parseCommonStatusState(thermalSubsystem.Status.State); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_state"].desc, prometheus.GaugeValue, stateValue, labelValues...)
	}

	eg := newRecoverGroup(ctx)

	eg.Go(func() error {
		fans, err := thermalSubsystemFans(c.redfishClient.Service.GetClient(), thermalSubsystem)
		if err != nil {
			logger.Error("error getting fans from thermal subsystem", slog.String("operation", "thermalSubsystemFans()"), slog.Any("error", err))
			return nil
		}
		if len(fans) == 0 {
			logger.Info("no thermal subsystem fans found", slog.String("operation", "thermalSubsystemFans()"))
			return nil
		}
		for _, fan := range fans {
			parseThermalSubsystemFan(ch, chassisID, fan)
		}
		return nil
	})

	eg.Go(func() error {
		c.temperatures(c, ch, chassisID, thermalSubsystem, logger)
		return nil
	})

	eg.Go(func() error {
		// NOTE: Handles some odd (maybe even buggy) OEM implementations of LeakDeteactor
		leakDetectors := c.leakDetection(c, thermalSubsystem, logger)
		if len(leakDetectors) == 0 {
			logger.Info("no leak detectors found")
			return nil
		}
		for _, ld := range leakDetectors {
			parseLeakDetector(ch, chassisID, ld)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		logger.Error("goroutine error", slog.Any("error", err))
	}
}

// collectThermalMetrics emits the temperatures in a ThermalSubsystem's ThermalMetrics.
func (*ChassisCollector) collectThermalMetrics(ch chan<- prometheus.Metric, chassisID string, thermalSubsystem *schemas.ThermalSubsystem, logger *slog.Logger) {
	for i, temperature := range readThermalMetrics(thermalSubsystem, logger) {
		parseThermalSubsystemTemperature(ch, chassisID, i, temperature)
	}
}

// readThermalMetrics returns the temperature readings in a ThermalSubsystem's ThermalMetrics.
func readThermalMetrics(thermalSubsystem *schemas.ThermalSubsystem, logger *slog.Logger) []schemas.SensorArrayExcerpt {
	thermalMetrics, err := thermalSubsystem.ThermalMetrics()
	if err != nil {
		logger.Error("error getting thermal metrics from thermal subsystem", slog.String("operation", "thermalSubsystem.ThermalMetrics()"), slog.Any("error", err))
		return nil
	}
	if thermalMetrics == nil {
		logger.Info("no thermal metrics found", slog.String("operation", "thermalSubsystem.ThermalMetrics()"))
		return nil
	}
	return thermalMetrics.TemperatureReadingsCelsius
}

// thermalSubsystemFans fetches the ThermalSubsystem Fans collection as Fan resources.
// gofish's ThermalSubsystem.Fans() decodes the members into the deprecated Thermal resource's
// fan struct, which has no SpeedPercent, so every reading comes back empty. The collection link
// is unexported, so take it from the raw payload and decode the members as what they are.
func thermalSubsystemFans(client schemas.Client, thermalSubsystem *schemas.ThermalSubsystem) ([]*schemas.Fan, error) {
	var links struct {
		Fans schemas.Link `json:"Fans"`
	}
	if err := json.Unmarshal(thermalSubsystem.RawData, &links); err != nil {
		return nil, err
	}
	if links.Fans == "" {
		return nil, nil
	}
	return schemas.ListReferencedFans(client, links.Fans.String())
}

// getLeakDetectors works around an unfortunate fact that the LeakDetection schema is not yet standard, and some OEMs return
// a single LeakDetection object from their ThermalSubsystem, instead of a gofish-expected collection.
func (c *ChassisCollector) getLeakDetectors(thermalSubsystem *schemas.ThermalSubsystem, logger *slog.Logger) []*schemas.LeakDetector {
	var allDetectors []*schemas.LeakDetector

	// Standard gofish approach, for starters
	leakDetectionCollection, err := thermalSubsystem.LeakDetection()
	if err != nil {
		logger.Debug("standard LeakDetection() call failed, will try fallback", slog.Any("error", err))
		if leakDetectionCollection != nil {
			detectors, err := leakDetectionCollection.LeakDetectors()
			if err != nil {
				logger.Error("failed obtaining LeakDetectors at all", slog.Any("error", err))
				return nil
			}
			allDetectors = append(allDetectors, detectors...)
		}
		return allDetectors
	}

	// ...otherwise, try a fallback to handle buggy OEM implementations.
	leakDetectionURL := thermalSubsystem.ODataID + "/LeakDetection"
	leakDetection, err := schemas.GetLeakDetection(c.redfishClient.Service.GetClient(), leakDetectionURL)
	if err != nil {
		logger.Debug("fallback GetLeakDetection failed", slog.Any("error", err))
		return allDetectors
	}

	if leakDetection != nil {
		detectors, err := leakDetection.LeakDetectors()
		if err != nil {
			logger.Debug("error fetching leak detectors via fallback method", slog.Any("error", err))
			return nil
		}

		if len(detectors) > 0 {
			allDetectors = append(allDetectors, detectors...)
		}
	}
	return allDetectors
}

func parseChassisTemperature(ch chan<- prometheus.Metric, chassisID string, chassisTemperature schemas.Temperature) {
	chassisTemperatureSensorName := chassisTemperature.Name
	chassisTemperatureSensorID := chassisTemperature.MemberID
	chassisTemperatureStatus := chassisTemperature.Status
	chassisTemperatureLabelvalues := []string{"temperature", chassisID, chassisTemperatureSensorName, chassisTemperatureSensorID}

	chassisTemperatureStatusHealth := chassisTemperatureStatus.Health
	if chassisTemperatureStatusHealthValue, ok := parseCommonStatusHealth(chassisTemperatureStatusHealth); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_temperature_sensor_health"].desc, prometheus.GaugeValue, chassisTemperatureStatusHealthValue, chassisTemperatureLabelvalues...)
	}

	chassisTemperatureStatusState := chassisTemperatureStatus.State
	//			chassisTemperatureStatusLabelNames :=[]string{BaseLabelNames,"temperature_sensor_name","temperature_sensor_member_id")
	//		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_temperature_status_health"].desc, prometheus.GaugeValue, parseCommonStatusHealth(chassisTemperatureStatusHealth), chassisTemperatureLabelvalues...)
	if chassisTemperatureStatusStateValue, ok := parseCommonStatusState(chassisTemperatureStatusState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_temperature_sensor_state"].desc, prometheus.GaugeValue, chassisTemperatureStatusStateValue, chassisTemperatureLabelvalues...)
	}

	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_temperature_celsius"].desc, prometheus.GaugeValue, gofish.Deref(chassisTemperature.ReadingCelsius), chassisTemperatureLabelvalues...)
}

func parseChassisFan(ch chan<- prometheus.Metric, chassisID string, chassisFan schemas.ThermalFan) {
	chassisFanID := chassisFan.MemberID
	chassisFanName := chassisFan.Name
	chassisFanStaus := chassisFan.Status
	chassisFanStausHealth := chassisFanStaus.Health
	chassisFanStausState := chassisFanStaus.State
	chassisFanRPM := intPtrToFloat64(chassisFan.Reading)
	chassisFanUnit := chassisFan.ReadingUnits
	chassisFanRPMLowerCriticalThreshold := intPtrToFloat64(chassisFan.LowerThresholdCritical)
	chassisFanRPMUpperCriticalThreshold := intPtrToFloat64(chassisFan.UpperThresholdCritical)
	chassisFanRPMLowerFatalThreshold := intPtrToFloat64(chassisFan.LowerThresholdFatal)
	chassisFanRPMUpperFatalThreshold := intPtrToFloat64(chassisFan.UpperThresholdFatal)
	chassisFanRPMMin := intPtrToFloat64(chassisFan.MinReadingRange)
	chassisFanRPMMax := intPtrToFloat64(chassisFan.MaxReadingRange)

	chassisFanPercentage := chassisFanRPM
	if chassisFanUnit != schemas.PercentReadingUnits {
		// Some vendors (e.g. PowerEdge C6420) report null RPMs for Min/Max, as well as Lower/UpperFatal,
		// but provide Lower/UpperCritical, so use largest non-null for max. However, we can't know if
		// min is null (reported as zero by gofish) or just zero, so we'll have to assume a min of zero
		// if Min is not reported...
		min := chassisFanRPMMin
		max := math.Max(math.Max(chassisFanRPMMax, chassisFanRPMUpperFatalThreshold), chassisFanRPMUpperCriticalThreshold)
		chassisFanPercentage = 0
		if max != 0 {
			chassisFanPercentage = float64((chassisFanRPM+min)/max) * 100
		}
	}

	//			chassisFanStatusLabelNames :=[]string{BaseLabelNames,"fan_name","fan_member_id")
	chassisFanLabelvalues := []string{"fan", chassisID, chassisFanName, chassisFanID, strings.ToLower(string(chassisFanUnit))} // e.g. RPM -> rpm, Percentage -> percentage

	if chassisFanStausHealthValue, ok := parseCommonStatusHealth(chassisFanStausHealth); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_health"].desc, prometheus.GaugeValue, chassisFanStausHealthValue, chassisFanLabelvalues...)
	}

	if chassisFanStausStateValue, ok := parseCommonStatusState(chassisFanStausState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_state"].desc, prometheus.GaugeValue, chassisFanStausStateValue, chassisFanLabelvalues...)
	}
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm"].desc, prometheus.GaugeValue, chassisFanRPM, chassisFanLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm_min"].desc, prometheus.GaugeValue, chassisFanRPMMin, chassisFanLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm_max"].desc, prometheus.GaugeValue, chassisFanRPMMax, chassisFanLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm_percentage"].desc, prometheus.GaugeValue, chassisFanPercentage, chassisFanLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm_lower_threshold_critical"].desc, prometheus.GaugeValue, chassisFanRPMLowerCriticalThreshold, chassisFanLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm_upper_threshold_critical"].desc, prometheus.GaugeValue, chassisFanRPMUpperCriticalThreshold, chassisFanLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm_lower_threshold_fatal"].desc, prometheus.GaugeValue, chassisFanRPMLowerFatalThreshold, chassisFanLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_fan_rpm_upper_threshold_fatal"].desc, prometheus.GaugeValue, chassisFanRPMUpperFatalThreshold, chassisFanLabelvalues...)
}

func parseThermalSubsystemFan(ch chan<- prometheus.Metric, chassisID string, fan *schemas.Fan) {
	fanLabelValues := []string{"fan", chassisID, fan.Name, fan.ID}

	if fanHealthValue, ok := parseCommonStatusHealth(fan.Status.Health); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_fan_health"].desc, prometheus.GaugeValue, fanHealthValue, fanLabelValues...)
	}
	if fanStateValue, ok := parseCommonStatusState(fan.Status.State); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_fan_state"].desc, prometheus.GaugeValue, fanStateValue, fanLabelValues...)
	}

	// Every speed property is optional here, and a fan reporting a percentage need not report
	// RPM, so an absent reading is skipped rather than reported as a stopped fan.
	if fan.SpeedPercent.SpeedRPM != nil {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_fan_rpm"].desc, prometheus.GaugeValue, *fan.SpeedPercent.SpeedRPM, fanLabelValues...)
	}
	if fan.SpeedPercent.Reading != nil {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_fan_speed_percentage"].desc, prometheus.GaugeValue, *fan.SpeedPercent.Reading, fanLabelValues...)
	}
	if fan.RatedSpeedRPM != nil {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_fan_rpm_max"].desc, prometheus.GaugeValue, intPtrToFloat64(fan.RatedSpeedRPM), fanLabelValues...)
	}
}

func parseThermalSubsystemTemperature(ch chan<- prometheus.Metric, chassisID string, index int, temperature schemas.SensorArrayExcerpt) {
	if temperature.Reading == nil {
		return
	}

	// TemperatureReadingsCelsius is an array of sensor excerpts rather than of addressable
	// resources, so fall back to the array index to keep two unnamed readings from collapsing
	// into one series.
	sensorID := fmt.Sprint(index)
	if temperature.DataSourceURI != "" {
		sensorID = path.Base(temperature.DataSourceURI)
	}
	sensorName := temperature.DeviceName
	if sensorName == "" {
		sensorName = sensorID
	}

	temperatureLabelValues := []string{"temperature", chassisID, sensorName, sensorID}
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_thermal_subsystem_temperature_celsius"].desc, prometheus.GaugeValue, *temperature.Reading, temperatureLabelValues...)
}

func parseLeakDetector(ch chan<- prometheus.Metric, chassisID string, ld *schemas.LeakDetector) {
	ldID := ld.ID
	labelValues := []string{"leak_detector", chassisID, "LeakDetection", ldID}

	if statusHealth, ok := parseCommonStatusHealth(ld.Status.Health); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_leak_detector_health"].desc, prometheus.GaugeValue, statusHealth, labelValues...)
	}
}

// collectPower reads the chassis' legacy Power resource, when it links one.
func (c *ChassisCollector) collectPower(ch chan<- prometheus.Metric, chassis *schemas.Chassis, logger *slog.Logger) {
	chassisID := chassis.ID
	chassisPowerInfo, err := chassis.Power()
	if err != nil {
		logger.Error("error getting power data from chassis", slog.String("operation", "chassis.Power()"), slog.Any("error", err))
	} else if chassisPowerInfo == nil {
		logger.Info("no power data found", slog.String("operation", "chassis.Power()"))
	} else {
		// power voltages
		for _, chassisPowerInfoVoltage := range chassisPowerInfo.Voltages {
			parseChassisPowerInfoVoltage(ch, chassisID, chassisPowerInfoVoltage)
		}

		// power control
		for _, chassisPowerInfoPowerControl := range chassisPowerInfo.PowerControl {
			parseChassisPowerInfoPowerControl(ch, chassisID, chassisPowerInfoPowerControl)
		}

		// powerSupply
		for _, chassisPowerInfoPowerSupply := range chassisPowerInfo.PowerSupplies {
			parseChassisPowerInfoPowerSupply(ch, chassisID, chassisPowerInfoPowerSupply)
		}
	}
}

// skipSensors reads nothing. A BMC that serves Thermal and Power already emits the fan,
// temperature and voltage families from them, so a Sensors read would repeat them.
func (c *ChassisCollector) skipSensors(chan<- prometheus.Metric, *schemas.Chassis, *slog.Logger) {}

func parseChassisPowerInfoVoltage(ch chan<- prometheus.Metric, chassisID string, chassisPowerInfoVoltage schemas.Voltage) {
	chassisPowerInfoVoltageName := chassisPowerInfoVoltage.Name
	chassisPowerInfoVoltageID := chassisPowerInfoVoltage.MemberID
	chassisPowerInfoVoltageNameReadingVolts := chassisPowerInfoVoltage.ReadingVolts
	chassisPowerInfoVoltageState := chassisPowerInfoVoltage.Status.State
	chassisPowerVoltageLabelvalues := []string{"power_voltage", chassisID, chassisPowerInfoVoltageName, chassisPowerInfoVoltageID}
	if chassisPowerInfoVoltageStateValue, ok := parseCommonStatusState(chassisPowerInfoVoltageState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_voltage_state"].desc, prometheus.GaugeValue, chassisPowerInfoVoltageStateValue, chassisPowerVoltageLabelvalues...)
	}
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_voltage_volts"].desc, prometheus.GaugeValue, float32PtrToFloat64(chassisPowerInfoVoltageNameReadingVolts), chassisPowerVoltageLabelvalues...)
}

func parseChassisPowerInfoPowerControl(ch chan<- prometheus.Metric, chassisID string, chassisPowerInfoPowerControl schemas.PowerControl) {
	name := chassisPowerInfoPowerControl.Name
	id := chassisPowerInfoPowerControl.MemberID
	pm := chassisPowerInfoPowerControl.PowerMetrics
	chassisPowerVoltageLabelvalues := []string{"power_wattage", chassisID, name, id}
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_average_consumed_watts"].desc, prometheus.GaugeValue, float32PtrToFloat64(pm.AverageConsumedWatts), chassisPowerVoltageLabelvalues...)
}

func parseChassisPowerInfoPowerSupply(ch chan<- prometheus.Metric, chassisID string, chassisPowerInfoPowerSupply schemas.PowerSupply) {
	chassisPowerInfoPowerSupplyName := chassisPowerInfoPowerSupply.Name
	// This is optional in some devices causing duplicate metrics
	chassisPowerInfoPowerSupplyID := chassisPowerInfoPowerSupply.MemberID
	if chassisPowerInfoPowerSupplyID == "" {
		slog.Debug("PowerSupply ID is empty, using serial number as ID")
		chassisPowerInfoPowerSupplyID = chassisPowerInfoPowerSupply.SerialNumber
		if chassisPowerInfoPowerSupplyID == "" {
			slog.Error("PowerSupply ID and serial number empty - skipping power supply")
			return
		}
	}
	chassisPowerInfoPowerSupplyEfficiencyPercent := gofish.Deref(chassisPowerInfoPowerSupply.EfficiencyPercent)
	chassisPowerInfoPowerSupplyPowerCapacityWatts := chassisPowerInfoPowerSupply.PowerCapacityWatts
	chassisPowerInfoPowerSupplyPowerInputWatts := chassisPowerInfoPowerSupply.PowerInputWatts
	chassisPowerInfoPowerSupplyPowerOutputWatts := chassisPowerInfoPowerSupply.PowerOutputWatts
	chassisPowerInfoPowerSupplyLastPowerOutputWatts := chassisPowerInfoPowerSupply.LastPowerOutputWatts

	chassisPowerInfoPowerSupplyState := chassisPowerInfoPowerSupply.Status.State
	chassisPowerInfoPowerSupplyHealth := chassisPowerInfoPowerSupply.Status.Health
	chassisPowerSupplyLabelvalues := []string{"power_supply", chassisID, chassisPowerInfoPowerSupplyName, chassisPowerInfoPowerSupplyID}
	if chassisPowerInfoPowerSupplyStateValue, ok := parseCommonStatusState(chassisPowerInfoPowerSupplyState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_powersupply_state"].desc, prometheus.GaugeValue, chassisPowerInfoPowerSupplyStateValue, chassisPowerSupplyLabelvalues...)
	}
	if chassisPowerInfoPowerSupplyHealthValue, ok := parseCommonStatusHealth(chassisPowerInfoPowerSupplyHealth); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_powersupply_health"].desc, prometheus.GaugeValue, chassisPowerInfoPowerSupplyHealthValue, chassisPowerSupplyLabelvalues...)
	}
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_powersupply_power_efficiency_percentage"].desc, prometheus.GaugeValue, chassisPowerInfoPowerSupplyEfficiencyPercent, chassisPowerSupplyLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_powersupply_last_power_output_watts"].desc, prometheus.GaugeValue, float32PtrToFloat64(chassisPowerInfoPowerSupplyLastPowerOutputWatts), chassisPowerSupplyLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_powersupply_power_capacity_watts"].desc, prometheus.GaugeValue, float32PtrToFloat64(chassisPowerInfoPowerSupplyPowerCapacityWatts), chassisPowerSupplyLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_powersupply_power_input_watts"].desc, prometheus.GaugeValue, float32PtrToFloat64(chassisPowerInfoPowerSupplyPowerInputWatts), chassisPowerSupplyLabelvalues...)
	ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_power_powersupply_power_output_watts"].desc, prometheus.GaugeValue, float32PtrToFloat64(chassisPowerInfoPowerSupplyPowerOutputWatts), chassisPowerSupplyLabelvalues...)
}

func parseNetworkAdapter(ch chan<- prometheus.Metric, chassisID string, networkAdapter *schemas.NetworkAdapter) error {
	networkAdapterName := networkAdapter.Name
	networkAdapterID := networkAdapter.ID
	networkAdapterState := networkAdapter.Status.State
	networkAdapterHealthState := networkAdapter.Status.Health
	chassisNetworkAdapterLabelValues := []string{"network_adapter", chassisID, networkAdapterName, networkAdapterID}
	if networkAdapterStateValue, ok := parseCommonStatusState(networkAdapterState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_network_adapter_state"].desc, prometheus.GaugeValue, networkAdapterStateValue, chassisNetworkAdapterLabelValues...)
	}
	if networkAdapterHealthStateValue, ok := parseCommonStatusHealth(networkAdapterHealthState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_network_adapter_health_state"].desc, prometheus.GaugeValue, networkAdapterHealthStateValue, chassisNetworkAdapterLabelValues...)
	}

	networkPorts, err := networkAdapter.NetworkPorts()
	if err != nil {
		return err
	}
	for _, networkPort := range networkPorts {
		parseNetworkPort(ch, chassisID, networkPort, networkAdapterName, networkAdapterID)
	}
	return nil
}

func parseNetworkPort(ch chan<- prometheus.Metric, chassisID string, networkPort *schemas.NetworkPort, networkAdapterName string, networkAdapterID string) {
	networkPortName := networkPort.Name
	networkPortID := networkPort.ID
	networkPortState := networkPort.Status.State
	networkLinkStatus := networkPort.LinkStatus
	networkPortLinkType := networkPort.ActiveLinkTechnology
	networkPortLinkSpeed := fmt.Sprintf("%d Mbps", networkPort.CurrentLinkSpeedMbps)
	networkPortHealthState := networkPort.Status.Health
	networkPortConnectionType := networkPort.FCPortConnectionType
	networkPhysicalPortNumber := networkPort.PhysicalPortNumber
	chassisNetworkPortLabelValues := []string{"network_port", chassisID, networkAdapterName, networkAdapterID, networkPortName, networkPortID, string(networkPortLinkType), networkPortLinkSpeed, string(networkPortConnectionType), networkPhysicalPortNumber}

	if networkLinkStatusValue, ok := parsePortLinkStatus(schemas.PortLinkStatus(networkLinkStatus)); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_network_port_link_state"].desc, prometheus.GaugeValue, networkLinkStatusValue, chassisNetworkPortLabelValues...)
	}

	if networkPortStateValue, ok := parseCommonStatusState(networkPortState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_network_port_state"].desc, prometheus.GaugeValue, networkPortStateValue, chassisNetworkPortLabelValues...)
	}
	if networkPortHealthStateValue, ok := parseCommonStatusHealth(networkPortHealthState); ok {
		ch <- prometheus.MustNewConstMetric(chassisMetrics["chassis_network_port_health_state"].desc, prometheus.GaugeValue, networkPortHealthStateValue, chassisNetworkPortLabelValues...)
	}
}
