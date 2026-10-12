#pragma once

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct UiaH264Encoder UiaH264Encoder;

int uia_h264_create(int scale_divisor, int frame_rate, int bit_rate,
                    UiaH264Encoder** encoder, uint32_t* width,
                    uint32_t* height, uint32_t* display_width,
                    uint32_t* display_height);
// Returns 0 with a frame, 1 when no frame is ready, 2 when idle, 3 on resize, or a
// negative error code. The returned access unit is Annex-B H.264.
int uia_h264_encode_next(UiaH264Encoder* encoder, uint8_t** output,
                         size_t* output_size, int* is_key_frame,
                         int64_t* presentation_time_us);
void uia_h264_destroy(UiaH264Encoder* encoder);
int uia_h264_wake(UiaH264Encoder* encoder, int key_frame);
void uia_h264_free_buffer(void* buffer);

#ifdef __cplusplus
}
#endif
