package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/piperswe/Codebase/projects/yago/internal/db"
	"github.com/piperswe/Codebase/projects/yago/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "smoke" {
		fmt.Printf("yago %s", version.Version)
		return
	}
	q, err := db.NewFromEnvironment(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer q.Close(context.Background())
	err = q.Migrate(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to migrate database: %v\n", err)
		os.Exit(1)
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
	userCount, err := q.GetUserCount(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get user count: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("User count: %d\n", userCount)
}
