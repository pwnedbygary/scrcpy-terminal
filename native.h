#ifndef SCT_NATIVE_H
#define SCT_NATIVE_H

#include <stdint.h>

// scrcpy wire codec ids (big-endian u32 on the wire)
#define SCT_CODEC_H264 0x68323634u
#define SCT_CODEC_H265 0x68323635u
#define SCT_CODEC_AV1  0x00617631u
#define SCT_CODEC_VP8  0x00767038u
#define SCT_CODEC_VP9  0x00767039u
#define SCT_CODEC_OPUS 0x6f707573u
#define SCT_CODEC_AAC  0x00616163u
#define SCT_CODEC_FLAC 0x666c6163u
#define SCT_CODEC_RAW  0x00726177u

// ---- video decoder (libavcodec) + scaler (libswscale) ----
// codec_id: one of SCT_CODEC_*; width/height: from the first session packet.
// Returns opaque decoder handle, NULL on error.
void *sct_vdec_open(int codec_id, int width, int height);

// Feed one annex-b media packet (config packet must already be prepended).
// pts is in microseconds; returns 0 on success, -1 on error.
int sct_vdec_send(void *d, const uint8_t *data, int len, int64_t pts);

// Pull the next decoded frame. Returns 1 if a new frame is available,
// 0 if none yet (EAGAIN), -1 on error/EOF.
int sct_vdec_recv(void *d);

// Scale/convert the last received frame to RGBA into dst at dst_stride
// (bytes per row). Destination region is dst_w x dst_h starting at dst (which
// may point into a larger canvas, allowing centered letterboxing).
// Returns 0 on success, -1 if no frame is available.
// out_fw/out_fh receive the last frame's width/height.
int sct_vdec_scale(void *d, uint8_t *dst, int dst_stride, int dst_w, int dst_h,
                   int *out_fw, int *out_fh);

void sct_vdec_free(void *d);

// ---- audio decoder (libavcodec opus/aac/flac) ----
// Returns opaque handle, or NULL if codec_id is RAW (see sct_adec_is_raw).
void *sct_adec_open(int codec_id);
int sct_adec_is_raw(int codec_id);

// Feed one media packet. Returns 0 on success, -1 on error.
int sct_adec_send(void *d, const uint8_t *data, int len, int64_t pts);

// Decode to interleaved s16 stereo 48kHz. Fills out_bytes with the number of
// bytes written to out (<= out_cap). Returns 0 on success (possibly 0 bytes),
// -1 on error.
int sct_adec_recv(void *d, uint8_t *out, int out_cap, int *out_bytes);

void sct_adec_free(void *d);

// ---- JPEG encoder (libavcodec mjpeg) ----
// The web/window display path ships one still image per video frame. mjpeg
// encodes at several hundred MPix/s on a modern CPU, which keeps the whole
// pipeline far cheaper than shipping raw BGR0 to the browser.
//
// Open an encoder for a fixed w x h (must be even). Returns NULL on error.
void *sct_jenc_open(int w, int h, int quality);

// Encode one BGR0 (memory [B,G,R,X]) frame of w x h pixels, stride in bytes.
// On success *out points at an internal buffer of *out_size JPEG bytes that
// stays valid until the next call. Returns 0, or -1 on error.
// stride == 0 means tightly packed rows (w*4).
int sct_jenc_encode(void *e, const uint8_t *bgr0, int stride, uint8_t **out, int *out_size);

// Zero-copy variant for the network path: writes the JPEG directly into
// dst+dst_off when it fits in dst_cap (never writes more than dst_cap bytes).
// On success *out_size is the JPEG length, and the caller's buffer can go
// straight to the socket with no intermediate copy. Returns 0, or -1 if the
// frame does not fit (caller retries with a bigger buffer) or on error.
int sct_jenc_encode_to(void *e, const uint8_t *bgr0, int stride,
                       uint8_t *dst, int dst_off, int dst_cap, int *out_size);

// Upper bound for one encoded frame of this encoder's geometry, so callers can
// size a buffer once instead of retrying.
int sct_jenc_max_size(void *e);

// Drop the internal output buffers (call after a burst when the source resized
// or the encoder is idle, to give memory back).
void sct_jenc_shrink(void *e);

void sct_jenc_free(void *e);

// ---- terminal cell packing (SIMD: AVX2/SSE on x86, NEON on aarch64) ----
// rgba: W x H RGBA buffer (H even). keys: W/1 * H/2 uint64 cells.
// Cell (x,y) -> keys[y*W+x] = quantized(top_rgb32) | quantized(bottom_rgb32)<<32
// Quantization: RGB channels masked to 0xE0 (3-bit) to merge neighbor runs.
void sct_pack_cells(const uint8_t *rgba, int w, int h, uint64_t *keys);

// ---- s16 gain (SIMD; q8 fixed point, 256 = 1.0, 0 = mute) ----
void sct_gain_s16(int16_t *buf, int n, int gain_q8);

const char *sct_av_version(void);

#endif
