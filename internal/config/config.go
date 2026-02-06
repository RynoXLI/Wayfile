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

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}
