package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Usage = "a packagist mirror tool"
	app.Description = "For create packagist mirror"
	app.Flags = append(app.Flags,

		&cli.IntFlag{
			Name:  "c",
			Usage: "Number of multiple requests to make at a time",
			Value: cfg.Concurrency,
		},
		&cli.IntFlag{
			Name:  "a",
			Usage: "Number of attempt times",
			Value: cfg.Attempts,
		},
		&cli.IntFlag{
			Name:  "i",
			Usage: "Interval",
			Value: cfg.Interval,
		},
		&cli.StringFlag{
			Name:  "mirror",
			Usage: "Mirror url",
			Value: cfg.Mirror,
		},
		&cli.StringFlag{
			Name:  "proxy",
			Usage: "Set proxy for request. eg: http://ip:port",
			Value: cfg.Proxy,
		},
		&cli.DurationFlag{
			Name:  "timeout",
			Usage: "timeout",
			Value: cfg.Timeout,
		},
		&cli.StringFlag{
			Name:  "redis.host",
			Usage: "Redis server hostname",
			Value: cfg.Redis.Host,
		},
		&cli.IntFlag{
			Name:  "redis.port",
			Usage: "Redis server port",
			Value: cfg.Redis.Port,
		},
		&cli.StringFlag{
			Name:  "redis.password",
			Usage: "Password to use when connecting to the server",
			Value: cfg.Redis.Password,
		},
		&cli.BoolFlag{
			Name:  "v",
			Usage: "Verbose",
			Value: cfg.Verbose,
		},
	)
	app.Action = runCmd

	app.Run(os.Args)
}

func runCmd(ctx *cli.Context) error {
	if s := ctx.String("proxy"); len(s) > 0 {
		cfg.Proxy = s
	}
	if v := ctx.Int("c"); v > 0 {
		cfg.Concurrency = v
	}
	if v := ctx.Int("a"); v > 0 {
		cfg.Attempts = v
	}
	if v := ctx.Int("i"); v > 0 {
		cfg.Interval = v
	}
	if s := ctx.String("mirror"); len(s) > 0 {
		cfg.Mirror = s
	}
	if s := ctx.Duration("timeout"); s > 0 {
		cfg.Timeout = s
	}
	if s := ctx.String("redis.host"); len(s) > 0 {
		cfg.Redis.Host = s
	}
	if v := ctx.Int("redis.port"); v > 0 {
		cfg.Redis.Port = v
	}
	if s := ctx.String("redis.password"); len(s) > 0 {
		cfg.Redis.Password = s
	}

	log.Printf("config: %+v\n", cfg)
	return run(cfg)
}
