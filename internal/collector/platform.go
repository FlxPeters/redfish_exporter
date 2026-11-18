package collector

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

// CollectorConstructor builds a collector for a module, the way newCollectorFromModule does
// for a prober. A platform names one per prober it replaces wholesale.
type CollectorConstructor func(moduleName string, m *config.Module, rfClient *gofish.APIClient, logger *slog.Logger) (ContextAwareCollector, error)

// Platform is the set of code paths the exporter runs against one server type. Each field
// is one read that differs between server types, as a method expression on the collector
// that performs it, or a function built from per-firmware data; a collector calls its cell and
// never decides for itself.
//
// The table below is the only place a server type is mapped to code. Keys are
// "<server_sku>-<firmware_version>", the same strings an operator declares under
// `platforms:` in config.yml and that a scrape names with the `platform` query parameter.
// Keys are compared case-insensitively, because the config loader lowercases map keys: the
// table is keyed by the lowercased Name, and Name keeps the spelling shown in metrics and logs.
//
// This repository ships only the Default row. Rows for named server types live outside it and
// are compiled in with -tags private_platforms, which replaces extraPlatforms (platform_rows.go).
type Platform struct {
	Name         string
	NVLink       func(*GPUCollector, context.Context, SystemGPU) (GPUNVLinkCollection, error)
	ChassisPower func(*ChassisCollector, chan<- prometheus.Metric, *schemas.Chassis, *slog.Logger)
	// ChassisSensors reads a chassis' Sensors collection, for a BMC that reports as Sensors
	// what the legacy Thermal and Power resources carried.
	ChassisSensors func(*ChassisCollector, chan<- prometheus.Metric, *schemas.Chassis, *slog.Logger)
	// ChassisLeakDetection reads the leak detectors in a chassis' ThermalSubsystem.
	ChassisLeakDetection func(*ChassisCollector, *schemas.ThermalSubsystem, *slog.Logger) []*schemas.LeakDetector
	// ChassisTemperatures reads the temperatures in a chassis' ThermalSubsystem.
	ChassisTemperatures func(*ChassisCollector, chan<- prometheus.Metric, string, *schemas.ThermalSubsystem, *slog.Logger)
	// ChassisList reads the chassis the chassis collector walks.
	ChassisList func(*ChassisCollector) ([]*schemas.Chassis, error)
	// Collectors replaces whole collectors: the constructor used for a prober on this
	// platform, in place of the one newCollectorFromModule picks. Module and prober names in
	// config.yml do not change; a platform without an entry for a prober gets the default
	// collector for it. Cells above tune one read inside a collector; this swaps the type.
	Collectors map[string]CollectorConstructor
}

// DefaultPlatformName is the platform a scrape runs as when the request names none, or
// names one the table does not know.
const DefaultPlatformName = "default"

var Default = Platform{
	Name:                 DefaultPlatformName,
	NVLink:               (*GPUCollector).nvlinkPortsExpand,
	ChassisPower:         (*ChassisCollector).collectPower,
	ChassisSensors:       (*ChassisCollector).skipSensors,
	ChassisLeakDetection: (*ChassisCollector).getLeakDetectors,
	ChassisTemperatures:  (*ChassisCollector).collectThermalMetrics,
	ChassisList:          (*ChassisCollector).listChassis,
}

var platforms = platformTable(append([]Platform{Default}, extraPlatforms...))

// platformTable keys rows by their lowercased Name.
func platformTable(rows []Platform) map[string]Platform {
	table := make(map[string]Platform, len(rows))
	for _, p := range rows {
		table[strings.ToLower(p.Name)] = p
	}
	return table
}

// PlatformAware is implemented by collectors that route one or more reads by platform.
type PlatformAware interface {
	SetPlatform(Platform)
}

// PlatformByName returns the platform for a "<server_sku>-<firmware_version>" key, or
// Default and false when the table has no such key.
func PlatformByName(name string) (Platform, bool) {
	if p, ok := platforms[strings.ToLower(name)]; ok {
		return p, true
	}
	return Default, false
}

// PlatformNames lists the keys in the table, sorted.
func PlatformNames() []string {
	names := make([]string, 0, len(platforms))
	for name := range platforms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
