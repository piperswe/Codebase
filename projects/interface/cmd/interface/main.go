package main

import (
	"fmt"
	"os"

	"codebase.bid/lib/go/notify"
	"codebase.bid/projects/interface/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "smoke" {
		fmt.Printf("interface %s", version.Version)
		return
	}
	notify.Ready()
	fmt.Println("Hello, world!")
}
