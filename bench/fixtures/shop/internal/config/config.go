package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	Port              int     `json:"port"`
	Currency          string  `json:"currency"`
	TaxRate           float64 `json:"tax_rate"`
	SessionTTLMinutes int     `json:"session_ttl_minutes"`
	FreeShippingOver  int64   `json:"free_shipping_over"`
}

// Load reads a JSON config file.
// TODO: validate the port range
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Default() *Config {
	return &Config{Port: 8080, Currency: "USD", TaxRate: 0.1, SessionTTLMinutes: 15, FreeShippingOver: 10000}
}
