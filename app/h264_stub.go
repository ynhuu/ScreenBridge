//go:build !android || !arm64 || !cgo

package main

import (
	"errors"
	"time"
)

type unavailableH264Encoder struct{}

func newH264Encoder(scale, frameRate, bitRate int) (h264Encoder, int, int, error) {
	return nil, 0, 0, errors.New("H.264 MediaCodec is only available in the Android arm64 build")
}

func (encoder *unavailableH264Encoder) NextFrame() ([]byte, time.Duration, error) {
	return nil, 0, errors.New("H.264 MediaCodec is unavailable")
}

func (encoder *unavailableH264Encoder) Close() {}

func (*unavailableH264Encoder) DisplaySize() (int, int) { return 0, 0 }

func (*unavailableH264Encoder) Wake(bool) error { return errors.New("H.264 MediaCodec is unavailable") }
