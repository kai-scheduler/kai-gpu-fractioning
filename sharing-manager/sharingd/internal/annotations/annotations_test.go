package annotations

import (
	"testing"

	"github.com/kai-scheduler/gpu-sharing/sharing-manager/common/configuration"
)

func TestParseToDecimalMB(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    string
		expectedErr bool
	}{
		{name: "4Gi to decimal MB", input: "4Gi", expected: "4294"},
		{name: "2048Mi to decimal MB", input: "2048Mi", expected: "2147"},
		{name: "1Gi to decimal MB", input: "1Gi", expected: "1073"},
		{name: "512Mi to decimal MB", input: "512Mi", expected: "536"},
		{name: "4096M SI megabytes", input: "4096M", expected: "4096"},
		{name: "500M SI megabytes", input: "500M", expected: "500"},
		{name: "1G SI gigabyte", input: "1G", expected: "1000"},
		{name: "1M minimum valid", input: "1M", expected: "1"},
		{name: "plain integer 4Gi in bytes", input: "4294967296", expected: "4294"},
		{name: "below 1MB is error", input: "4096", expectedErr: true},
		{name: "zero is error", input: "0", expectedErr: true},
		{name: "500Ki below 1MB", input: "500Ki", expectedErr: true},
		{name: "empty string", input: "", expectedErr: true},
		{name: "whitespace is invalid", input: "  2048Mi  ", expectedErr: true},
		{name: "garbage", input: "notanumber", expectedErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseToDecimalMB(tt.input)
			if tt.expectedErr {
				if err == nil {
					t.Errorf("parseToDecimalMB(%q) = %q, expected error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseToDecimalMB(%q) error = %v", tt.input, err)
				return
			}
			if got != tt.expected {
				t.Errorf("parseToDecimalMB(%q) = %q, expected %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseVisibleDevices(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    string
	}{
		{
			name:        "single UUID",
			annotations: map[string]string{VisibleDevicesAnnotation: "GPU-abc123"},
			expected:    "GPU-abc123",
		},
		{
			name:        "comma-separated UUIDs passed through verbatim",
			annotations: map[string]string{VisibleDevicesAnnotation: "GPU-abc123,GPU-def456"},
			expected:    "GPU-abc123,GPU-def456",
		},
		{
			name:        "surrounding whitespace trimmed",
			annotations: map[string]string{VisibleDevicesAnnotation: "  GPU-abc123  "},
			expected:    "GPU-abc123",
		},
		{
			name:        "index value passed through",
			annotations: map[string]string{VisibleDevicesAnnotation: "0"},
			expected:    "0",
		},
		{
			name:        "absent annotation yields empty",
			annotations: map[string]string{"other": "value"},
			expected:    "",
		},
		{
			name:        "blank annotation yields empty",
			annotations: map[string]string{VisibleDevicesAnnotation: "   "},
			expected:    "",
		},
		{
			name:        "nil annotations yields empty",
			annotations: nil,
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseVisibleDevices(tt.annotations); got != tt.expected {
				t.Errorf("ParseVisibleDevices() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestParseGPUMemoryAnnotations(t *testing.T) {
	tests := []struct {
		name            string
		annotations     map[string]string
		containerName   string
		prefix          string
		expectedRequest string
		expectedLimit   string
		expectedEmpty   bool
		expectedErr     bool
	}{
		{
			name: "both request and limit",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.trainer.request": "2048Mi",
				"nvidia.com/gpu-memory.container.trainer.limit":   "4Gi",
			},
			containerName:   "trainer",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "2147",
			expectedLimit:   "4294",
			expectedEmpty:   false,
		},
		{
			name: "only limit",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.main.limit": "1024M",
			},
			containerName:   "main",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "",
			expectedLimit:   "1024",
			expectedEmpty:   false,
		},
		{
			name: "only request",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.worker.request": "512Mi",
			},
			containerName:   "worker",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "536",
			expectedLimit:   "",
			expectedEmpty:   false,
		},
		{
			name: "no matching annotations",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.other.limit": "4096M",
			},
			containerName:   "main",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "",
			expectedLimit:   "",
			expectedEmpty:   true,
		},
		{
			name:            "nil annotations",
			annotations:     nil,
			containerName:   "main",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "",
			expectedLimit:   "",
			expectedEmpty:   true,
		},
		{
			name:            "empty annotations",
			annotations:     map[string]string{},
			containerName:   "main",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "",
			expectedLimit:   "",
			expectedEmpty:   true,
		},
		{
			name: "malformed limit value",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.main.limit": "not-a-number",
			},
			containerName: "main",
			prefix:        configuration.DefaultAnnotationPrefix,
			expectedErr:   true,
		},
		{
			name: "value below 1MB",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.main.limit": "500Ki",
			},
			containerName: "main",
			prefix:        configuration.DefaultAnnotationPrefix,
			expectedErr:   true,
		},
		{
			name: "multiple containers only target extracted",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.sidecar.limit": "1024M",
				"nvidia.com/gpu-memory.container.main.limit":    "4096M",
				"nvidia.com/gpu-memory.container.init.limit":    "512M",
			},
			containerName:   "main",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "",
			expectedLimit:   "4096",
			expectedEmpty:   false,
		},
		{
			name: "container name with dashes",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.my-training-job.limit": "2048M",
			},
			containerName:   "my-training-job",
			prefix:          configuration.DefaultAnnotationPrefix,
			expectedRequest: "",
			expectedLimit:   "2048",
			expectedEmpty:   false,
		},
		{
			name: "custom prefix",
			annotations: map[string]string{
				"gpu-sharing.kai.scheduler/container.main.limit": "4Gi",
			},
			containerName:   "main",
			prefix:          "gpu-sharing.kai.scheduler/container.",
			expectedRequest: "",
			expectedLimit:   "4294",
			expectedEmpty:   false,
		},
		{
			name: "empty prefix still matches bare container keys",
			annotations: map[string]string{
				"main.limit": "4Gi",
			},
			containerName:   "main",
			prefix:          "",
			expectedRequest: "",
			expectedLimit:   "4294",
			expectedEmpty:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseGPUMemoryAnnotations(tt.annotations, tt.containerName, tt.prefix)
			if tt.expectedErr {
				if err == nil {
					t.Errorf("ParseGPUMemoryAnnotations() = %+v, expected error", cfg)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseGPUMemoryAnnotations() error = %v", err)
				return
			}
			if cfg.Request != tt.expectedRequest {
				t.Errorf("Request = %q, expected %q", cfg.Request, tt.expectedRequest)
			}
			if cfg.Limit != tt.expectedLimit {
				t.Errorf("Limit = %q, expected %q", cfg.Limit, tt.expectedLimit)
			}
			if cfg.IsEmpty() != tt.expectedEmpty {
				t.Errorf("IsEmpty() = %v, expected %v", cfg.IsEmpty(), tt.expectedEmpty)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name        string
		in          GPUMemoryConfig
		wantRequest string
		wantLimit   string
	}{
		{"both set unchanged", GPUMemoryConfig{Request: "3221", Limit: "6442"}, "3221", "6442"},
		{"request only defaults limit", GPUMemoryConfig{Request: "4294"}, "4294", "4294"},
		{"limit only defaults request", GPUMemoryConfig{Limit: "6442"}, "6442", "6442"},
		{"empty stays empty", GPUMemoryConfig{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.ApplyDefaults()
			if got.Request != tt.wantRequest || got.Limit != tt.wantLimit {
				t.Errorf("ApplyDefaults(%+v) = {Request:%q Limit:%q}, want {Request:%q Limit:%q}",
					tt.in, got.Request, got.Limit, tt.wantRequest, tt.wantLimit)
			}
		})
	}
}
