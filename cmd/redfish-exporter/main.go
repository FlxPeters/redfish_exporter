package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/LambdaLabs/redfish_exporter/internal/collector"
	"github.com/LambdaLabs/redfish_exporter/internal/config"
	"github.com/LambdaLabs/redfish_exporter/internal/ctxlog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/exporter-toolkit/web"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
)

type (
	rfContextKey int
)

const (
	scrapeRequestCtxKey rfContextKey = iota
)

var (
	webConfig     = flag.String("web.config-file", "", "Path to web configuration file.")
	configFile    = flag.String("config.file", "config.yml", "Path to configuration file.")
	pprofEnabled  = flag.Bool("pprof.enabled", false, "Enable pprof handler at /pprof")
	listenAddress = flag.String(
		"web.listen-address",
		":9610",
		"Address to listen on for web interface and telemetry.",
	)
	envPrefix = flag.String(
		"config.env-prefix",
		"",
		"Prefix for environment variables that override config values (e.g. REDFISH_EXPORTER). Empty means no prefix.",
	)
	safeConfig *config.SafeConfig
	reloadCh   chan chan error
)

// 'meta' metrics, which the collector itself should emit
var (
	// collectorLastStatus tracks the last configuration staus of a collector
	collectorLastStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "redfish_exporter_config_collector_last_status",
		Help: "The last known configuration state of a collector. 0=unconfigured/unhealthy, 1=configured/healthy",
	},
		[]string{"collector_name"},
	)
	// collectorModuleUnknown tracks count of requests which asked for &module=foo, but where module foo is not
	// in the exporter configuration file
	collectorModuleUnknown = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "redfish_exporter_unknown_modules_requested_total",
		Help: "Count of requests specifying a module name not known to this exporter",
	})
	// platformUnknown counts requests whose platform= names a key the exporter's platform table
	// does not have; those scrapes run as the default platform.
	platformUnknown = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "redfish_exporter_unknown_platforms_requested_total",
		Help: "Count of requests specifying a platform not in this exporter's platform table",
	})
	// scrapeSuccessesTotal counts scrapes where all sub-collectors completed without failure.
	scrapeSuccessesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "redfish_exporter_scrape_successes_total",
		Help: "Total number of scrapes where all sub-collectors completed successfully for the target.",
	}, []string{"target"})
)

func reloadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" || r.Method == "PUT" {
			slog.Info("Triggered configuration reload from /-/reload HTTP endpoint")
			err := safeConfig.ReloadConfig(*configFile)
			if err != nil {
				slog.Error("failed to reload config file", slog.Any("error", err))
				http.Error(w, "failed to reload config file", http.StatusInternalServerError)
			}
			logUnknownPlatforms(safeConfig)
			slog.Info("config file reloaded", slog.String("operation", "sc.ReloadConfig"))

			w.WriteHeader(http.StatusOK)
			_, err = io.WriteString(w, "Configuration reloaded successfully!")
			if err != nil {
				slog.Warn("failed to send configuration reload status message")
			}
		} else {
			http.Error(w, "Only PUT and POST methods are allowed", http.StatusBadRequest)
		}
	}
}

// logUnknownPlatforms reports every platforms: key in the loaded config that this build's
// platform table does not have. Such a platform still scrapes, as default, so this is the
// place a typo or a missing table row shows up.
func logUnknownPlatforms(sc *config.SafeConfig) {
	sc.Lock()
	defer sc.Unlock()
	for name := range sc.Config.Platforms {
		if _, known := collector.PlatformByName(name); !known {
			slog.Error("config declares a platform this exporter's table does not know; scrapes of it run as default",
				slog.String("platform", name), slog.Any("known_platforms", collector.PlatformNames()))
		}
	}
}

// registerMetaMetrics registers 'meta' metrics for the redfish_exporter.
// 'meta' metrics include details like "which collectors errored during creation?"
func registerMetaMetrics() {
	custom := []prometheus.Collector{
		collectorLastStatus,
		collectorModuleUnknown,
		platformUnknown,
		scrapeSuccessesTotal,
	}
	custom = append(custom, collector.SessionMetrics()...)

	prometheus.DefaultRegisterer.MustRegister(custom...)
}

type scrapeRequest struct {
	Target     string
	Modules    []string
	Platform   string
	HostConfig *config.HostConfig
}

func withLogger(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := ctxlog.NewContext(r.Context(), logger)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func mustScrapeRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxLogger := ctxlog.GetContextLogger(r.Context())

		urlQuery, urlErr := url.ParseQuery(r.URL.RawQuery)
		if urlErr != nil {
			ctxLogger.Error("request query was not parsed", slog.Any("error", urlErr))
			http.Error(w, "failed to successfully parse incoming query", 400)
			return
		}
		target := urlQuery.Get("target")
		if target == "" {
			http.Error(w, "'target' parameter must be specified", 400)
			return
		}
		platformName := urlQuery.Get("platform")
		modules := modulesFor(platformName, urlQuery["module"], safeConfig, ctxLogger)
		if modules[0] == "rf_exporter_default" {
			ctxLogger.Debug("incoming request included no modules. using default modules, and a future release will result in failed collection without one or more modules configured and provided")
		}

		ctxLogger.Debug("Scraping target host", slog.String("target", target))

		var (
			hostConfig *config.HostConfig
			err        error
			ok         bool
			group      []string
		)

		group, ok = urlQuery["group"]
		if ok && len(group[0]) >= 1 {
			// Trying to get hostConfig from group.
			if hostConfig, err = safeConfig.HostConfigForGroup(group[0]); err != nil {
				ctxLogger.Error("error getting credentials", slog.Any("error", err))
				return
			}
		}
		// Always falling back to single host config when group config failed.
		if hostConfig == nil {
			if hostConfig, err = safeConfig.HostConfigForTarget(target); err != nil {
				ctxLogger.Error("error getting credentials", slog.Any("error", err))
				return
			}
		}
		sr := &scrapeRequest{
			Target:     target,
			Modules:    modules,
			Platform:   platformName,
			HostConfig: hostConfig,
		}
		ctx := context.WithValue(r.Context(), scrapeRequestCtxKey, sr)
		ctx = ctxlog.NewContext(ctx, ctxLogger)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// modulesFor decides the modules a scrape runs. A request that names a platform with a
// stanza in config.yml runs that stanza's modules; any module= params on such a request are
// ignored, with a warning. A request that names no platform, or names one without a stanza,
// runs its module= params, else the default bundle. A module= value may list several modules
// separated by commas, since a relabel rule in a scrape job can set only one value.
func modulesFor(platformName string, requested []string, sc *config.SafeConfig, logger *slog.Logger) []string {
	requested = splitModules(requested)
	if platformName != "" {
		if modules, ok := sc.PlatformModules(platformName); ok && len(modules) > 0 {
			if len(requested) > 0 {
				logger.Warn("request named both a platform and modules; the platform's modules win",
					slog.String("platform", platformName), slog.Any("ignored_modules", requested))
			}
			return modules
		}
	}
	if len(requested) > 0 {
		return requested
	}
	return []string{"rf_exporter_default"}
}

// splitModules splits each module= value on commas, trimming spaces and dropping empty entries.
func splitModules(values []string) []string {
	var modules []string
	for _, value := range values {
		for _, module := range strings.Split(value, ",") {
			if module = strings.TrimSpace(module); module != "" {
				modules = append(modules, module)
			}
		}
	}
	return modules
}

func getScrapeRequest(r *http.Request) (*scrapeRequest, bool) {
	sr, ok := r.Context().Value(scrapeRequestCtxKey).(*scrapeRequest)
	return sr, ok
}

// metricsHandler provides the client interface for the redfish_exporter.
// Clients (like Prometheus) MUST provide a target (FQDN or IP)
// and SHOULD provide a 'module' param.
func metricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		registry := prometheus.NewRegistry()

		ctxLogger := ctxlog.GetContextLogger(r.Context())

		sr, ok := getScrapeRequest(r)
		if !ok {
			http.Error(w, "scrape request not found in context", 500)
			return
		}

		scrapeLogger := ctxLogger.With("scrape_host", sr.Target)
		rfClientConfig := safeConfig.RedfishClientConfig()
		aggregateCollector, err := collector.NewRedfishCollector(r.Context(), scrapeLogger, sr.Target,
			sr.HostConfig.Username,
			sr.HostConfig.Password,
			rfClientConfig)

		if err != nil {
			scrapeLogger.With("error", err).Error("unable to create redfish aggregate collector, bailing")
			statusCode := http.StatusInternalServerError
			var rfErr *schemas.Error
			if errors.As(err, &rfErr) && rfErr.HTTPReturnedStatusCode != 0 {
				statusCode = rfErr.HTTPReturnedStatusCode
			}
			http.Error(w, fmt.Sprintf("unable to construct aggregate collector: %s", err), statusCode)
			return
		}
		// NewRedfishCollector has opened a session on the BMC, so release it on every exit
		// from here on: the normal path, the one where the scrape context is already
		// cancelled and Collect() declines to do any work, and a panic unwinding out of
		// MustRegister or a sub-collector. A session left behind occupies one of the BMC's
		// limited slots until the BMC's own idle timeout reclaims it.
		defer aggregateCollector.Close()

		platform, known := collector.PlatformByName(sr.Platform)
		if sr.Platform != "" && !known {
			platformUnknown.Inc()
			scrapeLogger.Warn("platform is not in the exporter's table, running as default", slog.String("platform", sr.Platform))
		}
		scrapeLogger.Info("platform selected", slog.String("platform", platform.Name), slog.String("requested", sr.Platform))
		platformInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "redfish_exporter_platform_info",
			Help: "The platform this scrape ran as, and the platform the request named: requested is empty when the request named none, and platform is default when it named one the exporter's table does not know.",
		}, []string{"platform", "requested"})
		platformInfo.WithLabelValues(platform.Name, sr.Platform).Set(1)
		registry.MustRegister(platformInfo)

		collectors := buildCollectorsFor(r.Context(), sr.Modules, safeConfig.GetModules(), aggregateCollector.Client(), scrapeLogger, platform)
		aggregateCollector.WithCollectors(collectors)
		registry.MustRegister(aggregateCollector)
		gatherers := prometheus.Gatherers{
			registry,
		}
		// Delegate http serving to Prometheus client library, which will call collector.Collect.
		h := promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{})
		h.ServeHTTP(w, r)

		if _, failed := aggregateCollector.CollectorOutcome(); failed == 0 {
			scrapeSuccessesTotal.WithLabelValues(sr.Target).Inc()
		}
	}
}

// buildCollectorsFor accepts a list of modules and map of moduleConfig, yielding a slice of
// prometheus.Collector built from the module config.
// It silently discards modules for which there is no module config.
// For ease onboarding from existing redfish_exporter deployments,
// a modules[0] == "rf_exporter_default" will yield a []prometheus.Collector defined
// in this function. Future relases will remove this behavior and require user input.
func buildCollectorsFor(ctx context.Context, modules []string, moduleConfig map[string]config.Module, rfClient *gofish.APIClient, logger *slog.Logger, platform collector.Platform) []collector.ContextAwareCollector {
	if modules[0] == "rf_exporter_default" {
		logger.Warn("Using default collector bundle. In a future release, the exporter will require configuration of one or more modules.")
		return buildCollectorsFor(ctx, []string{
			"gpu_collector",
			"chassis_collector",
			"manager_collector",
			"system_collector",
			"telemetry_collector",
		}, config.DefaultModuleConfig, rfClient, logger, platform)
	}
	c := []collector.ContextAwareCollector{}
	for _, module := range modules {
		if modConfig, found := moduleConfig[module]; found {
			collectorClient := rfClient.WithContext(collector.ContextWithCollector(ctx, module))
			collector, err := collector.NewCollectorFromModule(module, &modConfig, collectorClient, logger, platform)
			if err != nil {
				logger.Error("unable to create collector", slog.Any("error", err))
				collectorLastStatus.WithLabelValues(module).Set(0)
				continue
			}
			collectorLastStatus.WithLabelValues(module).Set(1)
			c = append(c, collector)
		} else {
			collectorModuleUnknown.Inc()
		}
	}
	return c
}

// Parse the log leven from input
func parseLogLevel(level string) slog.Level {
	ret := slog.LevelInfo
	switch level {
	case "debug":
		ret = slog.LevelDebug
	case "info":
		ret = slog.LevelInfo
	case "warn":
		ret = slog.LevelWarn
	case "error":
		ret = slog.LevelError
	default:
		slog.Warn("Invalid loglevel provided. Fallback to default")
	}

	return ret
}

func main() {
	slog.Info("Starting redfish_exporter")
	flag.Parse()

	if err := runMain(); err != nil {
		slog.Error("Failed to run redfish_exporter", slog.Any("error", err))
		os.Exit(1)
	}
}

func runMain() error {
	safeConfig = config.NewSafeConfig(*envPrefix)
	// load config first time
	if err := safeConfig.ReloadConfig(*configFile); err != nil {
		return fmt.Errorf("error parsing config file: %w", err)
	}
	logUnknownPlatforms(safeConfig)

	registerMetaMetrics()
	// Setup dinal logger from config
	opts := &slog.HandlerOptions{
		Level: parseLogLevel(safeConfig.Config.Loglevel),
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, opts))
	slog.SetDefault(logger)

	slog.Info("Config successfully parsed", slog.String("loglevel", opts.Level.Level().String()))

	// load config in background to watch for config changes
	hup := make(chan os.Signal, 1)
	reloadCh = make(chan chan error)
	signal.Notify(hup, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-hup:
				if err := safeConfig.ReloadConfig(*configFile); err != nil {
					slog.Error("failed to reload config file", slog.Any("error", err))
					break
				}
				logUnknownPlatforms(safeConfig)
				slog.Info("config file reload", slog.String("operation", "sc.ReloadConfig"))
			case rc := <-reloadCh:
				if err := safeConfig.ReloadConfig(*configFile); err != nil {
					slog.Error("failed to reload config file", slog.Any("error", err))
					rc <- err
					break
				}
				logUnknownPlatforms(safeConfig)
				slog.Info("config file reloaded", slog.String("operation", "sc.ReloadConfig"))
				rc <- nil
			}
		}
	}()

	mux := http.NewServeMux()

	mux.Handle("/redfish", withLogger(mustScrapeRequest(metricsHandler()), logger)) // Regular metrics endpoint for local Redfish metrics.
	mux.Handle("/-/reload", reloadHandler())                                        // HTTP endpoint for triggering configuration reload
	mux.Handle("/metrics", promhttp.Handler())

	if *pprofEnabled {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
		mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
		mux.Handle("/debug/pprof/block", pprof.Handler("block"))
		mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
		mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))

		slog.Info("pprof endpoints enabled", slog.Any("endpoint", "/debug/pprof/"))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// nolint
		w.Write([]byte(`<html>
            <head>
            <title>Redfish Exporter</title>
            </head>
						<body>
            <h1>redfish Exporter</h1>
            <form action="/redfish">
            <label>Target:</label> <input type="text" name="target" placeholder="X.X.X.X" value="1.2.3.4"><br>
            <label>Group:</label> <input type="text" name="group" placeholder="group (optional)" value=""><br>
            <label>Module:</label> <input type="text" name="module" placeholder="module (optional)" value=""><br>
            <label>Platform:</label> <input type="text" name="platform" placeholder="platform (optional)" value=""><br>
            <input type="submit" value="Submit">
						</form>
						<p><a href="/metrics">Local metrics</a></p>
            </body>
            </html>`))
	})

	exporterToolkitConf := web.FlagConfig{
		WebListenAddresses: &([]string{*listenAddress}),
		WebConfigFile:      webConfig,
	}
	slog.Info("Exporter started", slog.String("listenAddress", *listenAddress))
	srv := &http.Server{
		Handler: mux,
	}

	mp, err := initOTelMeterProvider()
	if err != nil {
		return fmt.Errorf("failed to initialize OTel meter provider: %w", err)
	}

	shutdownFuncs := []func(ctx context.Context) error{
		func(ctx context.Context) error {
			return mp.Shutdown(ctx)
		},
		func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	}
	return runServer(func(ctx context.Context) error {
		return web.ListenAndServe(srv, &exporterToolkitConf, logger)
	}, shutdownFuncs, safeConfig.Config.ShutdownTimeout)
}

func runServer(f func(ctx context.Context) error, shutdownFuncs []func(ctx context.Context) error, shutdownTimeout time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- f(ctx)
	}()

	select {
	case err := <-serverErr:
		if ctx.Err() != nil {
			// Signal was received; web.ListenAndServe already shut down the HTTP server
			// internally. Break to run remaining shutdown functions (e.g. OTel provider).
			stop()
			break
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("exiting on ListenAndServe error: %w", err)
		}
		return nil
	case <-ctx.Done():
		stop() // re-enable default signal behavior so a second signal kills immediately
	}

	slog.Info("Received shutdown signal, starting graceful shutdown", slog.String("timeout", shutdownTimeout.String()))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	for _, shutdownFunc := range shutdownFuncs {
		if err := shutdownFunc(shutdownCtx); err != nil {
			slog.Error("error during graceful shutdown", slog.Any("error", err))
		}
	}
	return nil
}
