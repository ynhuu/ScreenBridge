package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host              string
	Port              int
	BootDelay         time.Duration
	DumpTimeout       time.Duration
	HandlerTimeout    time.Duration
	ScreenshotQuality int
}

func defaultConfig() Config {
	return Config{Host: "0.0.0.0", Port: 7913, BootDelay: 10 * time.Second,
		DumpTimeout: 15 * time.Second, HandlerTimeout: 20 * time.Second, ScreenshotQuality: 80}
}

func loadConfig(path string) (Config, error) {
	config := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config, nil
		}
		return config, err
	}
	values := make(map[string]string)
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return config, fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNumber+1)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if value := values["HOST"]; value != "" {
		config.Host = value
	}
	if err := setInt(values, "PORT", &config.Port, 1, 65535); err != nil {
		return config, err
	}
	if err := setDurationSeconds(values, "BOOT_DELAY", &config.BootDelay, 0, 300); err != nil {
		return config, err
	}
	if err := setDurationSeconds(values, "DUMP_TIMEOUT", &config.DumpTimeout, 1, 120); err != nil {
		return config, err
	}
	if err := setDurationSeconds(values, "HANDLER_TIMEOUT", &config.HandlerTimeout, 1, 300); err != nil {
		return config, err
	}
	if err := setInt(values, "SCREENSHOT_QUALITY", &config.ScreenshotQuality, 0, 100); err != nil {
		return config, err
	}
	return config, nil
}

func setInt(values map[string]string, key string, target *int, min, max int) error {
	value, ok := values[key]
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || strconv.Itoa(parsed) != value || parsed < min || parsed > max {
		return fmt.Errorf("invalid %s: expected integer from %d to %d", key, min, max)
	}
	*target = parsed
	return nil
}

func setDurationSeconds(values map[string]string, key string, target *time.Duration, min, max int) error {
	seconds := int(target.Seconds())
	if err := setInt(values, key, &seconds, min, max); err != nil {
		return err
	}
	if _, ok := values[key]; ok {
		*target = time.Duration(seconds) * time.Second
	}
	return nil
}
