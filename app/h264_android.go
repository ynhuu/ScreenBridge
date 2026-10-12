//go:build android && arm64 && cgo

package main

import (
	"errors"
	"time"

	"screenbridge/native"
)

type androidH264Encoder struct {
	encoder *native.H264Encoder
}

func newH264Encoder(scale, frameRate, bitRate int) (h264Encoder, int, int, error) {
	encoder, width, height, err := native.NewH264Encoder(scale, frameRate, bitRate)
	if err != nil {
		return nil, 0, 0, err
	}
	return &androidH264Encoder{encoder: encoder}, width, height, nil
}

func (encoder *androidH264Encoder) NextFrame() ([]byte, time.Duration, error) {
	frame, timestamp, err := encoder.encoder.NextFrame()
	if errors.Is(err, native.ErrH264NoFrame) {
		return nil, 0, errH264NoFrame
	}
	if errors.Is(err, native.ErrH264Idle) {
		return nil, 0, errH264Idle
	}
	if errors.Is(err, native.ErrH264DisplayChanged) {
		return nil, 0, errH264DisplayChanged
	}
	return frame, timestamp, err
}

func (encoder *androidH264Encoder) Close() { encoder.encoder.Close() }

func (encoder *androidH264Encoder) Wake(keyFrame bool) error { return encoder.encoder.Wake(keyFrame) }

func (encoder *androidH264Encoder) DisplaySize() (int, int) {
	return encoder.encoder.DisplayWidth, encoder.encoder.DisplayHeight
}
