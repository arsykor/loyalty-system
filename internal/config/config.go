package config

import (
	"flag"

	"github.com/caarlos0/env/v6"
)

type Config struct {
	RunAddress     string `env:"RUN_ADDRESS"`
	DatabaseURI    string `env:"DATABASE_URI"`
	AccrualAddress string `env:"ACCRUAL_SYSTEM_ADDRESS"`
}

func Load() *Config {
	cfg := &Config{}

	env.Parse(cfg)

	envRunAddress := cfg.RunAddress
	envDatabaseURI := cfg.DatabaseURI
	envAccrualAddress := cfg.AccrualAddress

	flag.StringVar(&cfg.RunAddress, "a", "localhost:8080", "HTTP server address")
	flag.StringVar(&cfg.DatabaseURI, "d", "", "Database connection string")
	flag.StringVar(&cfg.AccrualAddress, "r", "", "Accrual system address")

	flag.Parse()

	// Priority: env > flag > default
	if envRunAddress != "" {
		cfg.RunAddress = envRunAddress
	}
	if envDatabaseURI != "" {
		cfg.DatabaseURI = envDatabaseURI
	}
	if envAccrualAddress != "" {
		cfg.AccrualAddress = envAccrualAddress
	}

	return cfg
}
