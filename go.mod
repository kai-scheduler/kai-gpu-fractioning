module github.com/kai-scheduler/kai-gpu-fractioning

go 1.26.4

require (
	github.com/NVIDIA/go-nvml v0.13.3-1
	github.com/containerd/nri v0.12.1
	github.com/kai-scheduler/kai-gpu-fractioning/pkg v0.0.0
	google.golang.org/grpc v1.83.0
	k8s.io/apimachinery v0.36.3
	k8s.io/cri-api v0.36.3
)

replace github.com/kai-scheduler/kai-gpu-fractioning/pkg => ./pkg

require (
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/ttrpc v1.2.7 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/knqyf263/go-plugin v0.9.0 // indirect
	github.com/onsi/ginkgo/v2 v2.32.0 // indirect
	github.com/onsi/gomega v1.42.1 // indirect
	github.com/opencontainers/runtime-spec v1.3.0 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
)
