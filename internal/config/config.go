package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	CredentialsSecretName      string
	CredentialsSecretNamespace string
	OutputSecretName           string
	OutputSecretNamespace      string
	OutputSecretKey            string
	RefreshInterval            time.Duration // 0 means: derive from expires_in
	HealthPort                 string
}

func Load() (*Config, error) {
	cfg := &Config{
		OutputSecretKey: getEnvOrDefault("OUTPUT_SECRET_KEY", "token"),
		HealthPort:      getEnvOrDefault("HEALTH_PORT", "8080"),
	}

	required := []struct {
		env string
		dst *string
	}{
		{"CREDENTIALS_SECRET_NAME", &cfg.CredentialsSecretName},
		{"CREDENTIALS_SECRET_NAMESPACE", &cfg.CredentialsSecretNamespace},
		{"OUTPUT_SECRET_NAME", &cfg.OutputSecretName},
		{"OUTPUT_SECRET_NAMESPACE", &cfg.OutputSecretNamespace},
	}
	for _, r := range required {
		v := os.Getenv(r.env)
		if v == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", r.env)
		}
		*r.dst = v
	}

	if raw := os.Getenv("REFRESH_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid REFRESH_INTERVAL %q: %w", raw, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("REFRESH_INTERVAL must be positive")
		}
		cfg.RefreshInterval = d
	}

	return cfg, nil
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
