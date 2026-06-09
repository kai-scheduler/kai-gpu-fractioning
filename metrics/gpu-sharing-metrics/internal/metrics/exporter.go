package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/store"
)

type Config struct {
	Enabled  bool
	Address  string
	Path     string
	ProcRoot string
	Interval time.Duration
	// SMUtilizationWindow, when greater than Interval, enables sliding-window
	// averaging of per-pod GPU SM utilization. Each collect's sample is stored;
	// samples older than the window are evicted; the reported value is the
	// average of the remaining samples. Zero (default) disables windowing and
	// reports the latest sample only.
	SMUtilizationWindow time.Duration
	Names               MetricNames
}

// MetricNames holds the exported Prometheus metric names. Each is independently
// configurable; empty fields fall back to the defaults via WithDefaults.
type MetricNames struct {
	GPUMemoryUsedBytes      string `json:"gpuMemoryUsedBytes"`
	GPUSMUtilizationPercent string `json:"gpuSmUtilizationPercent"`
}

// DefaultMetricNames returns the built-in metric names used when none are
// configured. Pod identity is carried in labels (namespace, pod, pod_uid), so
// the names describe the measurement and unit only, per Prometheus convention.
func DefaultMetricNames() MetricNames {
	return MetricNames{
		GPUMemoryUsedBytes:      "gpu_sharing_gpu_memory_used_bytes",
		GPUSMUtilizationPercent: "gpu_sharing_gpu_sm_utilization_percent",
	}
}

// WithDefaults fills any unset (empty) metric name with its default, so a config
// that overrides only some names keeps the defaults for the rest.
func (n MetricNames) WithDefaults() MetricNames {
	d := DefaultMetricNames()
	if strings.TrimSpace(n.GPUMemoryUsedBytes) == "" {
		n.GPUMemoryUsedBytes = d.GPUMemoryUsedBytes
	}
	if strings.TrimSpace(n.GPUSMUtilizationPercent) == "" {
		n.GPUSMUtilizationPercent = d.GPUSMUtilizationPercent
	}
	return n
}

// metricNamePattern mirrors Prometheus's own metric-name rule (the same
// expression as prometheus/common/model.MetricNameRE): a required first
// character from [a-zA-Z_:] followed by any number of [a-zA-Z0-9_:]. This
// implies a minimum of one character and imposes no maximum length, exactly
// like Prometheus.
var metricNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// validate rejects names Prometheus would refuse, so a typo fails startup with a
// clear error instead of panicking inside MustRegister.
func (n MetricNames) validate() error {
	for field, name := range map[string]string{
		"gpuMemoryUsedBytes":      n.GPUMemoryUsedBytes,
		"gpuSmUtilizationPercent": n.GPUSMUtilizationPercent,
	} {
		if !metricNamePattern.MatchString(name) {
			return fmt.Errorf("metric name %s=%q is not a valid Prometheus metric name", field, name)
		}
	}
	return nil
}

type Runtime struct {
	cancel        context.CancelFunc
	server        *http.Server
	coll          collector
	controller    *metricsController
	wg            sync.WaitGroup
	provider      SnapshotProvider
	log           *slog.Logger
	mu            sync.Mutex
	registry      *prometheus.Registry
	memoryBytes   *prometheus.GaugeVec
	smUtilization *prometheus.GaugeVec
	knownLabels   map[podGPUKey]prometheus.Labels
}

// New builds a Runtime from cfg — validates config, wires the collector, pod
// source, controller, and HTTP server, but does not start any goroutines.
// Returns nil when cfg.Enabled is false.
func New(ctx context.Context, cfg Config, reader store.Reader, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if !cfg.Enabled {
		logger.InfoContext(ctx, "GPU metrics exporter disabled")
		return nil, nil
	}

	names := cfg.Names.WithDefaults()
	if err := names.validate(); err != nil {
		return nil, fmt.Errorf("configure metric names: %w", err)
	}

	if reader == nil {
		return nil, fmt.Errorf("metrics requires an NRI container store")
	}

	gpuCollector := newCollector(ctx, cfg.collectInterval(), logger)
	controller := newMetricsController(gpuCollector, ProcCgroupResolver{ProcRoot: cfg.ProcRoot}, reader, cfg.collectInterval(), cfg.SMUtilizationWindow, logger)
	runtime := newRuntime(controller, names)
	runtime.log = logger
	runtime.coll = gpuCollector
	runtime.controller = controller

	mux := http.NewServeMux()
	mux.Handle(cfg.metricsPath(), runtime.handler())
	runtime.server = &http.Server{
		Addr:              cfg.Address,
		Handler:           mux,
		ReadHeaderTimeout: metricsReadHeaderTimeout,
	}

	return runtime, nil
}

// Start launches the HTTP server, the collector loop, and the metrics controller.
// It must be called once on a Runtime returned by New.
func (r *Runtime) Start(ctx context.Context) {
	metricsCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	go func() {
		r.log.InfoContext(metricsCtx, "starting GPU metrics exporter", "address", r.server.Addr)
		if err := r.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.log.ErrorContext(metricsCtx, "GPU metrics exporter stopped unexpectedly", "error", err)
		}
	}()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.coll.Run(metricsCtx)
	}()
	go r.controller.Run(metricsCtx)
}

func newRuntime(provider SnapshotProvider, names MetricNames) *Runtime {
	names = names.WithDefaults()
	labels := []string{"namespace", "pod", "pod_uid", "gpu_uuid", "gpu_index"}
	runtime := &Runtime{
		provider: provider,
		registry: prometheus.NewRegistry(),
		memoryBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: names.GPUMemoryUsedBytes,
			Help: "GPU memory used by processes attributed to a Kubernetes pod.",
		}, labels),
		smUtilization: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: names.GPUSMUtilizationPercent,
			Help: "GPU SM utilization attributed to a Kubernetes pod: the sum of per-process NVML SM-active time fractions, clamped to [0,100]. Approximate under MPS where a pod's processes execute concurrently.",
		}, labels),
		knownLabels: map[podGPUKey]prometheus.Labels{},
	}
	runtime.registry.MustRegister(
		runtime.memoryBytes,
		runtime.smUtilization,
	)
	return runtime
}

func (e *Runtime) handler() http.Handler {
	handler := promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.refresh(r.Context())
		handler.ServeHTTP(w, r)
	})
}

// refresh pulls the latest snapshot from the provider on each scrape. Because the
// provider may be remote, a fetch failure is non-fatal: we log it and serve the
// last observed values rather than dropping the scrape.
func (e *Runtime) refresh(ctx context.Context) {
	if e.provider == nil {
		return
	}
	snapshot, err := e.provider.Snapshot(ctx)
	if err != nil {
		log := e.log
		if log == nil {
			log = slog.Default()
		}
		log.WarnContext(ctx, "failed to fetch metrics snapshot from provider; serving last known values", "error", err)
		return
	}
	e.observeSnapshot(snapshot)
}

func (e *Runtime) observeSnapshot(snapshot Snapshot) {
	e.observe(snapshot.Metrics, snapshot.ActivePodUIDs)
}

func (e *Runtime) observe(metrics []PodGPUMetric, activePodUIDs map[string]struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Remove series for gone pods, then zero-out remaining known series
	// (containers still active but currently idle).
	e.pruneDeletedPods(activePodUIDs)
	for _, labels := range e.knownLabels {
		e.setZero(labels)
	}

	// For each active metric: remove any idle placeholder for the same pod+GPU,
	// then write the real value. setZero may have touched the idle label first,
	// but deleteIdleLabelsForObservedGPU removes it from the gauge immediately.
	for _, metric := range metrics {
		key, labels := metricLabels(metric)
		e.deleteIdleLabelsForObservedGPU(key)
		e.knownLabels[key] = labels
		e.memoryBytes.With(labels).Set(float64(metric.MemoryBytes))
		e.smUtilization.With(labels).Set(metric.SMUtilizationPercent)
	}
}

func (e *Runtime) pruneDeletedPods(activePodUIDs map[string]struct{}) {
	if activePodUIDs == nil {
		return
	}
	for key := range e.knownLabels {
		if _, ok := activePodUIDs[key.PodUID]; !ok {
			e.deleteSeries(key)
		}
	}
}

func (e *Runtime) deleteSeries(key podGPUKey) {
	labels := e.knownLabels[key]
	if labels != nil {
		e.memoryBytes.Delete(labels)
		e.smUtilization.Delete(labels)
	}
	delete(e.knownLabels, key)
}

func (e *Runtime) deleteIdleLabelsForObservedGPU(observed podGPUKey) {
	if observed.GPUUUID == "" {
		return
	}
	for key := range e.knownLabels {
		if key.Namespace == observed.Namespace && key.PodUID == observed.PodUID && key.GPUUUID == "" && key.GPUIndex == observed.GPUIndex {
			e.deleteSeries(key)
		}
	}
}

func (e *Runtime) setZero(labels prometheus.Labels) {
	e.memoryBytes.With(labels).Set(0)
	e.smUtilization.With(labels).Set(0)
}

func metricLabels(metric PodGPUMetric) (podGPUKey, prometheus.Labels) {
	key := podGPUKey{
		Namespace: metric.Namespace,
		Pod:       metric.Pod,
		PodUID:    metric.PodUID,
		GPUUUID:   metric.GPUUUID,
		GPUIndex:  metric.GPUIndex,
	}
	return key, prometheus.Labels{
		"namespace": metric.Namespace,
		"pod":       metric.Pod,
		"pod_uid":   metric.PodUID,
		"gpu_uuid":  metric.GPUUUID,
		"gpu_index": strconv.Itoa(metric.GPUIndex),
	}
}

func (e *Runtime) Stop(ctx context.Context, logger *slog.Logger) {
	if e == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	if e.cancel != nil {
		e.cancel()
	}
	// Wait for gpuCollector.Run to exit before calling nvml.Shutdown so there is
	// no race between in-flight NVML calls and the shutdown sequence.
	e.wg.Wait()
	if e.server != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := e.server.Shutdown(shutdownCtx); err != nil {
			logger.WarnContext(ctx, "failed to shutdown GPU metrics exporter", "error", err)
		}
	}
	if e.coll != nil {
		if err := e.coll.Close(); err != nil {
			logger.WarnContext(ctx, "failed to close NVML collector", "error", err)
		}
	}
}

func (c Config) metricsPath() string {
	path := strings.TrimSpace(c.Path)
	if path == "" {
		return DefaultPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

const (
	// MinCollectInterval is the floor on the sampling cadence. NVML
	// process-utilization queries are coarse and frequent polling adds cost
	// without resolution, so a configured interval below this is clamped up.
	MinCollectInterval = 2 * time.Second
	// DefaultCollectInterval is used when no valid interval is configured.
	DefaultCollectInterval = 5 * time.Second
	// metricsReadHeaderTimeout is the maximum time the HTTP server waits to
	// read request headers after a TCP connection is accepted. Prevents stalled
	// clients or port-scanners from holding connections open indefinitely.
	metricsReadHeaderTimeout = 5 * time.Second
)

func (c Config) collectInterval() time.Duration {
	if c.Interval <= 0 {
		return DefaultCollectInterval
	}
	if c.Interval < MinCollectInterval {
		return MinCollectInterval
	}
	return c.Interval
}

