package main

import (
	"fmt"
	"os"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/piperswe/Codebase/projects/yago/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "smoke" {
		fmt.Printf("yago %s", version.Version)
		return
	}
	watchdogInterval, err := daemon.SdWatchdogEnabled(false)
	if err == nil {
		go func(watchdogInterval time.Duration) {
			for {
				time.Sleep(watchdogInterval / 2)
				_, _ = daemon.SdNotify(false, daemon.SdNotifyWatchdog)
			}
		}(watchdogInterval)
	}
	_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)
	fmt.Println("Hello, world!")
}
