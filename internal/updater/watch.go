package updater

import (
	"context"
	"errors"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/exitcode"
)

// Watch checks immediately and then on every interval. It returns after a
// successful replacement so the supervisor or tray can restart the app.
func (e *Engine) Watch(ctx context.Context, interval time.Duration, onCheck func(UpdateResult, error)) error {
	if interval <= 0 { interval = 6 * time.Hour }
	check := func() error {
		result, err := e.Update(ctx, UpdateRequest{})
		if onCheck != nil { onCheck(result, err) }
		if err == nil && result.Updated { return nil }
		var coded *exitcode.Error
		if errors.As(err, &coded) && coded.Code == exitcode.UpToDate { return errContinue }
		if err != nil { return err }
		return errContinue
	}
	if err := check(); err != errContinue { return err }
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done(): return ctx.Err()
		case <-ticker.C:
			if err := check(); err != errContinue { return err }
		}
	}
}

var errContinue = errors.New("continue watching")
