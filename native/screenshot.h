#pragma once

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// Captures and WebP-encodes the internal display through Android's private
// SurfaceFlinger APIs. Returns zero on success and allocates *output.
int uia_capture_webp(int quality, int scale_divisor,
                     uint8_t** output, size_t* output_size);

// Encodes the raw header+pixels stream produced by /system/bin/screencap.
int uia_encode_screencap(const uint8_t* input, size_t input_size, int quality,
                         uint8_t** output, size_t* output_size,
                         uint32_t* width, uint32_t* height);

void uia_free_buffer(void* buffer);

#ifdef __cplusplus
}
#endif
