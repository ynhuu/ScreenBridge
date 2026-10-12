package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = os.Environ()
	return command.CombinedOutput()
}

func (s *serverApp) dumpUI(parent context.Context) ([]byte, error) {
	if err := os.MkdirAll(s.stateDir, 0755); err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(s.stateDir, "window-dump-*.xml")
	if err != nil {
		return nil, err
	}
	path := temporary.Name()
	_ = temporary.Close()
	_ = os.Remove(path)
	defer os.Remove(path)

	var commandOutput []byte
	if _, statErr := os.Stat("/system/bin/runcon"); statErr == nil {
		ctx, cancel := context.WithTimeout(parent, s.config.DumpTimeout)
		commandOutput, err = runCombined(ctx, "/system/bin/runcon", "u:r:shell:s0", "/system/bin/uiautomator", "dump", path)
		cancel()
		if err == nil {
			if data, readErr := os.ReadFile(path); readErr == nil && len(data) > 0 {
				return data, nil
			}
		}
	}

	_ = os.Remove(path)
	ctx, cancel := context.WithTimeout(parent, s.config.DumpTimeout)
	output, fallbackErr := runCombined(ctx, "/system/bin/uiautomator", "dump", path)
	cancel()
	if fallbackErr != nil {
		if len(commandOutput) > 0 {
			output = append(append(commandOutput, []byte("; ")...), output...)
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		if len(output) == 0 {
			output = commandOutput
		}
		return nil, fmt.Errorf("dump produced no XML: %s", strings.TrimSpace(string(output)))
	}
	return data, nil
}

func (s *serverApp) screenshot(ctx context.Context, quality, scale int) ([]byte, int, error) {
	if s.apiLevel == "32" && s.deviceABI == "arm64-v8a" {
		image, err := nativeCaptureWebP(quality, scale)
		if err == nil && len(image) > 0 {
			return image, scale, nil
		}
		if err != nil {
			s.logger.Printf("native capture failed; falling back to screencap: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	raw, err := runOutput(ctx, "/system/bin/screencap")
	if err != nil {
		return nil, 1, fmt.Errorf("screencap failed: %w: %s", err, strings.TrimSpace(string(raw)))
	}
	image, _, _, err := nativeEncodeScreencap(raw, quality)
	if err != nil {
		return nil, 1, fmt.Errorf("WebP encoding failed: %w", err)
	}
	return image, 1, nil
}

func (s *serverApp) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	quality, err := parseIntParam(r, "quality", false, s.config.ScreenshotQuality)
	if err != nil || quality > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "quality must be an integer from 0 to 100"})
		return
	}
	scale, err := parseIntParam(r, "scale", false, 1)
	if err != nil || (scale != 1 && scale != 2 && scale != 4) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "scale must be 1, 2, or 4"})
		return
	}
	image, actualScale, err := s.screenshot(r.Context(), quality, scale)
	if err != nil {
		s.logger.Printf("screenshot failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "screenshot failed"})
		return
	}
	w.Header().Set("X-Capture-Scale", fmt.Sprintf("%d", actualScale))
	writeBytes(w, http.StatusOK, "image/webp", image)
}

func runOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	return command.Output()
}
