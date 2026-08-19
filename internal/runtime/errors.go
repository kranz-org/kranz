package runtime

import (
	"errors"

	"github.com/kranz-org/kranz/internal/app"
)

// encodeError turns a Go error into the wire shape. app.PortConflictError is
// the one error type internal/ui reconstructs with errors.As to drive the
// port-conflict modal (see internal/ui/model_lifecycle.go), so it is the one
// error kind that carries structured fields instead of just text.
func encodeError(err error) errorPayload {
	var conflict *app.PortConflictError
	if errors.As(err, &conflict) {
		return errorPayload{
			Kind:         errorPortConflict,
			Message:      conflict.Error(),
			Service:      conflict.Service,
			Port:         conflict.Port,
			PID:          conflict.PID,
			Process:      conflict.Process,
			Command:      conflict.Command,
			OwnerService: conflict.OwnerService,
			External:     conflict.External,
		}
	}
	return errorPayload{Kind: errorGeneric, Message: err.Error()}
}

// decodeError reconstructs an error from the wire shape. A generic error
// only carries text across the wire: a client-side errors.Is/errors.As
// against a sentinel from internal/service or internal/app will not match a
// generic remote error, which is an accepted limitation of this stream — the
// one caller that needs structured matching (the port-conflict modal) gets
// its own kind.
func decodeError(payload errorPayload) error {
	switch payload.Kind {
	case errorPortConflict:
		return &app.PortConflictError{
			Service:      payload.Service,
			Port:         payload.Port,
			PID:          payload.PID,
			Process:      payload.Process,
			Command:      payload.Command,
			OwnerService: payload.OwnerService,
			External:     payload.External,
		}
	default:
		return errors.New(payload.Message)
	}
}
