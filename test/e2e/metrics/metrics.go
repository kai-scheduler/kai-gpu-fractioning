// Package metrics scrapes and parses a Prometheus text-exposition endpoint.
// It has no dependency on the rest of the framework — callers supply a local
// port (typically from portforward).
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Scrape fetches and parses the Prometheus text-exposition response from a
// local port previously opened via portforward.ToPod.
func Scrape(localPort int, path string) (map[string]*dto.MetricFamily, error) {
	if path == "" {
		path = "/metrics"
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", localPort, path)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s: unexpected status %d: %s", url, resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		return nil, fmt.Errorf("GET %s: unexpected Content-Type %q", url, ct)
	}

	// LegacyValidation matches the ASCII metric/label names every Prometheus
	// exporter in this repo emits; NameValidationScheme has no meaningful
	// default here since this module never imports client_golang/prometheus
	// (whose init() would otherwise set the global for us).
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse metrics from %s: %w", url, err)
	}
	return families, nil
}

// GaugeValue extracts the numeric value from a gauge sample.
func GaugeValue(m *dto.Metric) float64 {
	if m.GetGauge() != nil {
		return m.GetGauge().GetValue()
	}
	return 0
}

// FindSeries returns every sample of metricName whose labels are a superset
// of match (a sample may carry labels not present in match; those are
// ignored). Callers match on whatever subset identifies the series they
// care about — e.g. {"namespace": ..., "pod": ..., "pod_uuid": ...} to find a
// specific pod's series regardless of its gpu_index/gpu_uuid.
func FindSeries(families map[string]*dto.MetricFamily, metricName string, match map[string]string) []*dto.Metric {
	mf, ok := families[metricName]
	if !ok {
		return nil
	}

	var out []*dto.Metric
	for _, m := range mf.Metric {
		if labelsMatch(m.GetLabel(), match) {
			out = append(out, m)
		}
	}
	return out
}

// Label returns the value of the named label on m, or "" if absent.
func Label(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func labelsMatch(labels []*dto.LabelPair, match map[string]string) bool {
	got := make(map[string]string, len(labels))
	for _, l := range labels {
		got[l.GetName()] = l.GetValue()
	}
	for k, v := range match {
		if got[k] != v {
			return false
		}
	}
	return true
}
