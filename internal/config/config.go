// Package config handles application configuration loading and management
package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// Config holds the entire configuration for the application
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	NATS     NATSConfig     `mapstructure:"nats"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Logging  LoggingConfig  `mapstructure:"logging"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port           int    `mapstructure:"port"`
	Host           string `mapstructure:"host"`
	BaseURL        string `mapstructure:"base_url"`
	SigningSecret  string `mapstructure:"signing_secret"`
	ReadTimeout    int    `mapstructure:"read_timeout"`     // seconds
	WriteTimeout   int    `mapstructure:"write_timeout"`    // seconds
	IdleTimeout    int    `mapstructure:"idle_timeout"`     // seconds
	RateLimitRPS   int    `mapstructure:"rate_limit_rps"`   // requests per second
	RateLimitBurst int    `mapstructure:"rate_limit_burst"` // burst size
	MaxUploadSize  int64  `mapstructure:"max_upload_size"`  // bytes
	EnableDocs     bool   `mapstructure:"enable_docs"`      // enable /docs endpoint
}

// DatabaseConfig holds database-related configuration
type DatabaseConfig struct {
	URL string `mapstructure:"url"`
}

// NATSConfig holds NATS-related configuration
type NATSConfig struct {
	URL string `mapstructure:"url"`
}

// StorageConfig holds storage-related configuration
type StorageConfig struct {
	Type  string             `mapstructure:"type"`
	Local LocalStorageConfig `mapstructure:"local"`
}

// LocalStorageConfig holds configuration for local storage
type LocalStorageConfig struct {
	Path string `mapstructure:"path"`
}

// LoggingConfig holds logging-related configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Load reads the configuration from file and environment variables
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME/.wayfile")

	// Environment variable overrides
	viper.SetEnvPrefix("WAYFILE")
	viper.AutomaticEnv()

	// Bind environment variables
	err := viper.BindEnv("database.url", "DATABASE_URL")
	if err != nil {
		return nil, fmt.Errorf("failed to bind env variable: %w", err)
	}
	err = viper.BindEnv("nats.url", "NATS_URL")
	if err != nil {
		return nil, fmt.Errorf("failed to bind env variable: %w", err)
	}
	err = viper.BindEnv("storage.path", "STORAGE_PATH")
	if err != nil {
		return nil, fmt.Errorf("failed to bind env variable: %w", err)
	}

	// Set defaults
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.base_url", "http://localhost:8080")
	viper.SetDefault("server.read_timeout", 10)           // 10 seconds
	viper.SetDefault("server.write_timeout", 30)          // 30 seconds
	viper.SetDefault("server.idle_timeout", 120)          // 120 seconds
	viper.SetDefault("server.rate_limit_rps", 100)        // 100 requests per second
	viper.SetDefault("server.rate_limit_burst", 200)      // burst of 200
	viper.SetDefault("server.max_upload_size", 104857600) // 100 MB
	viper.SetDefault("server.enable_docs", true)
	viper.SetDefault("storage.type", "local")
	viper.SetDefault("storage.local.path", "./data/storage")
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate required fields
	if cfg.Server.SigningSecret == "" {
		return nil, fmt.Errorf("server.signing_secret is required for pre-signed URL security")
	}
	if cfg.Server.SigningSecret == "change-me-in-production-use-random-string" {
		return nil, fmt.Errorf(
			"server.signing_secret must be changed from default value in production",
		)
	}
	if cfg.Database.URL == "" {
		return nil, fmt.Errorf("database.url is required")
	}
	if cfg.NATS.URL == "" {
		return nil, fmt.Errorf("nats.url is required")
	}

	return &cfg, nil
}
