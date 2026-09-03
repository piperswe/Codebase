package notify

import (
	"context"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
)

var cancelWatchdog context.CancelFunc

// Ready signals to systemd that the service has initialized successfully. It
// also starts a watchdog loop if the service has a watchdog configured.
func Ready() {
	watchdogInterval, err := daemon.SdWatchdogEnabled(false)
	if err == nil && watchdogInterval > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		cancelWatchdog = cancel
		go func(watchdogInterval time.Duration) {
			ticker := time.NewTicker(watchdogInterval / 2)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_, _ = daemon.SdNotify(false, daemon.SdNotifyWatchdog)
				}
			}
		}(watchdogInterval)
	}
	_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)
}

// Stopping signals to systemd that the service is shutting down. The watchdog
// loop is also canceled.
func Stopping() {
	_, _ = daemon.SdNotify(false, daemon.SdNotifyStopping)
	if cancelWatchdog != nil {
		cancelWatchdog()
	}
}
