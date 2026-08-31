package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = serveCommand(os.Args[2:])
	case "check":
		err = checkCommand(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "help", "--help", "-h":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(2)
	}
}

func serveCommand(args []string) error {
	set := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := set.String("addr", "127.0.0.1:8080", "listen address")
	timeout := set.Duration("timeout", 60*time.Second, "timeout for each probe")
	if err := set.Parse(args); err != nil {
		return err
	}
	return serve(*addr, *timeout)
}

func checkCommand(args []string) error {
	set := flag.NewFlagSet("check", flag.ContinueOnError)
	baseURL := set.String("base-url", os.Getenv("LLMCONFORM_BASE_URL"), "provider base URL")
	model := set.String("model", os.Getenv("LLMCONFORM_MODEL"), "model name")
	routes := set.String("routes", "all", "comma-separated routes: chat,responses,messages")
	profile := set.String("profile", "", "target profile: openai, claude, gateway, custom")
	level := set.String("level", LevelStandard, "test level: quick, standard, full")
	format := set.String("format", "table", "output format: table or json")
	timeout := set.Duration("timeout", 60*time.Second, "timeout for each probe")
	if err := set.Parse(args); err != nil {
		return err
	}

	cfg := RunConfig{
		BaseURL: *baseURL,
		APIKey:  os.Getenv("LLMCONFORM_API_KEY"),
		Model:   *model,
		Profile: *profile,
		Level:   *level,
		Routes:  splitRoutes(*routes),
		Timeout: *timeout,
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	report := NewRunner().Run(context.Background(), cfg, nil)
	switch *format {
	case "json":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	case "table":
		printReportTable(os.Stdout, report)
	default:
		return fmt.Errorf("unsupported format %q", *format)
	}
	if report.Summary.Fail > 0 {
		os.Exit(1)
	}
	return nil
}

func splitRoutes(value string) []string {
	if value == "" || value == "all" {
		return allRouteIDs()
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if route := strings.TrimSpace(part); route != "" {
			result = append(result, route)
		}
	}
	return result
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `LLMConform checks LLM API route compatibility.

Usage:
  llmconform serve [--addr 127.0.0.1:8080]
	llmconform check --base-url URL --model MODEL [--profile gateway] [--level standard] [--routes all] [--format table|json]

Environment:
  LLMCONFORM_API_KEY   API key used for checks
  LLMCONFORM_BASE_URL  default provider base URL
  LLMCONFORM_MODEL     default model name`)
}
