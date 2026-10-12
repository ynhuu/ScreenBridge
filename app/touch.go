package main

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type touchSessionOwner struct {
	width  int
	height int
}

type touchInjector interface {
	Down(width, height, x, y int) error
	Move(x, y int) error
	Up(x, y int) error
	Cancel() error
}

func (s *serverApp) handleTouchControl(owner *touchSessionOwner, request controlWSRequest) controlWSResponse {
	response := controlWSResponse{ID: request.ID}
	action, ok := request.Params["action"].(string)
	if !ok {
		response.Error = "touch action is required"
		return response
	}
	x, errX := touchInteger(request.Params, "x")
	y, errY := touchInteger(request.Params, "y")
	if errX != nil || errY != nil {
		response.Error = "touch x and y must be non-negative integers"
		return response
	}

	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	switch action {
	case "down":
		width, errWidth := touchInteger(request.Params, "width")
		height, errHeight := touchInteger(request.Params, "height")
		if errWidth != nil || errHeight != nil || width < 16 || height < 16 || width > 16384 || height > 16384 ||
			x < 0 || y < 0 || x >= width || y >= height {
			response.Error = "touch bounds are invalid"
			return response
		}
		if s.touchOwner != nil {
			response.Error = "another touch is already active"
			return response
		}
		if s.touch == nil {
			response.Error = "touch injection is unavailable"
			return response
		}
		if err := s.touch.Down(width, height, x, y); err != nil {
			response.Error = fmt.Sprintf("touch down failed: %v", err)
			return response
		}
		owner.width, owner.height = width, height
		s.touchOwner = owner
	case "move", "up", "cancel":
		if s.touchOwner != owner {
			response.Error = "this WebSocket does not own the active touch"
			return response
		}
		if x < 0 || y < 0 || x >= owner.width || y >= owner.height {
			response.Error = "touch coordinates are outside the active display"
			return response
		}
		switch action {
		case "move":
			if err := s.touch.Move(x, y); err != nil {
				response.Error = fmt.Sprintf("touch move failed: %v", err)
				return response
			}
		case "up":
			err := s.touch.Up(x, y)
			s.touchOwner = nil
			if err != nil {
				response.Error = fmt.Sprintf("touch up failed: %v", err)
				return response
			}
		case "cancel":
			err := s.touch.Cancel()
			s.touchOwner = nil
			if err != nil {
				response.Error = fmt.Sprintf("touch cancel failed: %v", err)
				return response
			}
		}
	default:
		response.Error = "touch action must be down, move, up, or cancel"
		return response
	}
	response.OK = true
	s.notifyVideoActivity()
	return response
}

func (s *serverApp) cancelTouchOwner(owner *touchSessionOwner) {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	if s.touchOwner != owner {
		return
	}
	if s.touch != nil {
		if err := s.touch.Cancel(); err != nil {
			s.logger.Printf("release touch after WebSocket close: %v", err)
		}
	}
	s.touchOwner = nil
}

func touchInteger(params map[string]any, key string) (int, error) {
	var value int64
	switch raw := params[key].(type) {
	case json.Number:
		parsed, err := raw.Int64()
		if err != nil {
			return 0, err
		}
		value = parsed
	case string:
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return 0, err
		}
		value = parsed
	default:
		return 0, fmt.Errorf("%s is not an integer", key)
	}
	if int64(int(value)) != value || value < 0 {
		return 0, fmt.Errorf("%s is outside the supported range", key)
	}
	return int(value), nil
}
