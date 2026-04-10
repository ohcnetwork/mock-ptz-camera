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
	LogLevel  string
	Renderer  string
	PanoImage string
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
		LogLevel:  envOrDefault("LOG_LEVEL", "info"),
		Renderer:  envOrDefault("RENDERER", "pano"),
		PanoImage: envOrDefault("PANO_IMAGE", "assets/default_pano.jpg"),
	}
}

func (c *Config) RTSPAddress() string { return fmt.Sprintf(":%d", c.RTSPPort) }
func (c *Config) WebAddress() string  { return fmt.Sprintf(":%d", c.WebPort) }

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
