//go:build platform_examples

package collector

// exampleSKUA is an example of a row a private build adds: a server type that needs a cell and
// a collector the public code does not have. A private build keeps the row, the cell and the
// collector in files of its own and copies them in beside the public ones; here they are test
// files. platform_example_sku_b_test.go names this row's cell, and
// platform_example_sku_a_fw2_test.go copies the whole row.
var exampleSKUA = Platform{
	Name: "EXAMPLE-SKU-A-EXAMPLE-FW-1",

	// Public reads, the same ones Default names, defined in gpu_collector.go
	// (nvlinkPortsExpand) and chassis_collector.go (the rest).
	NVLink:               (*GPUCollector).nvlinkPortsExpand,
	ChassisSensors:       (*ChassisCollector).skipSensors,
	ChassisLeakDetection: (*ChassisCollector).getLeakDetectors,
	ChassisTemperatures:  (*ChassisCollector).collectThermalMetrics,
	ChassisList:          (*ChassisCollector).listChassis,

	// A cell written for this row: chassis_collector_example_power_test.go.
	ChassisPower: (*ChassisCollector).collectExamplePower,

	// A collector written for this row, serving the system_collector prober in place of the
	// public one: system_collector_example_test.go.
	Collectors: map[string]CollectorConstructor{
		"system_collector": newExampleSystemCollector,
	},
}
