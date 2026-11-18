# Chassis Collector Metrics

Every metric emitted by `chassis_collector` (`internal/collector/chassis_collector.go`), the
Redfish resource each one is read from, and — where that resource is deprecated — what to read
instead.

This page describes what the `default` platform reads. A row for another platform can read
these families from a different resource, or leave chassis out; see
[platforms.md](platforms.md#cells).

All names below are prefixed `redfish_chassis_`. All metrics are gauges. Every family carries
`chassis_id` (the Redfish `Chassis` `Id`) and, except for the log service family, a `resource`
label whose value is fixed per family and is listed with each table.

The **Value** column in each table below names what the number means. Enumerations are encoded as
integers, defined once in `internal/collector/common_collector.go` and repeated in each metric's
`HELP` text:

| Value | Meaning |
|---|---|
| **Health** | `1` OK, `2` Warning, `3` Critical |
| **State** | `1` Enabled, `2` Disabled, `3` StandbyOffline, `4` StandbySpare, `5` InTest, `6` Starting, `7` Absent, `8` UnavailableOffline, `9` Deferring, `10` Quiesced, `11` Updating, `12` Standby |
| **Port link** | `1` Up, `0` Down |
| **Intrusion sensor** | `1` Normal, `2` TamperingDetected, `3` HardwareIntrusion |
| **Info** | Always `1`. The information is in the labels; the number carries nothing |
| **Celsius**, **Watts**, **Volts**, **RPM**, **Percent** | A physical reading in that unit, as the BMC reported it |

Percentages are `0`–`100`, not `0`–`1`. No unit is converted on the way through — a reading is
passed on as the BMC gave it.

Note that a metric name ending `_state` does not reliably mean the State encoding:
`physical_security_sensor_state` is an intrusion sensor code and `network_port_link_state` is a
link code. Read the Value column rather than the suffix.

Health and state metrics are emitted only when the BMC supplies a value the exporter recognises,
so an absent one produces no sample rather than a zero.

Readings are less consistent. The ThermalSubsystem families skip an absent reading, but the
deprecated `Thermal` and `Power` families emit `0` for one the BMC omitted, because they predate
that rule — a `0` from `fan_rpm` or `temperature_celsius` may mean "not reported" rather than
"zero". Prefer `thermal_subsystem_*`, where the distinction is real.

## Chassis

Source: `Chassis/{id}` · `resource="chassis"`

| Metric | Value | Labels | Description |
|---|---|---|---|
| `health` | Health | `chassis_id` | Health of the chassis |
| `health_rollup` | Health | `chassis_id` | Health of the chassis and everything below it |
| `state` | State | `chassis_id` | State of the chassis |
| `model_info` | Info | `chassis_id`, `manufacturer`, `model`, `part_number`, `sku` | Always `1`; the identity is in the labels |

## Thermal — deprecated source

> **Deprecated.** These read `Chassis/{id}/Thermal`, which Redfish deprecated in release 2020.4
> in favour of `ThermalSubsystem`. See the
> [Redfish Resource and Schema Guide](https://redfish.dmtf.org/schemas/DSP2046_2020.4.html).
>
> The collector still reads it, because BMCs that implement it remain in service. But a BMC that
> serves only `ThermalSubsystem` produces none of these metrics, and many do. Check what a given
> host serves before relying on them — see [Checking what a host actually
> serves](#checking-what-a-host-actually-serves) — and prefer the ThermalSubsystem families below.

### Temperature

Source: `Chassis/{id}/Thermal` → `Temperatures[]` · `resource="temperature"`

| Metric | Value | Labels | Description | Read instead |
|---|---|---|---|---|
| `temperature_celsius` | Celsius | `chassis_id`, `sensor`, `sensor_id` | Temperature reading | `thermal_subsystem_temperature_celsius` |
| `temperature_sensor_health` | Health | same | Sensor health | *no direct equivalent* — see note |
| `temperature_sensor_state` | State | same | Sensor state | *no direct equivalent* — see note |

**Note on sensor health and state.** `ThermalMetrics.TemperatureReadingsCelsius[]` is an array of
sensor *excerpts*: each entry carries a reading, a device name and a `DataSourceUri`, but no
`Status`. Health and state for those sensors live on the `Sensors` resource the `DataSourceUri`
points at, which the chassis collector does not fetch — that is one GET per sensor, and a
densely instrumented chassis can report dozens. For a non-OK thermal condition, use
`thermal_subsystem_health` (the subsystem's own rollup) or `chassis_health_rollup`.

### Fans

Source: `Chassis/{id}/Thermal` → `Fans[]` · `resource="fan"`

| Metric | Value | Labels | Description | Read instead |
|---|---|---|---|---|
| `fan_rpm` | RPM *or* percent | `chassis_id`, `fan`, `fan_id`, `fan_unit` | Reading, in the unit named by `fan_unit` | `thermal_subsystem_fan_rpm` |
| `fan_health` | Health | same | Fan health | `thermal_subsystem_fan_health` |
| `fan_state` | State | same | Fan state | `thermal_subsystem_fan_state` |
| `fan_rpm_percentage` | Percent | same | Reading as a percentage of the min–max range, derived | `thermal_subsystem_fan_speed_percentage` (reported, not derived) |
| `fan_rpm_max` | RPM *or* percent | same | Highest possible reading | `thermal_subsystem_fan_rpm_max` |
| `fan_rpm_min` | RPM *or* percent | same | Lowest possible reading | *none* |
| `fan_rpm_lower_threshold_critical` | RPM *or* percent | same | Below normal, not fatal | *none* — see note |
| `fan_rpm_lower_threshold_non_critical` | RPM *or* percent | same | Below normal, not critical | *none* |
| `fan_rpm_lower_threshold_fatal` | RPM *or* percent | same | Below normal, fatal | *none* |
| `fan_rpm_upper_threshold_critical` | RPM *or* percent | same | Above normal, not fatal | *none* |
| `fan_rpm_upper_threshold_non_critical` | RPM *or* percent | same | Above normal, not critical | *none* |
| `fan_rpm_upper_threshold_fatal` | RPM *or* percent | same | Above normal, fatal | *none* |

**Note on fan thresholds.** The replacement `Fan` resource (`#Fan.v1_x.Fan`) carries no threshold
properties at all — it reports `SpeedPercent`, `RatedSpeedRPM` and `Status`. There is nothing to
re-source the six threshold metrics from, so a rule that compares a reading against its own
threshold cannot be ported as written; compare against `thermal_subsystem_fan_rpm_max` or a fixed
bound instead, and treat `thermal_subsystem_fan_health` as the vendor's own verdict.

**Note on `fan_unit`.** The deprecated family reports either RPM or a percentage in the *same*
metric, distinguished by the `fan_unit` label. The ThermalSubsystem family splits them into two
metrics, so no label is needed to interpret the value.

## ThermalSubsystem

Source: `Chassis/{id}/ThermalSubsystem`. This is the replacement for `Thermal`.

### Subsystem status

Source: `Chassis/{id}/ThermalSubsystem` → `Status` · `resource="thermal_subsystem"`

| Metric | Value | Labels | Description |
|---|---|---|---|
| `thermal_subsystem_health` | Health | `chassis_id` | Health of the thermal subsystem |
| `thermal_subsystem_health_rollup` | Health | `chassis_id` | Health of the subsystem and everything below it |
| `thermal_subsystem_state` | State | `chassis_id` | State of the thermal subsystem |

### Fans

Source: `Chassis/{id}/ThermalSubsystem/Fans/{id}` · `resource="fan"`

| Metric | Value | Labels | Description |
|---|---|---|---|
| `thermal_subsystem_fan_rpm` | RPM | `chassis_id`, `fan`, `fan_id` | Rotational speed, from `SpeedPercent.SpeedRPM` |
| `thermal_subsystem_fan_speed_percentage` | Percent | same | Speed as a percentage of rated, from `SpeedPercent.Reading` |
| `thermal_subsystem_fan_rpm_max` | RPM | same | Rated maximum speed, from `RatedSpeedRPM` |
| `thermal_subsystem_fan_health` | Health | same | Fan health |
| `thermal_subsystem_fan_state` | State | same | Fan state |

A fan reporting a percentage need not report RPM, and vice versa — each is emitted only when
present. A host may serve an empty `Fans` collection on every chassis while still reporting
temperatures, in which case no fan metric appears at all.

### Temperatures

Source: `Chassis/{id}/ThermalSubsystem/ThermalMetrics` → `TemperatureReadingsCelsius[]` ·
`resource="temperature"`

| Metric | Value | Labels | Description |
|---|---|---|---|
| `thermal_subsystem_temperature_celsius` | Celsius | `chassis_id`, `sensor`, `sensor_id` | Temperature reading |

`sensor_id` is the last path segment of the entry's `DataSourceUri`, and `sensor` is its
`DeviceName`. When an entry carries neither — both are optional — the array index is used for
both, so that two unnamed readings do not collapse into one series.

Sensor naming differs from the deprecated family and is **not** a drop-in substitution: the
`Thermal` resource reports names like `HGX GPU Temp`, while `ThermalMetrics` on the same class of
host reports `HGX_GPU_0_TEMP_0`, `HGX_GPU_0_DRAM_0_Temp_0`, `HGX_ProcessorModule_0_Inlet_Temp_0`.
A selector written against the old names matches nothing here.

A sensor reported by several chassis has one series under each, with the same `sensor_id`.

### Leak detectors

Source: `Chassis/{id}/ThermalSubsystem/LeakDetection/LeakDetectors/{id}` · `resource="leak_detector"`

| Metric | Value | Labels | Description |
|---|---|---|---|
| `leak_detector_health` | Health | `chassis_id`, `leak_detection_id`, `leak_detector_id` | Leak detector health |

Some OEMs return a single `LeakDetection` object where the schema calls for a collection. The
collector tries the standard path first and falls back to the OEM shape
(`getLeakDetectors`), so both are covered.

## Power — deprecated source

> **Deprecated.** These read `Chassis/{id}/Power`, deprecated in the same Redfish release as
> `Thermal` and replaced by `PowerSubsystem` plus `EnvironmentMetrics`. See the
> [Redfish Resource and Schema Guide](https://redfish.dmtf.org/schemas/DSP2046_2020.4.html).
>
> **The default does not read `PowerSubsystem` or `EnvironmentMetrics`,** so a BMC that serves
> only those resources yields no chassis power data unless its platform row names cells that do.
> See [platforms.md](platforms.md#cells).

### Power control and voltages

Source: `Chassis/{id}/Power` → `PowerControl[]`, `Voltages[]`

| Metric | Value | Labels | `resource` | Description |
|---|---|---|---|---|
| `power_average_consumed_watts` | Watts | `chassis_id`, `power_voltage`, `power_voltage_id` | `power_wattage` | Average consumed power, from `PowerControl[].PowerMetrics` |
| `power_voltage_volts` | Volts | `chassis_id`, `power_voltage`, `power_voltage_id` | `power_voltage` | Voltage reading |
| `power_voltage_state` | State | same | `power_voltage` | Voltage sensor state |

`power_average_consumed_watts` reuses the voltage label names despite being a power reading, and
is distinguished only by `resource="power_wattage"`. Filter on `resource`, not on the label names.

### Power supplies

Source: `Chassis/{id}/Power` → `PowerSupplies[]` · `resource="power_supply"`

| Metric | Value | Labels | Description |
|---|---|---|---|
| `power_powersupply_health` | Health | `chassis_id`, `power_supply`, `power_supply_id` | PSU health |
| `power_powersupply_state` | State | same | PSU state |
| `power_powersupply_power_input_watts` | Watts | same | Measured input power |
| `power_powersupply_power_output_watts` | Watts | same | Measured output power |
| `power_powersupply_last_power_output_watts` | Watts | same | Average output power |
| `power_powersupply_power_capacity_watts` | Watts | same | Rated capacity, **per PSU** |
| `power_powersupply_power_efficiency_percentage` | Percent | same | Rated efficiency |

## Network adapters and ports

Source: `Chassis/{id}/NetworkAdapters/{id}` and its `NetworkPorts/{id}`

| Metric | Value | `resource` | Labels | Description |
|---|---|---|---|---|
| `network_adapter_state` | State | `network_adapter` | `chassis_id`, `network_adapter`, `network_adapter_id` | Adapter state |
| `network_adapter_health_state` | Health | `network_adapter` | same | Adapter health |
| `network_port_state` | State | `network_port` | adapter labels + `network_port`, `network_port_id`, `network_port_type`, `network_port_speed`, `network_port_connectiont_type`, `network_physical_port_number` | Port state |
| `network_port_health_state` | Health | `network_port` | same | Port health |
| `network_port_link_state` | Port link | `network_port` | same | Port link state |

`network_port_connectiont_type` is misspelled in the emitted label. It is left as-is because
renaming a label breaks every query using it.

## Physical security

Source: `Chassis/{id}` → `PhysicalSecurity` · `resource="physical_security"`

| Metric | Value | Labels | Description |
|---|---|---|---|
| `physical_security_sensor_state` | Intrusion sensor | `chassis_id`, `intrusion_sensor_number`, `intrusion_sensor_rearm` | Intrusion sensor state |

## Log services — declared but never emitted

| Metric | Value | Labels | Description |
|---|---|---|---|
| `log_service_state` | State | `chassis_id`, `log_service`, `log_service_id`, `log_service_enabled`, `log_service_overwrite_policy` | Log service state |
| `log_service_health_state` | Health | same | Log service health |

**These two never produce a sample.** They are registered in the metric map, so they appear in
`/metrics` output as `HELP`/`TYPE` headers and in anything reading the exporter's `Describe`
output, but nothing in the collector fetches `LogServices` or emits them. They are a definition
without an implementation.

Do not build on them. They are listed here so that seeing them in a metric browser and finding no
data is explained rather than mysterious. This family also has no `resource` label, unlike every
other family above.

## Checking what a host actually serves

Whether a chassis serves `Thermal` or `ThermalSubsystem` is a property of the BMC, not of the
exporter. To check before writing a rule:

```fish
curl -sk -u "$BMC_USER:$BMC_PASS" https://$BMC/redfish/v1/Chassis/1 | jq 'keys'
```

`Thermal` and `Power` present means the deprecated families will populate; `ThermalSubsystem`,
`PowerSubsystem`, `Sensors` and `EnvironmentMetrics` present means they will not, unless the
host's platform row reads those resources instead.

---

*Metric list verified against `internal/collector/chassis_collector.go`. Which resources a BMC
actually serves varies by vendor, model and firmware; the observations here come from a small
number of captured Redfish trees, so do not assume another vendor or generation behaves the same
without checking it yourself.*
