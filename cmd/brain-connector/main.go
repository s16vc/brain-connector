package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/s16vc/brain-connector/agent"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("brain-connector", flag.ContinueOnError)
	flags.SetOutput(stderr)
	token := flags.String("token", "", "connector token from Brain (or BRAIN_CONNECTOR_TOKEN)")
	target := flags.String("target", "", "the database host:port this connector may reach (or BRAIN_CONNECTOR_TARGET)")
	server := flags.String("server", "", "BrainData address (or BRAIN_CONNECTOR_SERVER, default "+agent.DefaultServer+")")
	insecure := flags.Bool("insecure", false, "allow a ws:// server address, for local testing only")
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	connector := &agent.Agent{
		Server:        firstSet(*server, getenv("BRAIN_CONNECTOR_SERVER"), agent.DefaultServer),
		Token:         firstSet(*token, getenv("BRAIN_CONNECTOR_TOKEN")),
		Target:        firstSet(*target, getenv("BRAIN_CONNECTOR_TARGET")),
		AllowInsecure: *insecure,
		Logger:        slog.New(slog.NewJSONHandler(stdout, nil)).With(slog.String("version", version)),
	}
	if err := connector.Validate(); err != nil {
		fmt.Fprintln(stderr, "brain-connector:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connector.Logger.Info("brain-connector starting", slog.String("server", connector.Server), slog.String("target", connector.Target))
	if err := connector.Run(ctx); err != nil {
		connector.Logger.Error("brain-connector stopped", slog.String("error", err.Error()))
		return 1
	}
	return 0
}

func firstSet(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
