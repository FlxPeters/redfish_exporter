package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	gta "gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// readConfigFrom is a test helper that loads config from an io.Reader.
func readConfigFrom(r io.Reader, envPrefix string) (*Config, error) {
	v := newViperInstance(envPrefix)
	v.SetConfigType("yaml")
	if err := v.ReadConfig(r); err != nil {
		return &Config{}, err
	}
	return unmarshalViperConfig(v)
}

func TestConfigFromFile(t *testing.T) {
	configFile := "testdata/config.example.yml"

	sc := NewSafeConfig("")
	err := sc.ReloadConfig(configFile)
	assert.NoError(t, err)
	config := sc.Config

	assert.Equal(t, "info", config.Loglevel)
	assert.Equal(t, config.Hosts["default"], HostConfig{Username: "user", Password: "pass"})
	assert.Equal(t, config.Groups["group1"], HostConfig{Username: "group1_user", Password: "group1_pass"})
	assert.NotNil(t, config.Modules["foo"])
	assert.NotNil(t, config.Modules["foo"].GPUCollector)
	assert.Equal(t, "gpu_collector", config.Modules["foo"].Prober)
}

func TestRedfishClientConfig(t *testing.T) {
	tT := map[string]struct {
		inputYAML     string
		envVars       map[string]string
		envPrefix     string
		wantErrString string
		wantConfig    *Config
	}{
		"happy path, no user config": {
			inputYAML: `
loglevel: info
`,
			wantErrString: "",
			wantConfig: &Config{
				Loglevel:        "info",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient:   DefaultRedfishConfig,
			},
		},
		"happy path, just one value changed": {
			inputYAML: `
redfish_client:
  max_concurrent_requests: 100
`,
			wantErrString: "",
			wantConfig: &Config{
				ShutdownTimeout: 60 * time.Second,
				RedfishClient: RedfishClientConfig{
					MaxConcurrentRequests: 100,
					DialTimeout:           10 * time.Second,
					ResponseHeaderTimeout: 30 * time.Second,
					LogoutTimeout:         10 * time.Second,
				},
			},
		},
		"happy path, all values changed": {
			inputYAML: `
redfish_client:
  max_concurrent_requests: 100
  dial_timeout: 30s
  response_header_timeout: 45s
  logout_timeout: 5s
`,
			wantErrString: "",
			wantConfig: &Config{
				ShutdownTimeout: 60 * time.Second,
				RedfishClient: RedfishClientConfig{
					MaxConcurrentRequests: 100,
					DialTimeout:           30 * time.Second,
					ResponseHeaderTimeout: 45 * time.Second,
					LogoutTimeout:         5 * time.Second,
				},
			},
		},
		"LOGLEVEL overrides loglevel": {
			inputYAML: `loglevel: info`,
			envVars:   map[string]string{"LOGLEVEL": "debug"},
			wantConfig: &Config{
				Loglevel:        "debug",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient:   DefaultRedfishConfig,
			},
		},
		"REDFISH_CLIENT_MAX_CONCURRENT_REQUESTS overrides redfish_client.max_concurrent_requests": {
			inputYAML: `loglevel: info`,
			envVars:   map[string]string{"REDFISH_CLIENT_MAX_CONCURRENT_REQUESTS": "5"},
			wantConfig: &Config{
				Loglevel:        "info",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient: RedfishClientConfig{
					MaxConcurrentRequests: 5,
					DialTimeout:           10 * time.Second,
					ResponseHeaderTimeout: 30 * time.Second,
					LogoutTimeout:         10 * time.Second,
				},
			},
		},
		"REDFISH_CLIENT_DIAL_TIMEOUT overrides redfish_client.dial_timeout": {
			inputYAML: `loglevel: info`,
			envVars:   map[string]string{"REDFISH_CLIENT_DIAL_TIMEOUT": "30s"},
			wantConfig: &Config{
				Loglevel:        "info",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient: RedfishClientConfig{
					MaxConcurrentRequests: 1,
					DialTimeout:           30 * time.Second,
					ResponseHeaderTimeout: 30 * time.Second,
					LogoutTimeout:         10 * time.Second,
				},
			},
		},
		"REDFISH_CLIENT_LOGOUT_TIMEOUT overrides redfish_client.logout_timeout": {
			inputYAML: `loglevel: info`,
			envVars:   map[string]string{"REDFISH_CLIENT_LOGOUT_TIMEOUT": "3s"},
			wantConfig: &Config{
				Loglevel:        "info",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient: RedfishClientConfig{
					MaxConcurrentRequests: 1,
					DialTimeout:           10 * time.Second,
					ResponseHeaderTimeout: 30 * time.Second,
					LogoutTimeout:         3 * time.Second,
				},
			},
		},
		"REDFISH_CLIENT_RESPONSE_HEADER_TIMEOUT overrides redfish_client.response_header_timeout": {
			inputYAML: `loglevel: info`,
			envVars:   map[string]string{"REDFISH_CLIENT_RESPONSE_HEADER_TIMEOUT": "15s"},
			wantConfig: &Config{
				Loglevel:        "info",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient: RedfishClientConfig{
					MaxConcurrentRequests: 1,
					DialTimeout:           10 * time.Second,
					ResponseHeaderTimeout: 15 * time.Second,
					LogoutTimeout:         10 * time.Second,
				},
			},
		},
		"prefix MYPREFIX applies to all env vars": {
			inputYAML: `loglevel: info`,
			envVars:   map[string]string{"MYPREFIX_LOGLEVEL": "debug"},
			envPrefix: "MYPREFIX",
			wantConfig: &Config{
				Loglevel:        "debug",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient:   DefaultRedfishConfig,
			},
		},
	}
	for tName, test := range tT {
		t.Run(tName, func(t *testing.T) {
			for k, v := range test.envVars {
				t.Setenv(k, v)
			}
			byteReader := bytes.NewReader([]byte(test.inputYAML))
			gotConfig, err := readConfigFrom(byteReader, test.envPrefix)
			if test.wantErrString != "" {
				gta.ErrorContains(t, err, test.wantErrString)
			}
			gta.Assert(t, cmp.DeepEqual(test.wantConfig, gotConfig))
		})
	}
}

func TestModulesConfig(t *testing.T) {
	tT := map[string]struct {
		inputYAML     string
		wantErrString string
		wantConfig    *Config
	}{
		"GPU Collector exists": {
			inputYAML: `
modules:
  foo:
    prober: gpu_collector
    gpu_collector:
`,
			wantErrString: "",
			wantConfig: &Config{
				ShutdownTimeout: 60 * time.Second,
				RedfishClient:   DefaultRedfishConfig,
				Modules: map[string]Module{
					"foo": {
						Prober:           "gpu_collector",
						GPUCollector:     GPUCollectorConfig{},
						ChassisCollector: ChassisCollectorConfig{},
						JSONCollector: JSONCollectorConfig{
							Timeout: 30 * time.Second,
						},
						ManagerCollector:   ManagerCollectorConfig{},
						SystemCollector:    SystemCollectorConfig{},
						TelemetryCollector: TelemetryCollectorConfig{},
					},
				},
			},
		},
		"Unmarshal of multiple modules and with log level looks OK": {
			inputYAML: `
loglevel: info
modules:
  foo:
    prober: gpu_collector
    gpu_collector:
  boo:
    prober: chassis_collector
    chassis_collector:
`,
			wantErrString: "",
			wantConfig: &Config{
				Loglevel:        "info",
				ShutdownTimeout: 60 * time.Second,
				RedfishClient:   DefaultRedfishConfig,
				Modules: map[string]Module{
					"foo": {
						Prober:           "gpu_collector",
						GPUCollector:     GPUCollectorConfig{},
						ChassisCollector: ChassisCollectorConfig{},
						JSONCollector: JSONCollectorConfig{
							Timeout: 30 * time.Second,
						},
						ManagerCollector:   ManagerCollectorConfig{},
						SystemCollector:    SystemCollectorConfig{},
						TelemetryCollector: TelemetryCollectorConfig{},
					},
					"boo": {
						Prober:           "chassis_collector",
						GPUCollector:     GPUCollectorConfig{},
						ChassisCollector: ChassisCollectorConfig{},
						JSONCollector: JSONCollectorConfig{
							Timeout: 30 * time.Second,
						},
						ManagerCollector:   ManagerCollectorConfig{},
						SystemCollector:    SystemCollectorConfig{},
						TelemetryCollector: TelemetryCollectorConfig{},
					},
				},
			},
		},
		"erroneous config returns error": {
			inputYAML:     `foo:bar:baz`,
			wantErrString: "While parsing config: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str",
			wantConfig:    &Config{},
		},
		// TODO: viper silently drops YAML keys with null values (e.g. "gpu_collector:")
		// before Unmarshal runs, so the "foo" module disappears and Validate() never
		// sees the missing prober. Re-enable when fixed upstream:
		// https://github.com/spf13/viper/issues/819
		// "modules require a prober field": {
		// 	inputYAML: `
		// modules:
		//   foo:
		//     gpu_collector:
		// `,
		// 	wantErrString: "module foo is not valid: module requires a prober to be configured",
		// 	wantConfig: &Config{
		// 		RedfishClient: DefaultRedfishConfig,
		// 		Modules: map[string]Module{
		// 			"foo": {
		// 				JSONCollector: JSONCollectorConfig{
		// 					Timeout: 30 * time.Second,
		// 				},
		// 			},
		// 		},
		// 	},
		// },
	}
	for tName, test := range tT {
		t.Run(tName, func(t *testing.T) {
			byteReader := bytes.NewReader([]byte(test.inputYAML))
			gotConfig, err := readConfigFrom(byteReader, "")
			if test.wantErrString != "" {
				gta.ErrorContains(t, err, test.wantErrString)
			}
			gta.Assert(t, cmp.DeepEqual(test.wantConfig, gotConfig))
		})
	}
}

func TestReloadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	// Write initial config with loglevel: info
	err := os.WriteFile(configFile, []byte("loglevel: info\n"), 0600)
	assert.NoError(t, err)

	sc := NewSafeConfig("")
	err = sc.ReloadConfig(configFile)
	assert.NoError(t, err)
	assert.Equal(t, "info", sc.Config.Loglevel)

	// Overwrite config with loglevel: debug
	err = os.WriteFile(configFile, []byte("loglevel: debug\n"), 0600)
	assert.NoError(t, err)

	// Reload the same SafeConfig instance
	err = sc.ReloadConfig(configFile)
	assert.NoError(t, err)
	assert.Equal(t, "debug", sc.Config.Loglevel)
}

func TestModulesConfig_JSONCollector(t *testing.T) {
	tT := map[string]struct {
		inputFile     string
		wantErrString string
		wantConfig    *Config
	}{
		"extraction from Delta Electronics OEM Sensors endpoint": {
			inputFile:     "testdata/config.j2m.yaml",
			wantErrString: "",
			wantConfig: &Config{
				ShutdownTimeout: 60 * time.Second,
				RedfishClient:   DefaultRedfishConfig,
				Modules: map[string]Module{
					"rf_version": {
						Prober: "json_collector",
						JSONCollector: JSONCollectorConfig{
							Timeout:     30 * time.Second,
							RedfishRoot: `/redfish/v1`,
							JQFilter: `[{
  "name": "redfish_version",
  "help": "Redfish version reported by this device",
  "labels": {
    "redfish_version": .RedfishVersion
  },
  "value": 1.0
}]`,
						},
					},
					"delta_powershelf": {
						Prober: "json_collector",
						JSONCollector: JSONCollectorConfig{
							Timeout:     30 * time.Second,
							RedfishRoot: "/redfish/v1/Chassis/PowerShelf_0/Sensors?$expand=.($levels=1)",
							JQFilter: `[.Oem.deltaenergysystems.AllSensors.Sensors[]] |
map({
  name: (if .DeviceName | test("^ps[0-9]+_") then .DeviceName | sub("^ps[0-9]+_"; "") else .DeviceName end),
  value: .Reading,
  labels: (
    if .DeviceName | test("^ps[0-9]+_") then {"power_supply_id": (.DeviceName | split("_")[0])}
    else {}
    end),
    _raw: .
}) |
map(.help = "Value yielded from the Redfish API /Chassis/PowerShelf_0, expanded 1 level") | map(del(._raw)) | sort_by(.name)`,
						},
						GPUCollector:       GPUCollectorConfig{},
						ChassisCollector:   ChassisCollectorConfig{},
						ManagerCollector:   ManagerCollectorConfig{},
						SystemCollector:    SystemCollectorConfig{},
						TelemetryCollector: TelemetryCollectorConfig{},
					},
				},
			},
		},
		"config with a non-standard deadline": {
			inputFile:     "testdata/json_collector_with_timeout.yaml",
			wantErrString: "",
			wantConfig: &Config{
				ShutdownTimeout: 60 * time.Second,
				RedfishClient:   DefaultRedfishConfig,
				Modules: map[string]Module{
					"31s_timeout_collector": {
						Prober: "json_collector",
						JSONCollector: JSONCollectorConfig{
							Timeout:     31 * time.Second,
							RedfishRoot: "/redfish/v1/nop",
							JQFilter:    `nil`,
						},
					},
				},
			},
		},
	}
	for tName, test := range tT {
		t.Run(tName, func(t *testing.T) {
			sc := NewSafeConfig("")
			err := sc.ReloadConfig(test.inputFile)
			gotConfig := sc.Config
			if test.wantErrString != "" {
				gta.ErrorContains(t, err, test.wantErrString)
			}
			gta.Assert(t, cmp.DeepEqual(test.wantConfig, gotConfig))
		})
	}
}

func TestPlatformsConfig(t *testing.T) {
	cfg, err := readConfigFrom(bytes.NewBufferString(`
modules:
  chassis:
    prober: chassis_collector
  gpu:
    prober: gpu_collector
platforms:
  SYS-TEST-1.0:
    modules: [chassis, gpu]
  default:
    modules: [chassis]
`), "")
	assert.NoError(t, err)
	// viper lowercases map keys; a dotted firmware version must survive as one key.
	assert.Equal(t, []string{"chassis", "gpu"}, cfg.Platforms["sys-test-1.0"].Modules)
	assert.Equal(t, []string{"chassis"}, cfg.Platforms["default"].Modules)

	sc := NewSafeConfig("")
	sc.Config = cfg
	modules, ok := sc.PlatformModules("SYS-TEST-1.0")
	assert.True(t, ok)
	assert.Equal(t, []string{"chassis", "gpu"}, modules)
	_, ok = sc.PlatformModules("SYS-OTHER-1.0")
	assert.False(t, ok)

	_, err = readConfigFrom(bytes.NewBufferString(`
modules:
  chassis:
    prober: chassis_collector
platforms:
  SYS-TEST-1.0:
    modules: [chassis, nonexistent]
`), "")
	assert.ErrorContains(t, err, "platform sys-test-1.0 lists module nonexistent")

	_, err = readConfigFrom(bytes.NewBufferString(`
modules:
  chassis:
    prober: chassis_collector
platforms:
  SYS-TEST-1.0:
    modules: []
`), "")
	assert.ErrorContains(t, err, "platform sys-test-1.0 lists no modules")
}
