package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gocrane/crane/pkg/cost/service"
)

func main() {
	configPath := flag.String("config", "/etc/crane-cost/config.json", "path to cost collector JSON configuration")
	validateOnly := flag.Bool("validate-config", false, "validate configuration and exit")
	flag.Parse()

	config, err := service.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *validateOnly {
		fmt.Println("cost collector configuration is valid")
		return
	}
	collector, err := service.New(config, log.Default())
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := collector.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
