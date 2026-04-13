package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Username  string
	Password  string
	RTSPPort  int
	WebPort   int
	Width     int
	Height    int
	FPS       int
	Bitrate   string
	LogLevel  string
	Renderer  string
	PanoImage string
	HostIP    string

	TLSEnabled  bool
	TLSCertFile string
	TLSKeyFile  string
	TLSCertDir  string
	TLSPort     int
}

func Load() *Config {
	return &Config{
		Username:  envOrDefault("CAMERA_USER", "admin"),
		Password:  envOrDefault("CAMERA_PASS", "admin"),
		RTSPPort:  envIntOrDefault("RTSP_PORT", 8554),
		WebPort:   envIntOrDefault("WEB_PORT", 8080),
		Width:     envIntOrDefault("WIDTH", 1280),
		Height:    envIntOrDefault("HEIGHT", 720),
		FPS:       envIntOrDefault("FPS", 30),
		Bitrate:   envOrDefault("BITRATE", "2M"),
		LogLevel:  envOrDefault("LOG_LEVEL", "info"),
		Renderer:  envOrDefault("RENDERER", "pano"),
		PanoImage: envOrDefault("PANO_IMAGE", "assets/default_pano.jpg"),
		HostIP:    os.Getenv("HOST_IP"),

		TLSEnabled:  envBoolOrDefault("TLS_ENABLED", true),
		TLSCertFile: os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:  os.Getenv("TLS_KEY_FILE"),
		TLSCertDir:  envOrDefault("TLS_CERT_DIR", "certs"),
		TLSPort:     envIntOrDefault("TLS_PORT", 0),
	}
}

func (c *Config) RTSPAddress() string { return fmt.Sprintf(":%d", c.RTSPPort) }
func (c *Config) WebAddress() string  { return fmt.Sprintf(":%d", c.WebPort) }
func (c *Config) TLSAddress() string  { return fmt.Sprintf(":%d", c.TLSPort) }

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBoolOrDefault(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
