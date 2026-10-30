package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/s16vc/brain-connector/agent"
)

var version = "dev"

func main() {
	flags := flag.NewFlagSet("brain-connector", flag.ContinueOnError)
	token := flags.String("token", os.Getenv("BRAIN_CONNECTOR_TOKEN"), "connector token from Brain (or BRAIN_CONNECTOR_TOKEN)")
	target := flags.String("target", os.Getenv("BRAIN_CONNECTOR_TARGET"), "the database host:port this connector may reach (or BRAIN_CONNECTOR_TARGET)")
	server := flags.String("server", envOr("BRAIN_CONNECTOR_SERVER", agent.DefaultServer), "BrainData address (or BRAIN_CONNECTOR_SERVER)")
	insecure := flags.Bool("insecure", false, "allow a ws:// server address, for local testing only")
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println(version)
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With(slog.String("version", version))
	connector := &agent.Agent{
		Server:        *server,
		Token:         *token,
		Target:        *target,
		AllowInsecure: *insecure,
		Logger:        logger,
	}
	if err := connector.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "brain-connector:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("brain-connector starting", slog.String("server", *server), slog.String("target", *target))
	if err := connector.Run(ctx); err != nil {
		logger.Error("brain-connector stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
