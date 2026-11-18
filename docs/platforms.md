# Platforms: one code path per server type

This is the specification of how the exporter routes its reads per server type. The
configuration reference for the `platforms:` block is in
[CONFIGURATION.md](CONFIGURATION.md#platforms).

This repository ships the mechanism and only the `default` row, with its reads. Rows for
named server types, and the reads only they use, are compiled in from outside the repository
with `-tags private_platforms`; [Build tags](#build-tags-rows-kept-outside-this-repository)
says how. The examples below use placeholder keys such as `EXAMPLE-SKU-1.02.03`.

## Why

Redfish is a standard, and most of what the exporter reads is the same on every BMC. The
exceptions are where the work is. Some BMCs refuse any query string on a GPU's `Ports`
collection, so the expanded NVLink read fails on every GPU; the same kind of BMC may no longer
serve the deprecated `Power` resource at all. Others refuse `$expand` with a different status,
expose the GPU baseboard's chassis slowly through a proxy, or publish no `HGX_*` metric
reports, so their GPU telemetry has to come from per-GPU resources.

Until now each of those differences was handled where it was found: a flag on a collector
(to skip the NVLink expand, to exclude chassis, to switch the GPU telemetry source), or a
try-then-fall-back inside the read itself. Flags have to be set per deployment and only make
sense in the right combination; fallbacks spend a refused request per GPU per scrape to learn
what the BMC could have told us up front; and neither says, anywhere an operator can look,
which path a host is on.

A platform gives that knowledge one place. A server type is named by one key, the scrape job
names the key and the modules for each host, and a Go table names, per server type, the code
that performs each read that differs. A collector is handed what it needs and never decides
for itself.

## Vocabulary

| term | meaning |
|---|---|
| server type | one model of host as the exporter sees it: a BMC that behaves one way. This means SKU+FW are considered one server type |
| platform key | the name of a server type, `<server_sku>-<firmware_version>`, for example `EXAMPLE-SKU-1.02.03` |
| module | a named entry under `modules:` in `config.yml`; the unit a scrape asks for |
| prober | the collector type a module is backed by, for example `gpu_collector` |
| collector | the Go type that reads one area of a BMC and emits its metric families |
| row | the Go table's entry for a platform: a `Platform` value, `Default` in `internal/collector/platform.go` or a row compiled in with `-tags private_platforms` |
| cell | a function-typed field of a row naming the function that performs one read that differs by server type |
| read | a function a cell can name, named for the Redfish behaviour it handles: one of `Default`'s, here, or one written beside a row outside this repository |
| map entry | a scrape job's per-server-type entry: an inventory device type, its key and its modules |
| stanza | a `platforms:` entry for a platform in `config.yml`: modules that override the request's; a deployment may declare none |
| default | the platform every scrape ran as before platforms existed; one expanded read of the Ports collection per GPU |

## The two declarations of a platform

A platform exists in two places, joined by its key.

```
== the scrape job: one map entry per server type ==
+--------------------------------------------------------------------------------------------------+
| platforms map:                                                                                   |
|   - device_type: "EXAMPLE-SKU-01"                 matched against the inventory's device type    |
|     platform: EXAMPLE-SKU-1.02.03                 <-- the key                                    |
|     modules: [chassis, gpu, system, manager, telemetry]                                          |
|                                                                                                  |
| each host of that type is scraped with                                                           |
|   platform=<key>&module=chassis,gpu,system,manager,telemetry                                     |
|                                                                                                  |
| each module is defined under modules: in the exporter's config.yml:                              |
|   chassis: {prober: chassis_collector}   gpu: {prober: gpu_collector}   ...                      |
+--------------------------------------------------------------------------------------------------+
                       | same key, compared case-insensitively
                       v
== the platform table (compiled into the image) ==
+--------------------------------------------------------------------------------------------------+
| platforms map, keyed by lowercased Name: Default plus extraPlatforms                             |
|                                                                                                  |
|  Default  {Name: "default",                     internal/collector/platform.go                   |
|            NVLink: nvlinkPortsExpand,     <------ a cell                                         |
|            ChassisPower: collectPower,                                                           |
|            ChassisSensors: skipSensors,                                                          |
|            ChassisLeakDetection: getLeakDetectors,                                               |
|            ChassisTemperatures: collectThermalMetrics,                                           |
|            ChassisList: listChassis}                                                             |
|                                                                                                  |
|  ExampleSKU {                                   <------ a row from platform_<type>_private.go    |
|            Name: "EXAMPLE-SKU-1.02.03",                 (-tags private_platforms only)           |
|            NVLink: nvlinkProcessorExpand, <------ a read written beside the row                  |
|            ChassisPower: collectPower,    <------ or one of Default's, by name                   |
|            ChassisSensors: skipSensors,                                                          |
|            ChassisLeakDetection: getLeakDetectors,                                               |
|            ChassisTemperatures: collectThermalMetrics,                                           |
|            ChassisList: listChassis}                                                             |
+--------------------------------------------------------------------------------------------------+
```

The request's modules, or a stanza when one is declared, decide **which** collectors run for
that server type. The row decides **how** those collectors read: either one function per
differing read (a cell) or a whole collector type for a prober (a `Collectors` entry).
Neither gates the other: a request that names a key the table has runs that row's code
whether or not a stanza exists, and a stanza whose key the table lacks runs the default code.
Module and prober names are the same for every platform; only what is behind them changes.

At config load, every stanza must list at least one module and every module it lists must
exist under `modules:`, or the config is rejected. A stanza whose key the running build's
table does not know is logged at error level and scrapes of it run as `default`, so a typo
or a missing row is visible without breaking the scrape. The table is in the image
fleet-wide. What puts a host on a row is the scrape job that sends `platform=<key>`, so a
new server type is rolled out one scrape job at a time: its entry in the job names the key and
the modules. A deployment need declare no stanza; one declared later overrides the job's
modules for that key without a change to the scrape jobs.

## Build tags: rows kept outside this repository

A row's key names a server SKU and a firmware version, and which reads a key gets says what
hardware a deployment runs. Neither belongs in a public repository, so the table is built
from two parts:

```go
var platforms = platformTable(append([]Platform{Default}, extraPlatforms...))
```

- `platform_rows.go` carries `//go:build !private_platforms && !platform_examples` and defines
  `extraPlatforms` as empty. A plain `go build` ships `default` alone.
- A build with `-tags private_platforms` leaves that file out and compiles `*_private.go`
  files dropped into `internal/collector/` from outside the repository: one defines the real
  `extraPlatforms` list, and one per server type defines that type's row. Being in package
  `collector`, a row names unexported reads, `Default`'s or its own, with no exports, and its
  own reads call the collectors' unexported helpers the way `Default`'s do.
- The image builder keeps those files in its own `platforms/` directory, copies them into the
  cloned source, and builds and tests with the tag. See the make ci and the examples to
  understand how this works. A private build sets private_platforms and not platform_examples, 
  which leaves the example files out.

## The scrape request

```
GET /redfish?target=<bmc-ip>&group=<creds-group>&platform=<key>[&module=<name>[,<name>...]...]
```

`platform` is optional. Precedence for the modules a scrape runs:

| request | modules used |
|---|---|
| `platform=` names a key with a stanza | the stanza's modules; any `module=` on the request is ignored with a warning |
| `platform=` names a key without a stanza, or no `platform=` | the request's `module=` parameters |
| no stanza applies and no `module=` | the built-in bundle: the gpu, chassis, manager, system and telemetry probers with built-in options, independent of `modules:` and of any stanza |

The row is chosen by the same key, independently: a `platform=` the table does not know runs
as `default`, logs a warning, and increments
`redfish_exporter_unknown_platforms_requested_total`. Keys are compared case-insensitively
because the config loader lowercases map keys; the row's `Name` keeps the spelling that
appears in metrics and logs.

Every scrape that reaches collection reports what it ran as, in the scrape output:

```
redfish_exporter_platform_info{platform="EXAMPLE-SKU-1.02.03",requested="EXAMPLE-SKU-1.02.03"} 1
```

`platform` is the row that ran, spelled as the row's `Name`; `requested` is the `platform=`
value exactly as the request sent it, empty when it sent none. `platform="default"` with a
non-empty `requested` is a request that named a key the table does not have, and the same
scrape increments the unknown-platforms counter. `requested=""` is a job that names no
platform.

### One request through the code

The same path for one host, with the code at each step. The server type, its key, its read
and the address are made up: the row names `nvlinkProcessorExpand`, a read written beside
it, as its NVLink cell, and a build whose table has no such row runs the request as `default`.

```
+--------------------------------------------------------------------------------------------------+
| [1] request from the scraping collector                                                          |
|                                                                                                  |
| GET /redfish?group=secure&module=...&platform=SAMPLE-SKU-0.0.0&target=192.0.2.10                 |
+--------------------------------------------------------------------------------------------------+
          |
          | query string
          v
+--------------------------------------------------------------------------------------------------+
| [2] cmd/redfish-exporter/main.go: read the request, pick the modules                             |
|                                                                                                  |
| target := urlQuery.Get("target")                       // 192.0.2.10                             |
| platformName := urlQuery.Get("platform")               // SAMPLE-SKU-0.0.0                       |
| modules := modulesFor(platformName, urlQuery["module"], safeConfig, ctxLogger)                   |
|                                                                                                  |
| func modulesFor(platformName string, requested []string, ...) []string {                         |
|     requested = splitModules(requested)   // ["chassis,gpu"] -> [chassis gpu]                    |
|                                           // module= accepts comma-separated values:             |
|                                           // module=chassis,gpu runs the same as                 |
|                                           // module=chassis&module=gpu                           |
|     if modules, ok := sc.PlatformModules(platformName); ok && len(modules) > 0 {                 |
|         return modules                // the stanza's modules win over module=                   |
|     }                                                                                            |
|     ...                                                                                          |
+--------------------------------------------------------------------------------------------------+
          |
          | platform name, modules
          v
+--------------------------------------------------------------------------------------------------+
| [3] main.go + internal/collector/platform.go: pick the row                                       |
|                                                                                                  |
| platform, known := collector.PlatformByName(sr.Platform)                                         |
| collectors := buildCollectorsFor(ctx, sr.Modules, ..., platform)                                 |
|                                                                                                  |
| func PlatformByName(name string) (Platform, bool) {                                              |
|     if p, ok := platforms[strings.ToLower(name)]; ok { return p, true }                          |
|     return Default, false                                                                        |
| }                                                                                                |
|                                                                                                  |
| var SampleSKU = Platform{                          // the row it finds, in a private row file    |
|     Name:   "SAMPLE-SKU-0.0.0",                                                                  |
|     NVLink: (*GPUCollector).nvlinkProcessorExpand,                                               |
| }                                                                                                |
+--------------------------------------------------------------------------------------------------+
          |
          | once per module, the same row each time
          v
+--------------------------------------------------------------------------------------------------+
| [4] internal/collector/collector.go: build one GPUCollector, hand it the row                     |
|                                                                                                  |
| func NewCollectorFromModule(moduleName string, m *config.Module, ..., platform Platform) ... {   |
|     construct := newCollectorFromModule              // usual switch: gpu -> NewGPUCollector     |
|     if override, ok := platform.Collectors[m.Prober]; ok {                                       |
|         construct = override                         // whole-collector swap: none here          |
|     }                                                                                            |
|     c, err := construct(moduleName, m, rfClient, logger)                                         |
|                                // c points at a new GPUCollector{nvlink: nvlinkPortsExpand}      |
|                                // still Default's cell: nothing has been swapped yet             |
|     if aware, ok := c.(PlatformAware); ok {                                                      |
|         aware.SetPlatform(platform)  // this call does the swap: SetPlatform, in [5], sets       |
|                                      // c's nvlink to the row's NVLink. aware is the SAME        |
|                                      // pointer as c, so it changes c itself, not a copy         |
|     }                                                                                            |
|     return c, nil                    // the same pointer goes back to the caller                 |
| }                                                                                                |
+--------------------------------------------------------------------------------------------------+
          |
          | SetPlatform runs on that one GPUCollector
          v
+--------------------------------------------------------------------------------------------------+
| [5] internal/collector/gpu_collector.go: the swap, written into the struct itself                |
|                                                                                                  |
| type GPUCollector struct {                                                                       |
|     nvlink func(*GPUCollector, context.Context, SystemGPU) (GPUNVLinkCollection, error)          |
| }                                                                                                |
|                                                                                                  |
| nvlink: Default.NVLink,               // constructor: starts as nvlinkPortsExpand                |
|                                                                                                  |
| func (g *GPUCollector) SetPlatform(p Platform) {  // pointer receiver: g IS that GPUCollector    |
|     g.nvlink = p.NVLink               // now nvlinkProcessorExpand     <-- replaced              |
| }                                                                                                |
|                                                                                                  |
|     +----------------------------------------------+                                             |
|     | the GPUCollector in memory, from now on      |                                             |
|     |   nvlink = nvlinkProcessorExpand             |                                             |
|     +----------------------------------------------+                                             |
+--------------------------------------------------------------------------------------------------+
          |
          | the pointer is kept, nothing copies the struct
          v
+--------------------------------------------------------------------------------------------------+
| [6] main.go: the pointer is stored and the scrape is served                                      |
|                                                                                                  |
| c = append(c, collector)                          // buildCollectorsFor                          |
| aggregateCollector.WithCollectors(collectors)     // metricsHandler                              |
| registry.MustRegister(aggregateCollector)                                                        |
| h.ServeHTTP(w, r)                                 // asks every collector for metrics            |
+--------------------------------------------------------------------------------------------------+
          |
          | the registry calls the aggregate collector's Collect
          v
+--------------------------------------------------------------------------------------------------+
| [7] internal/collector/redfish_collector.go: each stored pointer is called                       |
|                                                                                                  |
| for _, collector := range r.collectors {                                                         |
|     collector.CollectWithContext(r.ctx, ch)       // the same GPUCollector as in [4]             |
| }                                                                                                |
+--------------------------------------------------------------------------------------------------+
          |
          | its collection loop, the same for every platform, once per GPU
          v
+--------------------------------------------------------------------------------------------------+
| [8] gpu_collector.go: call whatever is in the field                                              |
|                                                                                                  |
| func (g *GPUCollector) emitNVLinkTelemetry(ctx context.Context, ch ..., gpu SystemGPU) {         |
|     agg, err := g.nvlink(g, ctx, gpu)  // reads the field [5] wrote: nvlinkProcessorExpand       |
|     ...                                                                                          |
| }                                                                                                |
|                                                                                                  |
| the row's own read, written beside it, for a BMC that refuses Ports queries:                     |
| func (g *GPUCollector) nvlinkProcessorExpand(ctx context.Context, gpu SystemGPU) ... {           |
|     var processor struct{ Ports GPUNVLinkCollection `json:"Ports"` }                             |
|     err := getJSON(..., fmt.Sprintf(`%s?$expand=.($levels=3)`, gpu.ODataID), &processor)         |
|     return processor.Ports, err                                                                  |
| }                                                                                                |
+--------------------------------------------------------------------------------------------------+
          |
          | Redfish over HTTPS, with the secure group's BMC creds
          v
+--------------------------------------------------------------------------------------------------+
| [9] BMC 192.0.2.10                                                                               |
|                                                                                                  |
| GET /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_0?$expand=.($levels=3)                    |
|     -> 200, ports inline                                                                         |
|                                                                                                  |
| the default row would have sent:                                                                 |
| GET /redfish/v1/Systems/HGX_Baseboard_0/Processors/GPU_0/Ports?$expand=.($levels=2)              |
|     -> 404                                                                                       |
+--------------------------------------------------------------------------------------------------+
```

- **There is only one collector object.** `c`, `aware`, the entry in the collectors slice
  and the `collector` in the aggregate's loop are the same pointer. Viewing it as
  `PlatformAware` does not copy it.
- **The write sticks.** `SetPlatform` has a pointer receiver, so `g.nvlink = p.NVLink`
  changes that object, and nothing writes the field again.
- **It lasts one scrape.** Steps [2] to [8] run inside one HTTP request. The next scrape
  builds fresh collectors and sets the field from the row that request names.

### Cells

A cell is usually a method expression, `(*GPUCollector).nvlinkPortsExpand`, stored in a
field of function type. The collector calls the field, passing itself as the receiver:

```go
agg, err := g.nvlink(g, ctx, gpu)   // == g.nvlinkPortsExpand(ctx, gpu) on a row whose NVLink cell names that read
```

A row that replaces a collector must still fill every function cell.

A cell is the right tool when one read differs and the rest of the collector is right. Six
cells exist so far:

| cell | what it reads | `Default`'s read |
|---|---|---|
| `NVLink` | a GPU's NVLink ports | `nvlinkPortsExpand`: the GPU's `Ports` collection, expanded |
| `ChassisPower` | a chassis' power supplies, voltages and consumed watts | `collectPower`: the legacy `Power` resource |
| `ChassisSensors` | the families the legacy `Thermal` and `Power` resources carried, from a chassis' `Sensors` | `skipSensors`: nothing |
| `ChassisLeakDetection` | the leak detectors in a chassis' `ThermalSubsystem` | `getLeakDetectors`: the linked collection, then the guessed `ThermalSubsystem/LeakDetection` URL for OEMs that serve one object there, whether or not it is linked |
| `ChassisTemperatures` | the temperatures in a chassis' `ThermalSubsystem` | `collectThermalMetrics`: every chassis' `ThermalMetrics` |
| `ChassisList` | the chassis the chassis collector walks | `listChassis`: every chassis the service lists |

A read that can meet the same series in the reads of two chassis writes it through the chassis
collector's `writeOnce`, since a repeated series fails the scrape.

### Whole collectors

A `Collectors` entry replaces the type behind a prober for that platform. The replacement
embeds the default collector, so it inherits the metric map, `Describe`, discovery, and every
emitter, and owns only what differs. 

## What platforms do not decide

- **Client concurrency and timeouts.** gofish fixes `max_concurrent_requests` when the
  session opens (`NewRedfishCollector`), before any read, and the only other per-request
  limit is whatever proxy the deployment puts in front of the exporter. Both stay in the
  deployment's configuration; neither is per platform.
- **Module names.** They are chosen per deployment in `config.yml`; the Go table only ever
  refers to prober names.

## Keys

`<server_sku>-<firmware_version>`, for example `EXAMPLE-SKU-1.02.03`.

- **The SKU half** is the server type as the inventory names it, without any per-order
  suffix. It is a label chosen by people, not a string a BMC reports: a BMC may report a
  board name as its model and a placeholder such as `To be filled by O.E.M.` as its SKU.
- **The firmware half** is the host BMC firmware the row was validated against, as the
  vendor stamps it, for example the BMC image name the firmware inventory
  (`/redfish/v1/UpdateService/FirmwareInventory/BMC`) reports, less its file extension. It is
  a literal in the scrape job, because the inventory does not know firmware. While a row is
  being brought up, `0.0.0` marks the firmware as not yet settled.
- **A firmware roll that changes behaviour** is a new key: a new row and map entry for the
  new line, and jobs move hosts to it as they are flashed. A roll that changes nothing is a
  key edit if the operator wants the label truthful, or nothing at all; the exporter does
  not read firmware and will not notice.
- **Case** does not matter when keys are compared.
- **Dots** are preserved. The config loader's key delimiter is `::`, not viper's default
  `.`, so a firmware version is one key and not a nest of keys. The same fix makes per-host
  entries keyed by IP work.

## Adding a server type

A new server type is a row in the private build plus its entry in the scrape job. Nothing in
this repository changes. The example files in `internal/collector/`, compiled and tested by
`make platform-examples-test`, show each case:

| the new type needs | add, beside its row in the image builder's `platforms/` | example |
|---|---|---|
| only reads that already exist | a row whose cells name them, listed in `extraPlatforms` | `platform_example_sku_b_test.go` |
| a read that does not exist yet | the read, named for the Redfish behaviour it handles, and a row that names it | `chassis_collector_example_power_test.go` |
| a collector of its own | a constructor named in the row's `Collectors` map, keyed by prober; every cell is still filled | `system_collector_example_test.go` |
| nothing new on newer firmware | a copy of the row with the new key | `platform_example_sku_a_fw2_test.go` |
| a module no other type runs | the module under `modules:` in `config.yml`, listed only in that type's map entry | |


Two changes belong in this repository instead: a read every BMC should get, which is a change
to `Default`, and a new prober, which needs its collector, its case in
`newCollectorFromModule`, its options on `config.Module` and its name under `<prober_string>`
in CONFIGURATION.md.

Before adding a cell or a collector, check a captured BMC response. If the difference shows in
a link or a field of a resource the exporter already reads, the existing code follows it and
no row is needed, as with `Thermal` and `ThermalSubsystem`.

## What lives where

| piece | where |
|---|---|
| the mechanism, `Default` and its reads, the example rows, the fixture BMCs and test helpers | this repository |
| rows for named server types, the reads and collectors only they use, their tests and captures | the image builder's `platforms/`, compiled in with `-tags private_platforms` |
| device type to key and modules | the scrape jobs |
| module definitions, and stanzas if any | each deployment's `config.yml` |
