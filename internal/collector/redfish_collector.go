package collector

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"

	"github.com/LambdaLabs/redfish_exporter/internal/config"
)

// Metric name parts.
const (
	namespace = "redfish"
	exporter  = "exporter"
)

var (
	totalScrapeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, exporter, "collector_duration_seconds"),
		"Collector time duration.",
		nil, nil,
	)
	collectorsSucceededDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, exporter, "collectors_succeeded"),
		"Number of sub-collectors that completed successfully in the most recent scrape.",
		nil, nil,
	)
	collectorsFailedDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, exporter, "collectors_failed"),
		"Number of sub-collectors that failed (panicked) in the most recent scrape.",
		nil, nil,
	)
)

// redfishCollector is an aggregation of various other prometheus.Collector.
// It implements prometheus.Collector, and at Describe or Collect time will iterate all of
// its own collectors to yield data.
type redfishCollector struct {
	ctx           context.Context
	logger        *slog.Logger
	redfishClient *gofish.APIClient
	collectors    []ContextAwareCollector
	redfishUp     prometheus.Gauge

	// collectorsSucceeded and collectorsFailed track how many sub-collectors
	// completed successfully or failed in the most recent Collect() call.
	// They are reset to zero at the start of each Collect(). collectorSucceeded is
	// incremented as sub-collector goroutines complete. collectorsFailed is derived
	// at the end of Collect() as the difference between total collectors and successful
	// ones.
	collectorsSucceeded atomic.Int64
	collectorsFailed    atomic.Int64

	// host is the BMC address, retained so Close() can attribute its outcome to a device.
	host string
	// username and password are retained for the direct DELETE fallback in Close(): gofish
	// only ever presents the session token, so releasing a slot whose token the BMC has
	// already invalidated means re-authenticating the request ourselves.
	username string
	password string
	// logoutTimeout bounds each session delete attempt in Close().
	logoutTimeout time.Duration
	// closeOnce guards the session teardown in Close().
	closeOnce sync.Once
}

// NewRedfishCollector returns a *redfishCollector or an error.
func NewRedfishCollector(ctx context.Context, logger *slog.Logger, host string, username string, password string, rfConfig config.RedfishClientConfig) (*redfishCollector, error) {
	redfishClient, err := newRedfishClient(ctx, host, username, password, rfConfig)
	if err != nil {
		// The attempt may still have cost the BMC a slot: if the session was created and the
		// exporter stopped waiting before reading the Location header, nothing here ever
		// learns the identifier, so nothing can delete it.
		logger.Error("failed to create redfish session",
			slog.Int("status_code", httpStatusOf(err)),
			slog.Any("error", err))
		return nil, err
	}

	// Record the session as soon as it exists. A slot left occupied on a BMC can only be
	// traced back to the scrape that opened it if the identifier the BMC assigned was logged
	// at creation; by teardown time a failure may leave nothing else to correlate against.
	// At debug, because this is one line per scrape per target on the healthy path — the
	// counterpart signal that belongs in production is redfish_sessions_open.
	if session, sessErr := redfishClient.GetSession(); sessErr == nil {
		sessionLogger(logger, session.ID).Debug("redfish session created")
	} else {
		// A client holding no session — basic auth — has no slot to release either.
		logger.Debug("redfish client created without a session", slog.Any("error", sessErr))
	}

	logoutTimeout := rfConfig.LogoutTimeout
	if logoutTimeout <= 0 {
		logoutTimeout = config.DefaultRedfishConfig.LogoutTimeout
	}

	return &redfishCollector{
		ctx:           ctx,
		logger:        logger,
		redfishClient: redfishClient,
		host:          host,
		username:      username,
		password:      password,
		logoutTimeout: logoutTimeout,
		redfishUp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "",
				Name:      "up",
				Help:      "redfish up",
			},
		),
	}, nil
}

// WithCollectors sets a slice of prometheus.Collector which this aggregated RedfishCollector should use.
func (r *redfishCollector) WithCollectors(c []ContextAwareCollector) {
	r.collectors = c
}

// CollectorOutcome returns the number of sub-collectors that succeeded and failed
// in the most recent Collect() session. Values are meaningful only after Collect() returns.
func (r *redfishCollector) CollectorOutcome() (succeeded, failed int64) {
	return r.collectorsSucceeded.Load(), r.collectorsFailed.Load()
}

func (r *redfishCollector) Client() *gofish.APIClient {
	return r.redfishClient
}

// Describe implements prometheus.Collector.
func (r *redfishCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range r.collectors {
		collector.Describe(ch)
	}
	ch <- collectorsSucceededDesc
	ch <- collectorsFailedDesc
	ch <- sessionsOpenDesc
}

// Collect implements prometheus.Collector.
func (r *redfishCollector) Collect(ch chan<- prometheus.Metric) {
	r.collectorsSucceeded.Store(0)
	r.collectorsFailed.Store(0)

	scrapeTime := time.Now()
	if r.ctx.Err() != nil {
		r.logger.With("error", r.ctx.Err()).Warn("skipping further collection")
		r.redfishUp.Set(0)
	} else {
		r.redfishUp.Set(1)

		// Session teardown is the caller's responsibility via Close(), so that it also
		// happens on the branch above and on paths where Collect() is never reached.
		eg := newRecoverGroup(r.ctx)

		// Read the BMC's slot usage alongside the sub-collectors rather than ahead of them.
		// The value does not depend on the ordering — nothing a sub-collector does opens or
		// releases a session — and serialising it would add a whole request's latency to every
		// scrape, worst of all on the slow and silent BMCs this metric exists to watch, where it
		// would burn the response header timeout before collection had started.
		//
		// It deliberately does not touch collectorsSucceeded: that counter is reported against
		// len(r.collectors), so a non-collector goroutine incrementing it would overcount
		// successes and drive collectorsFailed negative. A failure here is not a scrape failure
		// either — the metric simply goes absent, which is itself legible in a query.
		eg.Go(func() error {
			if open, err := r.openSessionCount(); err != nil {
				r.logger.Debug("could not read the BMC's open session count", slog.Any("error", err))
			} else {
				ch <- prometheus.MustNewConstMetric(sessionsOpenDesc, prometheus.GaugeValue, float64(open))
			}
			return nil
		})

		for _, collector := range r.collectors {
			eg.Go(func() error {
				collector.CollectWithContext(r.ctx, ch)
				// Only reached if CollectWithContext returns without panicking.
				// Panics are caught by recoverGroup and skip this line, so
				// collectorsSucceeded naturally undercounts on panic.
				r.collectorsSucceeded.Add(1)
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			r.logger.Error("goroutine error", slog.Any("error", err))
		}
		// The delta is the number of collectors that didn't complete successfully.
		r.collectorsFailed.Store(int64(len(r.collectors)) - r.collectorsSucceeded.Load())
	}

	ch <- r.redfishUp
	ch <- prometheus.MustNewConstMetric(totalScrapeDurationDesc, prometheus.GaugeValue, time.Since(scrapeTime).Seconds())
	ch <- prometheus.MustNewConstMetric(collectorsSucceededDesc, prometheus.GaugeValue, float64(r.collectorsSucceeded.Load()))
	ch <- prometheus.MustNewConstMetric(collectorsFailedDesc, prometheus.GaugeValue, float64(r.collectorsFailed.Load()))
}

type collectorCtxKey struct{}

func ContextWithCollector(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, collectorCtxKey{}, name)
}

func CollectorFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(collectorCtxKey{}).(string); ok {
		return v
	}
	return ""
}

func newRedfishClient(ctx context.Context, host string, username string, password string, rfConfig config.RedfishClientConfig) (*gofish.APIClient, error) {
	url := fmt.Sprintf("https://%s", host)
	dialer := &net.Dialer{
		Timeout:   rfConfig.DialTimeout,
		KeepAlive: 30 * time.Second,
	}

	responseHeaderTimeout := rfConfig.ResponseHeaderTimeout
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = config.DefaultRedfishConfig.ResponseHeaderTimeout
	}

	transport := &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: rfConfig.DialTimeout,
		// The common BMC failure here is reachable-but-silent: TCP and TLS complete and the
		// response headers never arrive. This bounds only the wait for headers, so a slow
		// but progressing response body is left alone. gofish does not overwrite this field,
		// and the otelhttp wrapper installed below sits outside this transport, so it applies
		// to every request the client makes.
		ResponseHeaderTimeout: responseHeaderTimeout,
	}

	client := &http.Client{
		Transport: transport,
	}

	clientConfig := gofish.ClientConfig{
		HTTPClient:            client,
		MaxConcurrentRequests: rfConfig.MaxConcurrentRequests,
		Endpoint:              url,
		Username:              username,
		Password:              password,
		Insecure:              true,
		ReuseConnections:      true, // Enable HTTP keepalive for connection reuse
	}
	redfishClient, err := gofish.ConnectContext(ctx, clientConfig)
	if err != nil {
		return nil, err
	}
	// Wrap the transport with otelhttp for HTTP client metrics.
	// This must happen after ConnectContext so gofish can configure TLS/keepalive on the bare transport.
	redfishClient.HTTPClient.Transport = otelhttp.NewTransport(redfishClient.HTTPClient.Transport,
		otelhttp.WithMetricAttributesFn(func(r *http.Request) []attribute.KeyValue {
			if name := CollectorFromContext(r.Context()); name != "" {
				return []attribute.KeyValue{attribute.String("module", name)}
			}
			return nil
		}))
	// Outermost, so the HTTP client metrics count only requests that reach the BMC.
	redfishClient.HTTPClient.Transport = newScrapeCache(redfishClient.HTTPClient.Transport)
	return redfishClient, nil
}

func parseCommonStatusHealth(status schemas.Health) (float64, bool) {
	if bytes.Equal([]byte(status), []byte("OK")) {
		return float64(1), true
	} else if bytes.Equal([]byte(status), []byte("Warning")) {
		return float64(2), true
	} else if bytes.Equal([]byte(status), []byte("Critical")) {
		return float64(3), true
	}
	return float64(0), false
}

func parseCommonStatusState(status schemas.State) (float64, bool) {

	if bytes.Equal([]byte(status), []byte("")) {
		return float64(0), false
	} else if bytes.Equal([]byte(status), []byte("Enabled")) {
		return float64(1), true
	} else if bytes.Equal([]byte(status), []byte("Disabled")) {
		return float64(2), true
	} else if bytes.Equal([]byte(status), []byte("StandbyOffinline")) { // sure looks like a typo, but maybe intentional?
		return float64(3), true
	} else if bytes.Equal([]byte(status), []byte("StandbyOffline")) { // correct spelling, same state
		return float64(3), true
	} else if bytes.Equal([]byte(status), []byte("StandbySpare")) {
		return float64(4), true
	} else if bytes.Equal([]byte(status), []byte("InTest")) {
		return float64(5), true
	} else if bytes.Equal([]byte(status), []byte("Starting")) {
		return float64(6), true
	} else if bytes.Equal([]byte(status), []byte("Absent")) {
		return float64(7), true
	} else if bytes.Equal([]byte(status), []byte("UnavailableOffline")) {
		return float64(8), true
	} else if bytes.Equal([]byte(status), []byte("Deferring")) {
		return float64(9), true
	} else if bytes.Equal([]byte(status), []byte("Quiesced")) {
		return float64(10), true
	} else if bytes.Equal([]byte(status), []byte("Updating")) {
		return float64(11), true
	} else if bytes.Equal([]byte(status), []byte("Standby")) { // not standard, but seen on LITE-ON PSUs
		return float64(12), true
	}
	return float64(0), false
}

func parseCommonPowerState(status schemas.PowerState) (float64, bool) {
	if bytes.Equal([]byte(status), []byte("On")) {
		return float64(1), true
	} else if bytes.Equal([]byte(status), []byte("Off")) {
		return float64(2), true
	} else if bytes.Equal([]byte(status), []byte("PoweringOn")) {
		return float64(3), true
	} else if bytes.Equal([]byte(status), []byte("PoweringOff")) {
		return float64(4), true
	}
	return float64(0), false
}

func parseLinkStatus(status schemas.LinkStatus) (float64, bool) {
	if bytes.Equal([]byte(status), []byte("LinkUp")) {
		return float64(1), true
	} else if bytes.Equal([]byte(status), []byte("NoLink")) {
		return float64(2), true
	} else if bytes.Equal([]byte(status), []byte("LinkDown")) {
		return float64(3), true
	}
	return float64(0), false
}

func parseNVLinkPortLinkStatus(status schemas.PortLinkStatus) (float64, bool) {
	if bytes.Equal([]byte(status), []byte("LinkUp")) {
		return float64(1), true
	} else if bytes.Equal([]byte(status), []byte("Starting")) {
		return float64(2), true
	} else if bytes.Equal([]byte(status), []byte("Training")) {
		return float64(3), true
	} else if bytes.Equal([]byte(status), []byte("LinkDown")) {
		return float64(4), true
	} else if bytes.Equal([]byte(status), []byte("NoLink")) {
		return float64(5), true
	}
	return float64(0), false
}

func parsePortLinkStatus(status schemas.PortLinkStatus) (float64, bool) {
	if bytes.Equal([]byte(status), []byte("Up")) {
		return float64(1), true
	}
	return float64(0), false
}
func boolToFloat64(data bool) float64 {

	if data {
		return float64(1)
	}
	return float64(0)

}

func parsePhySecIntrusionSensor(method schemas.IntrusionSensor) (float64, bool) {
	if bytes.Equal([]byte(method), []byte("Normal")) {
		return float64(1), true
	}
	if bytes.Equal([]byte(method), []byte("TamperingDetected")) {
		return float64(2), true
	}
	if bytes.Equal([]byte(method), []byte("HardwareIntrusion")) {
		return float64(3), true
	}

	return float64(0), false
}

func float32PtrToFloat64(f *float32) float64 {
	if f == nil {
		return 0
	}
	return float64(*f)
}

func intPtrToFloat64(i *int) float64 {
	if i == nil {
		return 0
	}
	return float64(*i)
}
