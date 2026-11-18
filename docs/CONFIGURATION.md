# Configuration

Generic types include:

- `<string>` regular string values
- `<float64>` regular float64 value as expected in Golang
- `<timeout>` a string representation of a Golang time.Duration, e.g. `30s`

```yaml
hosts:
  [ <string>: <hostdetail> ]
  default: <hostdetail>
groups:
  [ <string>: <hostdetail> ]
modules: [ <string>: <module> ]
[ platforms: [ <string>: <platforms> ] ]
[ redfish_client: <redfish_client> ]
```

## `<hostdetail>`
``` yaml
username: <string>
password: <string>
```

Note that the `default` entry above is useful in order to avoid the exporter failing when attempting to collect from a host not explicitly defined in `hosts`.

## `<redfish_client>`

Tuning for the underlying Redfish (gofish) client. Every key is optional and shown with its default.

``` yaml
[ max_concurrent_requests: <int> | default = 1 ]
[ dial_timeout: <timeout> | default = 10s ]
[ response_header_timeout: <timeout> | default = 30s ]
[ logout_timeout: <timeout> | default = 10s ]
```

Any key may also be set from the environment as `REDFISH_CLIENT_<KEY>`, prefixed further if `--config.env-prefix` is given — e.g. `REDFISH_CLIENT_LOGOUT_TIMEOUT=5s`.

Within one scrape, a GET of a URL another collector has already fetched is answered from that response, so a resource several collectors read costs the BMC one request. Only successful responses up to 256 KiB are kept, and nothing outlives the scrape.

### Timeouts and session teardown

The exporter opens one Redfish session per scrape request and deletes it when the request finishes. BMCs limit how many sessions may be open at once, and refuse new ones once that limit is reached, so a session that is not released denies service to later scrapes until the BMC's own idle timeout reclaims the slot. The two timeouts below exist to keep that from happening.

`response_header_timeout` bounds how long a request waits for response headers after it has been written. Without it, the scrape's context is the only limit on any request, so a BMC that completes TCP and TLS and then declines to answer holds the request open for as long as it stays silent. It deliberately bounds only the wait for headers, so a slow but progressing response body is unaffected.

Set it below the scrape timeout of whatever is calling `/redfish`, or it will not take effect: the scrape context is cancelled first and that becomes the operative limit. Its purpose is to cap how much of a scrape's budget any *single* stalled request can consume — which matters most at `max_concurrent_requests: 1`, where requests are issued one at a time and one stalled call would otherwise spend the remainder of the budget on its own.

`logout_timeout` bounds the session delete issued at the end of a scrape. That request runs on a context deliberately detached from the scrape's: by the time teardown happens the scrape context is frequently already cancelled — which is exactly the case where a session would otherwise be stranded — and a cancelled context cannot carry the request that cleans up after it. Detached, it needs a deadline of its own.

It bounds each attempt, not the teardown as a whole. A delete the BMC refuses is retried directly (see below), and those retries share a second budget of the same size, so teardown of a silent BMC takes up to roughly twice `logout_timeout` — on a goroutine whose scrape response has already been written.

Both fall back to the defaults above when set to zero, so a config file predating these keys cannot leave a request unbounded.

### Observing sessions on the device

```
# HELP redfish_sessions_open Sessions currently open on the BMC, including the one this scrape opened.
# TYPE redfish_sessions_open gauge
```

Exposed on `/redfish?target=<host>` alongside `redfish_up`, so Prometheus attributes it to the target it scraped and it carries no labels of its own. It costs one extra GET per scrape: the session collection document, read for its member count rather than by walking each member.

This is the only session signal that describes the device rather than the exporter. The counters below can only see what this process did — a slot stranded by an earlier scrape, by a previous build, or by another client entirely occupies the device's cap just the same and is invisible to them. A BMC refusing new sessions is a device at its limit, and this is the metric that shows the limit approaching, and shows it receding once a fix is deployed.

It is absent, rather than zero, when the BMC declines to list its sessions — a 403 for an account behind a forced password change, for instance. That is deliberate: a wrong number is worse than a gap, and `absent()` is queryable.

### Observing session teardown

```
# HELP redfish_exporter_session_logouts_total Redfish session teardown attempts by target and outcome. result=success is a delete the BMC accepted, result=recovered means that delete failed and the direct DELETE fallback released the slot, and result=failure means every attempt failed and the session may stay occupied on the BMC until its own idle timeout reclaims it.
# TYPE redfish_exporter_session_logouts_total counter
```

Exposed on `/metrics`, not on `/redfish`, because teardown happens after a scrape's response has already been written. Labels are `target` (the BMC address) and `result` (`success`, `recovered` or `failure`). It is labelled per device because a session limit is a per-device property; the `http.client.*` metrics cannot answer "which BMC is holding sessions we failed to release", since they identify the exporter rather than the target.

There is deliberately no session-id label. BMCs that mint a random identifier per session would make every scrape a new series, in Prometheus and in the exporter's own memory, since a counter never releases a child once created. The identifier is in the logs instead, where it costs nothing to keep.

A rising `result="failure"` rate for a target means sessions are accumulating on that BMC — read against `redfish_sessions_open` for the same target to see whether they are actually piling up. `result="recovered"` is a slot that was only released because the fallback asked again: the teardown path is working but the BMC is not answering the first request, which is worth watching before it turns into failures.

Session actions are logged at `debug`, since on the healthy path they are two lines per scrape per target — `redfish_sessions_open` is the signal to run in production. Failures stay at `error` regardless of level, so a stranded slot is never silent. Every line carries `session_id` and `session_uri`:

```
level=DEBUG msg="redfish session created" scrape_host=10.0.0.1 session_id=42 session_uri=/redfish/v1/SessionService/Sessions/42
level=DEBUG msg="redfish session deleted" scrape_host=10.0.0.1 session_id=42 session_uri=/redfish/v1/SessionService/Sessions/42 elapsed=41ms
```

At `debug`, a `created` line with no matching `deleted` or `failed to delete` line for the same `session_id` is a session the exporter stopped tracking without releasing.

### Recovering a refused teardown

A delete the BMC refuses is not the end of the attempt. The exporter issues the `DELETE` against the session URI itself, first with the session token and then, if that is rejected, with the configured credentials, and records the outcome as `result="recovered"` if either is accepted.

Both retries are worth making. A timeout or a 5xx says only that this attempt did not release the slot, not that it cannot be released. And a BMC that has already invalidated the session token while still holding its slot answers `401` to the only request the Redfish client can make — there the credentialed attempt is the only one that can succeed.

Nothing is retried after a delete the BMC accepted: a second `DELETE` would target a slot the device may have already reissued.

**Known limitation.** If a session creation reaches the BMC but the exporter stops waiting before reading the response, the BMC may create a session whose identifier arrives in a response header the exporter never read. Nothing can delete that session, and it is not counted by the metric above, because no teardown is ever attempted for it. It is released by the BMC's own idle timeout. The `failed to create redfish session` log line marks the scrapes where this may have happened.

## `<platforms>`
[source](../internal/collector/platform.go)

A platform is one server type, keyed `<server_sku>-<firmware_version>`. A scrape names its platform with the `platform` HTTP query parameter, and the exporter reads that BMC the way the platform's row in its platform table says. This repository ships only the `default` row. The design, and how rows for other server types are built in, is in [platforms.md](platforms.md).

``` yaml
platforms:
  <server_sku>-<firmware_version>:
    # Modules a scrape that names this platform runs. They replace any module parameters
    # on the request. Each must be defined under modules:, and the list must not be empty.
    modules: [ <module_name>, ... ]
```

The block is optional: a request whose platform has no entry runs its own `module` parameters. Keys are compared case-insensitively.

```
/redfish?target=192.0.2.1&group=secure&platform=EXAMPLE-SKU-1.02.03
```

A `platform` the exporter's table does not know runs as `default`, logs a warning and increments `redfish_exporter_unknown_platforms_requested_total`. A `platforms:` key the table does not know is logged at error level when the config is loaded. Every scrape reports what it ran as:

```
# HELP redfish_exporter_platform_info The platform this scrape ran as, and the platform the request named: requested is empty when the request named none, and platform is default when it named one the exporter's table does not know.
# TYPE redfish_exporter_platform_info gauge
redfish_exporter_platform_info{platform="default",requested=""} 1
```

## `<module>`
Users of `blackbox_exporter` will be familiar with the concept of [modules (aka probers)](https://github.com/prometheus/blackbox_exporter/blob/master/CONFIGURATION.md#module).
Since `v0.2.0-alpha` released in October 2025, this fork of the redfish_exporter supports a similar concept.
With modules, users may enable only certain bits of collector behavior, tailoring data gathering to specific hosts.
As an example, a host without GPUs wouldn't want to spend network round-trips using the `gpu_collector` prober module, and that may be left out on the client (Prometheus) end.

**Note:** To retain backwards compatibility, the redfish_exporter will default to probing a system using all collectors below except the JSON collector. If this is not desired behavior, users must define a `modules:` configuration in the redfish_exporter, and specify one or more `module` HTTP query parameters on the client end.

The `module` parameter may be repeated, and each value may list several modules separated by commas: `module=chassis,gpu` runs the same collectors as `module=chassis&module=gpu`. A scrape job that chooses a host's modules with a relabel rule needs the comma form, because a rule sets one value.

**In a future release, users will be required to specify modules in the exporter and client**

Modules follow the below configuration schema:

``` yaml
# The type of prober this module uses
prober: <prober_string>

[ chassis_collector: <chassis_collector> ]
[ gpu_collector: <gpu_collector> ]
[ json_collector: <json_collector> ]
[ manager_collector: <manager_collector> ]
[ system_collector: <system_collector> ]
[ telemetry_collector: <telemetry_collector> ]
```

### `<prober_string>`

One of:

- `chassis_collector`
- `gpu_collector`
- `json_collector`
- `manager_collector`
- `system_collector`
- `telemetry_collector`
- `powershelf_collector`

### `<chassis_collector>`
[source](../collector/chassis_collector.go)

The Chassis Collector primarily exposes health data from the Chassis API. Against a Lambda lab system, the collector yields the following timeseries:

```
# HELP redfish_chassis_health health of chassis,1(OK),2(Warning),3(Critical)
# TYPE redfish_chassis_health gauge

# HELP redfish_chassis_health_rollup health rollup of chassis,1(OK),2(Warning),3(Critical)
# TYPE redfish_chassis_health_rollup gauge

# HELP redfish_chassis_leak_detector_health chassis leak detector health state,1(OK),2(Warning),3(Critical)
# TYPE redfish_chassis_leak_detector_health gauge

# HELP redfish_chassis_model_info organization responsible for producing the chassis, the name by which the manufacturer generally refers to the chassis, and a part number and sku assigned by the organization that is responsible for producing or manufacturing the chassis
# TYPE redfish_chassis_model_info gauge

# HELP redfish_chassis_network_adapter_state chassis network adapter state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_chassis_network_adapter_state gauge

# HELP redfish_chassis_state state of chassis,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_chassis_state gauge
```

Exposes no user configuration.

### `<gpu_collector>`
[source](../collector/gpu_collector.go)

The GPU Collector primarily exposes a wealth of Nvidia GPU-related data using a mix of Oem and Processor API data.
**Note:** This collector iterates many Redfish paths and depending on network round trips, may take a significant amount of time (30+ seconds) to return data.

Against a Lambda lab system, the collector yields the following timeseries:

```text
# HELP redfish_exporter_collector_duration_seconds Collector time duration.
# TYPE redfish_exporter_collector_duration_seconds gauge

# HELP redfish_gpu_context_utilization_seconds_total Accumulated GPU context utilization duration in seconds
# TYPE redfish_gpu_context_utilization_seconds_total counter

# HELP redfish_gpu_health GPU processor health,1(OK),2(Warning),3(Critical)
# TYPE redfish_gpu_health gauge

# HELP redfish_gpu_info GPU information with serial number and UUID
# TYPE redfish_gpu_info gauge

# HELP redfish_gpu_memory_capacity_mib GPU memory capacity in MiB
# TYPE redfish_gpu_memory_capacity_mib gauge

# HELP redfish_gpu_memory_correctable_row_remapping_count GPU memory correctable row remapping count
# TYPE redfish_gpu_memory_correctable_row_remapping_count gauge

# HELP redfish_gpu_memory_ecc_correctable current correctable memory ecc errors reported on the gpu
# TYPE redfish_gpu_memory_ecc_correctable counter

# HELP redfish_gpu_memory_ecc_uncorrectable current uncorrectable memory ecc errors reported on the gpu
# TYPE redfish_gpu_memory_ecc_uncorrectable counter

# HELP redfish_gpu_memory_health GPU memory health,1(OK),2(Warning),3(Critical)
# TYPE redfish_gpu_memory_health gauge

# HELP redfish_gpu_memory_high_availability_bank_count GPU memory high availability bank count
# TYPE redfish_gpu_memory_high_availability_bank_count gauge

# HELP redfish_gpu_memory_low_availability_bank_count GPU memory low availability bank count
# TYPE redfish_gpu_memory_low_availability_bank_count gauge

# HELP redfish_gpu_memory_max_availability_bank_count GPU memory max availability bank count
# TYPE redfish_gpu_memory_max_availability_bank_count gauge

# HELP redfish_gpu_memory_no_availability_bank_count GPU memory no availability bank count
# TYPE redfish_gpu_memory_no_availability_bank_count gauge

# HELP redfish_gpu_memory_partial_availability_bank_count GPU memory partial availability bank count
# TYPE redfish_gpu_memory_partial_availability_bank_count gauge

# HELP redfish_gpu_memory_row_remapping_failed GPU memory row remapping failed status (1 if failed)
# TYPE redfish_gpu_memory_row_remapping_failed gauge

# HELP redfish_gpu_memory_row_remapping_pending GPU memory row remapping pending status (1 if pending)
# TYPE redfish_gpu_memory_row_remapping_pending gauge

# HELP redfish_gpu_memory_state GPU memory state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_gpu_memory_state gauge

# HELP redfish_gpu_memory_uncorrectable_row_remapping_count GPU memory uncorrectable row remapping count
# TYPE redfish_gpu_memory_uncorrectable_row_remapping_count gauge

# HELP redfish_gpu_nvlink_bit_error_rate NVLink bit error rate
# TYPE redfish_gpu_nvlink_bit_error_rate gauge

# HELP redfish_gpu_nvlink_health NVLink port health,1(OK),2(Warning),3(Critical)
# TYPE redfish_gpu_nvlink_health gauge

# HELP redfish_gpu_nvlink_link_downed_count NVLink link downed count
# TYPE redfish_gpu_nvlink_link_downed_count gauge

# HELP redfish_gpu_nvlink_link_error_recovery_count NVLink error recovery count
# TYPE redfish_gpu_nvlink_link_error_recovery_count gauge

# HELP redfish_gpu_nvlink_runtime_error NVLink runtime error status (1 if error)
# TYPE redfish_gpu_nvlink_runtime_error gauge

# HELP redfish_gpu_nvlink_state NVLink port state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_gpu_nvlink_state gauge

# HELP redfish_gpu_nvlink_symbol_errors NVLink symbol error count
# TYPE redfish_gpu_nvlink_symbol_errors gauge

# HELP redfish_gpu_nvlink_training_error NVLink training error status (1 if error)
# TYPE redfish_gpu_nvlink_training_error gauge

# HELP redfish_gpu_sram_ecc_error_threshold_exceeded GPU SRAM ECC error threshold exceeded (1 if exceeded)
# TYPE redfish_gpu_sram_ecc_error_threshold_exceeded gauge

# HELP redfish_gpu_state GPU processor state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_gpu_state gauge

# HELP redfish_up redfish up
# TYPE redfish_up gauge
```

Exposes no user configuration.

### `<json_collector>`
[source](../collector/json_collector.go)

The JSON Collector allows probing Redfish APIs at a user-defined path, and deriving Prometheus gauge values from the result by use of JQ (using [gojq](https://github.com/itchyny/gojq) in particular).

The intent of this collector is to allow for greater end-user flexibility and agility in heterogeneous hardware environments.

OEM data is one good example of this; OEMs often evolve their own specific extensions and data faster than those updates percolate to Redfish libraries or even into publicly-available documentation. In those cases, the JSON collector may be a good fit to emit telemetry from arbitrary endpoints versus extending existing or adding new collectors to the core codebase to cover edge cases in OEM Redfish implementations.

Care should be taken when using this prober. Telemetry data is only as useful as it is both clear and understandable. Both of those criteria require finesse for what is at its core a transparent transformer around JSON data.

**NOTE:** Internally, the collector emits Gauge values. Care must be taken to collect and emit gauge-like data (power and fan data are good examples of gauges). Counter values _may_ work, but would not be canonically correct for use in Prometheus.

A recommended development cycle for this prober might be:
- Save the raw JSON output from a desired Redfish API path
- Iterate on the JQ script locally, targeting that saved output. See below for details on required JSON schema.
- Load the script and redfish path into a local copy of the redfish_exporter config (see below)
- Perform a single scrape through the exporter, backtesting against the same original source system
- Iterate on the script, if necessary

In order to transform data, the prober needs to execute a JQ script which yields data in the following output format:

``` json5
[
{
  # name of the resulting Prometheus timeseries
  "name": "<string>",
  # help text for the series. Prefer a static string, multiple values for a help string for the same timeseries name is invalid to Prometheus and will result in errors
  "help": "<string>",
  # value of the timeseries. Must be a float64.
  "value": <float64>,
  # OPTIONAL labels to assign to the timeseries.
  "labels": {
    "<string>": "<string>"
  }
},
...
]
```

This output is iterated over in the code, and gauge values are emitted as a result.

``` yaml
# Configurable timeout context, JQ parsing _must_ complete within this timeout.
context_timeout: <timeout> | default = 30s
# Redfish API path to probe
redfishPath: <string>
# JQ script to execute against the redfishPath. Note that the resulting format must match what is documented above.
jq: <string>
```

Examples of how this prober might be configured may be found in the [config testdata directory](../config/testdata/), and we welcome contributions adding more exotic ones.

### `<manager_collector>`
[source](../collector/manager_collector.go)

The Manager Collector exposes health data from the Manager API (e.g. the BMC itself). Against a Lambda lab system, the collector yields the following timeseries:

```
# HELP redfish_manager_health_state manager health,1(OK),2(Warning),3(Critical)
# TYPE redfish_manager_health_state gauge

# HELP redfish_manager_power_state manager power state
# TYPE redfish_manager_power_state gauge

# HELP redfish_manager_state manager state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_manager_state gauge
```

Exposes no user configuration.

### `<system_collector>`
[source](../collector/system_collector.go)

The System Collector primarily exposes health data from the Systems API, as well as some amount of capacity and PCIe data. Against a Lambda lab system, the collector yields the following timeseries:

```
# HELP redfish_exporter_collector_duration_seconds Collector time duration.
# TYPE redfish_exporter_collector_duration_seconds gauge

# HELP redfish_system_health_state system health,1(OK),2(Warning),3(Critical)
# TYPE redfish_system_health_state gauge

# HELP redfish_system_memory_capacity system memory capacity, MiB
# TYPE redfish_system_memory_capacity gauge

# HELP redfish_system_memory_health_state system memory health state,1(OK),2(Warning),3(Critical)
# TYPE redfish_system_memory_health_state gauge

# HELP redfish_system_memory_state system memory state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_system_memory_state gauge

# HELP redfish_system_power_state system power state
# TYPE redfish_system_power_state gauge

# HELP redfish_system_processor_cache_lifetime_correctable_ecc_error_count system processor cache lifetime correctable ECC error count
# TYPE redfish_system_processor_cache_lifetime_correctable_ecc_error_count gauge

# HELP redfish_system_processor_cache_lifetime_uncorrectable_ecc_error_count system processor cache lifetime uncorrectable ECC error count
# TYPE redfish_system_processor_cache_lifetime_uncorrectable_ecc_error_count gauge

# HELP redfish_system_processor_health_rollup system processor health rollup,1(OK),2(Warning),3(Critical)
# TYPE redfish_system_processor_health_rollup gauge

# HELP redfish_system_processor_health_state system processor health state,1(OK),2(Warning),3(Critical)
# TYPE redfish_system_processor_health_state gauge

# HELP redfish_system_processor_pcie_errors_correctable_count system processor PCIe correctable error count
# TYPE redfish_system_processor_pcie_errors_correctable_count gauge

# HELP redfish_system_processor_pcie_errors_fatal_count system processor PCIe fatal error count
# TYPE redfish_system_processor_pcie_errors_fatal_count gauge

# HELP redfish_system_processor_pcie_errors_l0_to_recovery_count system processor PCIe L0 to recovery state transition count
# TYPE redfish_system_processor_pcie_errors_l0_to_recovery_count gauge

# HELP redfish_system_processor_pcie_errors_nak_received_count system processor PCIe NAK received count
# TYPE redfish_system_processor_pcie_errors_nak_received_count gauge

# HELP redfish_system_processor_pcie_errors_nak_sent_count system processor PCIe NAK sent count
# TYPE redfish_system_processor_pcie_errors_nak_sent_count gauge

# HELP redfish_system_processor_pcie_errors_non_fatal_count system processor PCIe non-fatal error count
# TYPE redfish_system_processor_pcie_errors_non_fatal_count gauge

# HELP redfish_system_processor_pcie_errors_replay_count system processor PCIe replay count
# TYPE redfish_system_processor_pcie_errors_replay_count gauge

# HELP redfish_system_processor_pcie_errors_replay_rollover_count system processor PCIe replay rollover count
# TYPE redfish_system_processor_pcie_errors_replay_rollover_count gauge

# HELP redfish_system_processor_state system processor state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_system_processor_state gauge

# HELP redfish_system_processor_total_cores system processor total cores
# TYPE redfish_system_processor_total_cores gauge

# HELP redfish_system_processor_total_threads system processor total threads
# TYPE redfish_system_processor_total_threads gauge

# HELP redfish_system_state system state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_system_state gauge

# HELP redfish_system_storage_drive_capacity system storage drive capacity, Bytes
# TYPE redfish_system_storage_drive_capacity gauge

# HELP redfish_system_storage_drive_state system storage drive state,1(Enabled),2(Disabled),3(StandbyOffinline),4(StandbySpare),5(InTest),6(Starting),7(Absent),8(UnavailableOffline),9(Deferring),10(Quiesced),11(Updating)
# TYPE redfish_system_storage_drive_state gauge
```

Exposes no user configuration.

### `<telemetry_collector>`
[source](../collector/telemetry_collector.go)

The Telemetry Collector exposes a wealth of data using the TelemetryService API.
It was designed to capture much OEM data for Nvidia systems, and as such may not be less useful in non-GPU hardware environments.
Against a Lambda lab system, the collector yields the following timeseries:

```
# HELP redfish_gpu_temperature_tlimit_celsius GPU TLIMIT temperature headroom in Celsius
# TYPE redfish_gpu_temperature_tlimit_celsius gauge

# HELP redfish_telemetry_ambient_exhaust_temperature_celsius Ambient exhaust temperature in Celsius
# TYPE redfish_telemetry_ambient_exhaust_temperature_celsius gauge

# HELP redfish_telemetry_ambient_inlet_temperature_celsius Ambient inlet temperature in Celsius
# TYPE redfish_telemetry_ambient_inlet_temperature_celsius gauge

# HELP redfish_telemetry_bmc_temperature_celsius BMC temperature in Celsius
# TYPE redfish_telemetry_bmc_temperature_celsius gauge

# HELP redfish_telemetry_cache_ecc_correctable_total Total correctable ECC errors in GPU cache (L2/SRAM)
# TYPE redfish_telemetry_cache_ecc_correctable_total counter

# HELP redfish_telemetry_cache_ecc_uncorrectable_total Total uncorrectable ECC errors in GPU cache (L2/SRAM)
# TYPE redfish_telemetry_cache_ecc_uncorrectable_total counter

# HELP redfish_telemetry_collection_stale_reports_last Quantity of stale reports discovered on the last collection loop
# TYPE redfish_telemetry_collection_stale_reports_last gauge

# HELP redfish_telemetry_conventional_reset_entry_total Total conventional reset entry events
# TYPE redfish_telemetry_conventional_reset_entry_total counter

# HELP redfish_telemetry_conventional_reset_exit_total Total conventional reset exit events
# TYPE redfish_telemetry_conventional_reset_exit_total counter

# HELP redfish_telemetry_cpu_edp_current_limit_watts CPU current EDP (Electrical Design Point) limit in watts
# TYPE redfish_telemetry_cpu_edp_current_limit_watts gauge

# HELP redfish_telemetry_cpu_edp_peak_limit_watts CPU peak EDP (Electrical Design Point) limit in watts
# TYPE redfish_telemetry_cpu_edp_peak_limit_watts gauge

# HELP redfish_telemetry_cpu_energy_joules_total Total CPU energy consumption in joules
# TYPE redfish_telemetry_cpu_energy_joules_total counter

# HELP redfish_telemetry_cpu_power_watts CPU power consumption in watts
# TYPE redfish_telemetry_cpu_power_watts gauge

# HELP redfish_telemetry_cpu_temperature_average_celsius Average CPU temperature in Celsius
# TYPE redfish_telemetry_cpu_temperature_average_celsius gauge

# HELP redfish_telemetry_cpu_temperature_limit_celsius CPU temperature limit in Celsius
# TYPE redfish_telemetry_cpu_temperature_limit_celsius gauge

# HELP redfish_telemetry_fundamental_reset_entry_total Total fundamental reset entry events
# TYPE redfish_telemetry_fundamental_reset_entry_total counter

# HELP redfish_telemetry_fundamental_reset_exit_total Total fundamental reset exit events
# TYPE redfish_telemetry_fundamental_reset_exit_total counter

# HELP redfish_telemetry_gpu_memory_temperature_celsius GPU memory temperature in Celsius
# TYPE redfish_telemetry_gpu_memory_temperature_celsius gauge

# HELP redfish_telemetry_gpu_temperature_celsius GPU core temperature in Celsius
# TYPE redfish_telemetry_gpu_temperature_celsius gauge

# HELP redfish_telemetry_hardware_violation_throttle_duration_seconds_total Total time GPU was throttled due to hardware violations
# TYPE redfish_telemetry_hardware_violation_throttle_duration_seconds_total counter

# HELP redfish_telemetry_irot_reset_exit_total Total IRoT (Internal Root of Trust) reset exit events
# TYPE redfish_telemetry_irot_reset_exit_total counter

# HELP redfish_telemetry_last_reset_type_info Last reset type (1=Conventional, 2=Fundamental, 3=IRoT, 4=PF_FLR)
# TYPE redfish_telemetry_last_reset_type_info gauge

# HELP redfish_telemetry_memory_bandwidth_percent Memory bandwidth utilization percentage
# TYPE redfish_telemetry_memory_bandwidth_percent gauge

# HELP redfish_telemetry_memory_capacity_utilization_percent Memory capacity utilization percentage
# TYPE redfish_telemetry_memory_capacity_utilization_percent gauge

# HELP redfish_telemetry_memory_ecc_correctable_lifetime_total Lifetime correctable DRAM ECC errors
# TYPE redfish_telemetry_memory_ecc_correctable_lifetime_total counter

# HELP redfish_telemetry_memory_ecc_uncorrectable_lifetime_total Lifetime uncorrectable DRAM ECC errors
# TYPE redfish_telemetry_memory_ecc_uncorrectable_lifetime_total counter

# HELP redfish_telemetry_memory_operating_speed_mhz Memory operating speed in MHz
# TYPE redfish_telemetry_memory_operating_speed_mhz gauge

# HELP redfish_telemetry_nvidia_dmma_utilization_percent Double precision Matrix Multiply-Accumulate utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_dmma_utilization_percent gauge

# HELP redfish_telemetry_nvidia_fp16_activity_percent FP16 floating point activity percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_fp16_activity_percent gauge

# HELP redfish_telemetry_nvidia_fp32_activity_percent FP32 floating point activity percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_fp32_activity_percent gauge

# HELP redfish_telemetry_nvidia_fp64_activity_percent FP64 floating point activity percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_fp64_activity_percent gauge

# HELP redfish_telemetry_nvidia_graphics_engine_activity_percent Graphics engine activity percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_graphics_engine_activity_percent gauge

# HELP redfish_telemetry_nvidia_hmma_utilization_percent Half precision Matrix Multiply-Accumulate utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_hmma_utilization_percent gauge

# HELP redfish_telemetry_nvidia_imma_utilization_percent Integer Matrix Multiply-Accumulate utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_imma_utilization_percent gauge

# HELP redfish_telemetry_nvidia_integer_activity_percent Integer operation activity percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_integer_activity_percent gauge

# HELP redfish_telemetry_nvidia_nvdec_instance_utilization_percent Video decoder instance utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvdec_instance_utilization_percent gauge

# HELP redfish_telemetry_nvidia_nvdec_utilization_percent Video decoder overall utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvdec_utilization_percent gauge

# HELP redfish_telemetry_nvidia_nvjpg_instance_utilization_percent JPEG decoder instance utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvjpg_instance_utilization_percent gauge

# HELP redfish_telemetry_nvidia_nvjpg_utilization_percent JPEG decoder overall utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvjpg_utilization_percent gauge

# HELP redfish_telemetry_nvidia_nvlink_data_rx_bandwidth_gbps NVLink data receive bandwidth in Gbps (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvlink_data_rx_bandwidth_gbps gauge

# HELP redfish_telemetry_nvidia_nvlink_data_tx_bandwidth_gbps NVLink data transmit bandwidth in Gbps (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvlink_data_tx_bandwidth_gbps gauge

# HELP redfish_telemetry_nvidia_nvlink_raw_rx_bandwidth_gbps NVLink raw receive bandwidth in Gbps including overhead (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvlink_raw_rx_bandwidth_gbps gauge

# HELP redfish_telemetry_nvidia_nvlink_raw_tx_bandwidth_gbps NVLink raw transmit bandwidth in Gbps including overhead (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvlink_raw_tx_bandwidth_gbps gauge

# HELP redfish_telemetry_nvidia_nvofa_utilization_percent NVIDIA Optimized Fabrics Adapter utilization (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_nvofa_utilization_percent gauge

# HELP redfish_telemetry_nvidia_pcie_raw_rx_bandwidth_gbps PCIe raw receive bandwidth in Gbps (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_pcie_raw_rx_bandwidth_gbps gauge

# HELP redfish_telemetry_nvidia_pcie_raw_tx_bandwidth_gbps PCIe raw transmit bandwidth in Gbps (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_pcie_raw_tx_bandwidth_gbps gauge

# HELP redfish_telemetry_nvidia_sm_activity_percent Streaming Multiprocessor activity percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_sm_activity_percent gauge

# HELP redfish_telemetry_nvidia_sm_occupancy_percent Streaming Multiprocessor occupancy percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_sm_occupancy_percent gauge

# HELP redfish_telemetry_nvidia_tensor_core_activity_percent Tensor core activity percentage (NVIDIA GPM)
# TYPE redfish_telemetry_nvidia_tensor_core_activity_percent gauge

# HELP redfish_telemetry_pcie_correctable_errors_total Total PCIe correctable errors
# TYPE redfish_telemetry_pcie_correctable_errors_total counter

# HELP redfish_telemetry_pcie_fatal_errors_total Total PCIe fatal errors
# TYPE redfish_telemetry_pcie_fatal_errors_total counter

# HELP redfish_telemetry_pcie_l0_to_recovery_total Total PCIe L0 to recovery transitions
# TYPE redfish_telemetry_pcie_l0_to_recovery_total counter

# HELP redfish_telemetry_pcie_nak_received_total Total PCIe NAK received
# TYPE redfish_telemetry_pcie_nak_received_total counter

# HELP redfish_telemetry_pcie_nak_sent_total Total PCIe NAK sent
# TYPE redfish_telemetry_pcie_nak_sent_total counter

# HELP redfish_telemetry_pcie_nonfatal_errors_total Total PCIe non-fatal errors
# TYPE redfish_telemetry_pcie_nonfatal_errors_total counter

# HELP redfish_telemetry_pcie_replay_rollover_total Total PCIe replay rollover events
# TYPE redfish_telemetry_pcie_replay_rollover_total counter

# HELP redfish_telemetry_pcie_replay_total Total PCIe replay events
# TYPE redfish_telemetry_pcie_replay_total counter

# HELP redfish_telemetry_pf_flr_reset_entry_total Total PF FLR (Physical Function Function-Level Reset) entry events
# TYPE redfish_telemetry_pf_flr_reset_entry_total counter

# HELP redfish_telemetry_pf_flr_reset_exit_total Total PF FLR (Physical Function Function-Level Reset) exit events
# TYPE redfish_telemetry_pf_flr_reset_exit_total counter

# HELP redfish_telemetry_port_nvidia_intentional_link_down_count_total Total intentional link down events (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_intentional_link_down_count_total counter

# HELP redfish_telemetry_port_nvidia_neighbor_mtu_discards_total Total neighbor MTU discards (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_neighbor_mtu_discards_total counter

# HELP redfish_telemetry_port_nvidia_qp1_dropped_total Total QP1 packets dropped (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_qp1_dropped_total counter

# HELP redfish_telemetry_port_nvidia_rx_no_protocol_bytes_total Total RX bytes without protocol (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_rx_no_protocol_bytes_total counter

# HELP redfish_telemetry_port_nvidia_rx_remote_physical_errors_total Total RX remote physical errors (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_rx_remote_physical_errors_total counter

# HELP redfish_telemetry_port_nvidia_rx_switch_relay_errors_total Total RX switch relay errors (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_rx_switch_relay_errors_total counter

# HELP redfish_telemetry_port_nvidia_total_raw_ber Total raw bit error rate (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_total_raw_ber gauge

# HELP redfish_telemetry_port_nvidia_tx_no_protocol_bytes_total Total TX bytes without protocol (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_tx_no_protocol_bytes_total counter

# HELP redfish_telemetry_port_nvidia_tx_wait_total Total TX wait time (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_tx_wait_total counter

# HELP redfish_telemetry_port_nvidia_unintentional_link_down_count_total Total unintentional link down events (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_unintentional_link_down_count_total counter

# HELP redfish_telemetry_port_nvidia_vl15_dropped_total Total VL15 packets dropped (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_vl15_dropped_total counter

# HELP redfish_telemetry_port_nvidia_vl15_tx_bytes_total Total VL15 bytes transmitted (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_vl15_tx_bytes_total counter

# HELP redfish_telemetry_port_nvidia_vl15_tx_packets_total Total VL15 packets transmitted (NVIDIA OEM)
# TYPE redfish_telemetry_port_nvidia_vl15_tx_packets_total counter

# HELP redfish_telemetry_port_rx_bytes_total Total bytes received on port
# TYPE redfish_telemetry_port_rx_bytes_total counter

# HELP redfish_telemetry_port_rx_errors_total Total receive errors on port
# TYPE redfish_telemetry_port_rx_errors_total counter

# HELP redfish_telemetry_port_rx_frames_total Total frames received on port
# TYPE redfish_telemetry_port_rx_frames_total counter

# HELP redfish_telemetry_port_tx_bytes_total Total bytes transmitted on port
# TYPE redfish_telemetry_port_tx_bytes_total counter

# HELP redfish_telemetry_port_tx_discards_total Total transmit discards on port
# TYPE redfish_telemetry_port_tx_discards_total counter

# HELP redfish_telemetry_port_tx_frames_total Total frames transmitted on port
# TYPE redfish_telemetry_port_tx_frames_total counter

# HELP redfish_telemetry_power_throttle_duration_seconds_total Total time GPU was throttled due to power limits
# TYPE redfish_telemetry_power_throttle_duration_seconds_total counter

# HELP redfish_telemetry_software_violation_throttle_duration_seconds_total Total time GPU was throttled due to software violations
# TYPE redfish_telemetry_software_violation_throttle_duration_seconds_total counter

# HELP redfish_telemetry_thermal_throttle_duration_seconds_total Total time GPU was throttled due to thermal limits
# TYPE redfish_telemetry_thermal_throttle_duration_seconds_total counter
```

Exposes no user configuration.

# Extra

## Regenerating the above metrics HELP/TYPE

Maybe a good automation target? Until then, for a given `${TARGET}` representing expected output and given a specific `${MODULE}`, GNU AWK will save the day for wholesale replacing of the above examples:

```shell
curl -s "http://localhost:9610/redfish?target=${TARGET}&module=${MODULE}" | awk '/^# (TYPE|HELP)/ { if (/^# TYPE/) {print; print ""} else {print} }'
```
