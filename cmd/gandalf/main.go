// Gandalf is a high-performance LLM gateway that unifies multiple providers
// behind an OpenAI-compatible API.
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "configs/gandalf.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("gandalf", version)
		os.Exit(0)
	}

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
