// Package config handles application configuration loading and management
package config

import (
	"fmt"
	"net/url"

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
	Port int    `mapstructure:"port"`
	Host string `mapstructure:"host"`
}

// DatabaseConfig holds database-related configuration
type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Database string `mapstructure:"database"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	SSLMode  string `mapstructure:"sslmode"`
}

// URL constructs the PostgreSQL connection URL from individual fields
func (d DatabaseConfig) URL() string {
	userinfo := url.UserPassword(d.User, d.Password)
	u := &url.URL{
		Scheme: "postgres",
		User:   userinfo,
		Host:   fmt.Sprintf("%s:%d", d.Host, d.Port),
		Path:   "/" + d.Database,
	}
	q := u.Query()
	q.Set("sslmode", d.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
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
