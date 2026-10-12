//go:build !android || !arm64 || !cgo

package main

import "errors"

type unavailableTouchInjector struct{}

func newTouchInjector() touchInjector { return &unavailableTouchInjector{} }

func (*unavailableTouchInjector) Down(width, height, x, y int) error { return errTouchUnavailable }

func (*unavailableTouchInjector) Move(x, y int) error { return errTouchUnavailable }

func (*unavailableTouchInjector) Up(x, y int) error { return errTouchUnavailable }

func (*unavailableTouchInjector) Cancel() error { return errTouchUnavailable }

var errTouchUnavailable = errors.New("NDK uinput touch injection is available only in the Android arm64 build")
