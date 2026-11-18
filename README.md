# redfish_exporter

A Prometheus multi-target exporter to fetch metrics from Redfish-capable hardware.
Forked from https://github.com/FlxPeters/redfish_exporter, see [Why a fork](#why-a-fork) for rationale.

## Configuration

Configuration of the redfish_exporter is done via YAML file.

See [CONFIGURATION.md](./docs/CONFIGURATION.md) for details.

Per-server-type routing (the `platforms:` block and the `platform` query parameter) is specified in [docs/platforms.md](./docs/platforms.md).

## Building

To build the redfish_exporter executable:

```sh
make build
```

At the current time, this fork does not publish its own container images. We hope to in the near future.

## Running

### Running directly on Linux

The exporter can run directly on Linux as a binary:

```sh
redfish_exporter -config.file=redfish_exporter.yaml
```
Run `redfish_exporter -h` for more options.

### Running in a container

If using an image, an invocation like the following should also work:

``` shell
# For Podman users
podman run -v $(pwd)/redfish_exporter.yaml:/config.yml -p 9610:9610  some-image-reference:<some-image-tag> -config.file="/config.yml"
# For Docker users
docker run -v $(pwd)/redfish_exporter.yaml:/config.yml -p 9610:9610  some-image-reference:<some-image-tag> -config.file="/config.yml"
```

## Scraping

Clients - like Prometheus, your browser, or cURL - collect Redfish metrics from a system via the `/redfish` endpoint and at minimum, a `target` parameter:

```sh
curl -s http://<redfish_exporter host>:9610/redfish?target=<ip>
```

If using [modules](#configuration), clients would specify one or more `module` parameters:

``` shell
# Single module
curl -s "http://localhost:9610/redfish?target=<ip>&module=telemetry"
# Multiple modules, where 'rf_version' is possibly a custom JSON collector
curl -s "http://localhost:9610/redfish?target=<ip>&module=telemetry&module=rf_version"
```

## Reloading Configuration

```
PUT /-/reload
POST /-/reload
```

The `/-/reload` endpoint triggers a reload of the redfish_exporter configuration.
500 will be returned when the reload fails.

Alternatively, a configuration reload can be triggered by sending `SIGHUP` to the redfish_exporter process as well.

## Prometheus Configuration

Prometheus scrapes targets using something like the following scrape job configuration:

```yaml
  - job_name: 'redfish-exporter'

    # metrics_path defaults to '/metrics'
    metrics_path: /redfish
    
    # params below will apply to all targets in the scrape job. Useful in homogeneous hardware deployments (like job-by-hardware-type)
    params:
    # (optional) when using modules
      module: [telemetry, gpu]
      # (optional) when using group config
      group: [foo]
    # scheme defaults to 'http'.
    scheme: http
    
    static_configs:
    - targets:
       - <ip> # List of IPs to collect Redfish metrics from
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: localhost:9610  # The address of the redfish-exporter
      # NOTE: If certain params (like module or group) may be variable per host in the job, often depending on service discovery, do not use 'params' above but instead add relabels like so:
      - target_label: __param_group
        replacement: "foo" # likely a regex and using service discovery data
      - target_label: __param_module
        replacement: "foobar"
```

## Testing and Development

### Mock Server

We provide a mock Redfish server for testing the exporter without requiring actual hardware:

```shell
# Test with specific captured data
make mock-test TESTDATA=my_system/capture.txt
```

This starts both a mock Redfish server (with HTTP and HTTPS support) and the exporter configured to scrape it.

### Capturing Real Hardware Data

To capture data from real hardware for testing:

```shell
# Capture Redfish responses from a BMC
make capture HOST=10.0.0.100 USER=admin PASS=password OUTPUT=my_system

# Then test with the captured data
make mock-test TESTDATA=my_system/capture.txt
```

See `tools/mock-server/README.md` and `tools/capture/README.md` for detailed documentation.

## Supported Devices (tested)

Prior to the fork (should also still work post-fork):

- Enginetech EG520R-G20 (Supermicro Firmware Revision 1.76.39)
- Enginetech EG920A-G20 (Huawei iBMC 6.22)
- Lenovo ThinkSystem SR850 (BMC 2.1/2.42)
- Lenovo ThinkSystem SR650 (BMC 2.50)
- Dell PowerEdge R440, R640, R650, R6515, C6420
- GIGABYTE G292-Z20, G292-Z40, G482-Z54
- GIGABYTE R263-Z32 (AMI MegaRAC SP-X)

Since the fork:
- Nvidia B200
- Nvidia GB200/GB300 NVL72
- (really, most Nvidia server architectures/OEMs)

## Why a Fork?

We decided to fork the existing exporter for several reasons:

- Specialized needs: In the GPU Cloud/AI/Superintelligence space, hardware platforms evolve so quickly and in specialized ways, requiring tailored behaviors.
- Updated dependencies: The upstream repository had more than a few outdated dependencies. We want to stay (more) up to date.
- Tests: We want to encourage testable behavior as much as possible, _especially_ when working with bleeding-edge hardware.
- Deviation in behavior: We envision the redfish_exporter operating more similary to other Prometheus ecosystem exporters, like `blackbox_exporter`.

## Acknowledgements

* https://github.com/stmcginnis/gofish, whose work made this exporter and its predecessors possible
* https://github.com/jenningsloy318/redfish_exporter, where our fork was forked from
* https://github.com/FlxPeters/redfish_exporter (which this particular project forks)
