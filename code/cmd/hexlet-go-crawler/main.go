package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"code/crawler"
	cli "github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:      "hexlet-go-crawler",
		Usage:     "analyze a website structure",
		ArgsUsage: "<url>",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "depth",
				Value: 10,
				Usage: "crawl depth",
			},
			&cli.IntFlag{
				Name:  "retries",
				Value: 1,
				Usage: "number of retries for failed requests",
			},
			&cli.DurationFlag{
				Name:  "delay",
				Usage: "delay between requests (example: 200ms, 1s)",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: 15 * time.Second,
				Usage: "per-request timeout",
			},
			&cli.Float64Flag{
				Name:  "rps",
				Usage: "limit requests per second (overrides delay)",
			},
			&cli.StringFlag{
				Name:  "user-agent",
				Usage: "custom user agent",
			},
			&cli.IntFlag{
				Name:  "workers",
				Value: 4,
				Usage: "number of concurrent workers",
			},
		},
		Action: runApp,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runApp(c *cli.Context) error {
	if c.Args().Len() < 1 {
		return cli.Exit("usage: hexlet-go-crawler [options] <url>", 1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	url := c.Args().First()

	delay := c.Duration("delay")
	rps := c.Float64("rps")
	if rps > 0 {
		perReq := time.Duration(float64(time.Second) / rps)
		if perReq > 0 {
			delay = perReq
		}
	}

	opts := crawler.Options{
		URL:         url,
		Depth:       c.Int("depth"),
		Retries:     c.Int("retries"),
		Delay:       delay,
		Timeout:     c.Duration("timeout"),
		UserAgent:   c.String("user-agent"),
		Concurrency: c.Int("workers"),
		IndentJSON:  true,
	}

	data, err := crawler.Analyze(ctx, opts)
	if err != nil {
		return cli.Exit(fmt.Sprintf("crawl error: %v", err), 1)
	}

	if _, err := os.Stdout.Write(data); err != nil {
		return cli.Exit(fmt.Sprintf("write error: %v", err), 1)
	}
	// финальный перенос строки
	if len(data) == 0 || data[len(data)-1] != '\n' {
		fmt.Println()
	}
	return nil
}
