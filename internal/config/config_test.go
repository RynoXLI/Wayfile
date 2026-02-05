package config

import (
	"testing"
)

func TestDatabaseConfig_URL(t *testing.T) {
	tests := []struct {
		name   string
		config DatabaseConfig
		want   string
	}{
		{
			name: "standard configuration",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Database: "wayfile",
				User:     "postgres",
				Password: "postgres",
				SSLMode:  "disable",
			},
			want: "postgres://postgres:postgres@localhost:5432/wayfile?sslmode=disable",
		},
		{
			name: "production configuration with SSL",
			config: DatabaseConfig{
				Host:     "db.example.com",
				Port:     5432,
				Database: "production_db",
				User:     "admin",
				Password: "secure_password",
				SSLMode:  "require",
			},
			want: "postgres://admin:secure_password@db.example.com:5432/production_db?sslmode=require",
		},
		{
			name: "custom port",
			config: DatabaseConfig{
				Host:     "db.local",
				Port:     5433,
				Database: "testdb",
				User:     "testuser",
				Password: "testpass",
				SSLMode:  "prefer",
			},
			want: "postgres://testuser:testpass@db.local:5433/testdb?sslmode=prefer",
		},
		{
			name: "password with special characters",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Database: "wayfile",
				User:     "user",
				Password: "p@ssw0rd!#$",
				SSLMode:  "disable",
			},
			want: "postgres://user:p%40ssw0rd%21%23$@localhost:5432/wayfile?sslmode=disable",
		},
		{
			name: "empty password",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Database: "wayfile",
				User:     "postgres",
				Password: "",
				SSLMode:  "disable",
			},
			want: "postgres://postgres:@localhost:5432/wayfile?sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.URL()
			if got != tt.want {
				t.Errorf("DatabaseConfig.URL() = %v, want %v", got, tt.want)
			}
		})
	}
}
