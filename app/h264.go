package main

import (
	"errors"
	"time"
)

var errH264NoFrame = errors.New("MediaCodec has no output frame ready")
var errH264Idle = errors.New("screen is unchanged")
var errH264DisplayChanged = errors.New("display size changed")

type h264Encoder interface {
	DisplaySize() (int, int)
	Wake(keyFrame bool) error
	// NextFrame returns Annex-B data and its monotonic capture timestamp.
	NextFrame() ([]byte, time.Duration, error)
	Close()
}
