package main

import "time"

type Config struct {
	Proxy string
	Redis struct {
		Host     string
		Port     int
		Password string
	}
	Concurrency  int
	Attempts     int
	Timeout      time.Duration
	Interval     int
	Mirror       string
	Verbose      bool
	DataDir      string
	DistURL      string
	Dump         bool
	MetadataURL  string
	ProvidersURL string
}

var cfg = Config{
	Redis: struct {
		Host     string
		Port     int
		Password string
	}{Host: "127.0.0.1", Port: 6379, Password: ""},
	Concurrency:  30,
	Attempts:     5,
	Timeout:      30 * time.Second,
	Interval:     60,
	Mirror:       "https://packagist.org",
	DataDir:      "./data",
	DistURL:      "/dists/%package%/%reference%.%type%",
	Dump:         false,
	MetadataURL:  "/p2/%package%.json",
	ProvidersURL: "/p/%package%$%hash%.json",
}

func (cfg *Config) getMainUrl() string {
	return cfg.Mirror + "/packages.json"
}
