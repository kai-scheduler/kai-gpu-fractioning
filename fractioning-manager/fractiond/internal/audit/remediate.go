package audit

import (
	"context"
	"log/slog"
)

// ContainerStopper stops a single container out of band. The NRI plugin API has
// no stop/kill call, so remediation must go through the container runtime (CRI
// StopContainer) or the host. Implementations must be safe to call from a
// background goroutine and must not panic on runtime/connection errors.
type ContainerStopper interface {
	// Stop terminates the container with the given runtime ID. For containerd,
	// the NRI container.Id equals the CRI container ID, so no translation is
	// needed. Returning an error leaves the container as-is; the caller logs and
	// moves on to the next violator.
	Stop(ctx context.Context, containerID string) error
}

// remediator acts on the violators the detector produces: it stops each
// offending container so kubelet recreates it through a healthy CreateContainer
// hook. It is deliberately separate from detection so the (side-effecting) stop
// path can be tested with a fake stopper and disabled independently.
type remediator struct {
	// stopper performs the actual stop. Must be non-nil.
	stopper ContainerStopper
	// log is the logger; defaults to slog.Default().
	log *slog.Logger
}

// remediate stops every violating container. It is best-effort: a stop failure
// on one container is logged and does not abort the rest, so a single stuck
// container cannot block remediation of the others. Callers run this off the NRI
// hot path (one pass per Synchronize).
func (r remediator) remediate(ctx context.Context, violators []violator) {
	for _, v := range violators {
		if err := r.stopper.Stop(ctx, v.containerID); err != nil {
			r.logger().Error("audit: failed to stop container missing GPU fractioning injection",
				"container", v.container,
				"pod", v.pod,
				"namespace", v.namespace,
				"containerID", v.containerID,
				"error", err,
			)
			continue
		}

		r.logger().Info("audit: stopped container missing GPU fractioning injection; kubelet will recreate it",
			"container", v.container,
			"pod", v.pod,
			"namespace", v.namespace,
			"containerID", v.containerID,
			"missing", v.missing,
		)
	}
}

func (r remediator) logger() *slog.Logger {
	if r.log != nil {
		return r.log
	}
	return slog.Default()
}
