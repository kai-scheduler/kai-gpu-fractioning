module github.com/run-ai/gpu-sharing-operator

go 1.26.2

require (
	github.com/containerd/nri v0.12.0
	github.com/run-ai/gpu-sharing-operator/pkg v0.0.0
	k8s.io/apimachinery v0.36.2
)

replace github.com/run-ai/gpu-sharing-operator/pkg => ./pkg

require (
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/ttrpc v1.2.7 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/knqyf263/go-plugin v0.9.0 // indirect
	github.com/opencontainers/runtime-spec v1.3.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/mod v0.32.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230731190214-cbb8c96f2d6d // indirect
	google.golang.org/grpc v1.57.1 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
)
