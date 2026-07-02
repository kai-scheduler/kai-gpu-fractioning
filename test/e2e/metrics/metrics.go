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
