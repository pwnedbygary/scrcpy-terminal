// sct native core: libavcodec/libswscale/libswresample glue + SIMD pixel ops.
#include "native.h"

#include <libavcodec/avcodec.h>
#include <libavutil/channel_layout.h>
#include <libavutil/imgutils.h>
#include <libavutil/samplefmt.h>
#include <libswscale/swscale.h>
#include <libswresample/swresample.h>

#include <stdlib.h>
#include <string.h>

// ---------------------------------------------------------------------------
// Video: libavcodec decode + libswscale convert-to-RGBA
// ---------------------------------------------------------------------------

typedef struct {
    AVCodecContext *ctx;
    AVPacket *pkt;
    AVFrame *frame;
    AVFrame *last;
    int have_last;
    struct SwsContext *sws;
    int sws_src_w, sws_src_h;
    int sws_dst_w, sws_dst_h;
} sct_vdec;

static enum AVCodecID sct_to_avcodec(int codec_id) {
    switch (codec_id) {
        case SCT_CODEC_H264: return AV_CODEC_ID_H264;
        case SCT_CODEC_H265: return AV_CODEC_ID_HEVC;
        case SCT_CODEC_AV1:  return AV_CODEC_ID_AV1;
        case SCT_CODEC_VP8:  return AV_CODEC_ID_VP8;
        case SCT_CODEC_VP9:  return AV_CODEC_ID_VP9;
        default: return AV_CODEC_ID_NONE;
    }
}

void *sct_vdec_open(int codec_id, int width, int height) {
    enum AVCodecID id = sct_to_avcodec(codec_id);
    if (id == AV_CODEC_ID_NONE) return NULL;
    const AVCodec *codec = avcodec_find_decoder(id);
    if (!codec) return NULL;

    sct_vdec *d = calloc(1, sizeof(*d));
    if (!d) return NULL;

    d->ctx = avcodec_alloc_context3(codec);
    if (!d->ctx) { free(d); return NULL; }
    d->ctx->width = width;
    d->ctx->height = height;
    d->ctx->pix_fmt = AV_PIX_FMT_YUV420P;
    d->ctx->flags |= AV_CODEC_FLAG_LOW_DELAY;
    d->ctx->thread_count = 1; // keep decode latency minimal

    if (avcodec_open2(d->ctx, codec, NULL) < 0) {
        avcodec_free_context(&d->ctx);
        free(d);
        return NULL;
    }
    d->pkt = av_packet_alloc();
    d->frame = av_frame_alloc();
    d->last = av_frame_alloc();
    if (!d->pkt || !d->frame || !d->last) {
        sct_vdec_free(d);
        return NULL;
    }
    d->have_last = 0;
    return d;
}

int sct_vdec_send(void *v, const uint8_t *data, int len, int64_t pts) {
    sct_vdec *d = v;
    if (!d) return -1;
    av_packet_unref(d->pkt);
    if (av_new_packet(d->pkt, len) < 0) return -1;
    memcpy(d->pkt->data, data, len);
    d->pkt->pts = pts;
    d->pkt->dts = pts;
    return avcodec_send_packet(d->ctx, d->pkt) == 0 ? 0 : -1;
}

int sct_vdec_recv(void *v) {
    sct_vdec *d = v;
    if (!d) return -1;
    int ret = avcodec_receive_frame(d->ctx, d->frame);
    if (ret == AVERROR(EAGAIN)) return 0;
    if (ret < 0) return -1;
    // keep a ref so sct_vdec_scale can run after subsequent sends
    av_frame_unref(d->last);
    if (av_frame_ref(d->last, d->frame) < 0) return -1;
    d->have_last = 1;
    return 1;
}

int sct_vdec_scale(void *v, uint8_t *dst, int dst_stride, int dst_w, int dst_h,
                   int *out_fw, int *out_fh) {
    sct_vdec *d = v;
    if (!d || !d->have_last) return -1;
    AVFrame *f = d->last;

    if (d->sws_src_w != f->width || d->sws_src_h != f->height ||
        d->sws_dst_w != dst_w || d->sws_dst_h != dst_h) {
        if (d->sws) sws_freeContext(d->sws);
        d->sws = sws_getContext(f->width, f->height, f->format,
                                dst_w, dst_h, AV_PIX_FMT_RGBA,
                                SWS_BILINEAR, NULL, NULL, NULL);
        if (!d->sws) return -1;
        d->sws_src_w = f->width;
        d->sws_src_h = f->height;
        d->sws_dst_w = dst_w;
        d->sws_dst_h = dst_h;
    }
    const uint8_t *src[4] = { f->data[0], f->data[1], f->data[2], NULL };
    const int src_stride[4] = { f->linesize[0], f->linesize[1], f->linesize[2], 0 };

    uint8_t *dstp[] = { dst, NULL, NULL, NULL };
    int dst_stride_arr[] = { dst_stride, 0, 0, 0 };
    sws_scale(d->sws, src, src_stride, 0, f->height, dstp, dst_stride_arr);

    if (out_fw) *out_fw = f->width;
    if (out_fh) *out_fh = f->height;
    return 0;
}

void sct_vdec_free(void *v) {
    sct_vdec *d = v;
    if (!d) return;
    if (d->sws) sws_freeContext(d->sws);
    if (d->last) av_frame_free(&d->last);
    if (d->frame) av_frame_free(&d->frame);
    if (d->pkt) av_packet_free(&d->pkt);
    if (d->ctx) avcodec_free_context(&d->ctx);
    free(d);
}

// ---------------------------------------------------------------------------
// Audio: libavcodec decode (opus/aac/flac) + swresample to s16 stereo 48k
// ---------------------------------------------------------------------------

typedef struct {
    AVCodecContext *ctx;
    AVPacket *pkt;
    AVFrame *frame;
    SwrContext *swr;
    int swr_in_fmt;
    int swr_in_rate;
} sct_adec;

void *sct_adec_open(int codec_id) {
    enum AVCodecID id;
    switch (codec_id) {
        case SCT_CODEC_OPUS: id = AV_CODEC_ID_OPUS; break;
        case SCT_CODEC_AAC:  id = AV_CODEC_ID_AAC;  break;
        case SCT_CODEC_FLAC: id = AV_CODEC_ID_FLAC; break;
        default: return NULL;
    }
    const AVCodec *codec = avcodec_find_decoder(id);
    if (!codec) return NULL;

    sct_adec *d = calloc(1, sizeof(*d));
    if (!d) return NULL;

    d->ctx = avcodec_alloc_context3(codec);
    if (!d->ctx) { free(d); return NULL; }
    AVChannelLayout stereo = AV_CHANNEL_LAYOUT_STEREO;
    av_channel_layout_copy(&d->ctx->ch_layout, &stereo);
    d->ctx->sample_rate = 48000;
    if (codec_id == SCT_CODEC_FLAC) d->ctx->sample_fmt = AV_SAMPLE_FMT_S16;

    if (avcodec_open2(d->ctx, codec, NULL) < 0) {
        avcodec_free_context(&d->ctx);
        free(d);
        return NULL;
    }
    d->pkt = av_packet_alloc();
    d->frame = av_frame_alloc();
    d->swr_in_fmt = -1;
    d->swr_in_rate = 0;
    if (!d->pkt || !d->frame) {
        sct_adec_free(d);
        return NULL;
    }
    return d;
}

int sct_adec_is_raw(int codec_id) {
    return codec_id == SCT_CODEC_RAW;
}

int sct_adec_send(void *v, const uint8_t *data, int len, int64_t pts) {
    sct_adec *d = v;
    if (!d) return -1;
    av_packet_unref(d->pkt);
    if (av_new_packet(d->pkt, len) < 0) return -1;
    memcpy(d->pkt->data, data, len);
    d->pkt->pts = pts;
    d->pkt->dts = pts;
    int ret = avcodec_send_packet(d->ctx, d->pkt);
    return ret == 0 || ret == AVERROR(EAGAIN) ? 0 : -1;
}

int sct_adec_recv(void *v, uint8_t *out, int out_cap, int *out_bytes) {
    sct_adec *d = v;
    *out_bytes = 0;
    if (!d) return -1;

    int ret = avcodec_receive_frame(d->ctx, d->frame);
    if (ret == AVERROR(EAGAIN)) return 0;
    if (ret < 0) return -1;

    if (d->swr_in_fmt != d->frame->format || d->swr_in_rate != d->frame->sample_rate) {
        if (d->swr) swr_free(&d->swr);
        AVChannelLayout out_ch = AV_CHANNEL_LAYOUT_STEREO;
        AVChannelLayout in_ch = AV_CHANNEL_LAYOUT_STEREO;
        swr_alloc_set_opts2(&d->swr, &out_ch, AV_SAMPLE_FMT_S16, 48000,
                            &in_ch, d->frame->format, d->frame->sample_rate,
                            0, NULL);
        if (!d->swr || swr_init(d->swr) < 0) return -1;
        d->swr_in_fmt = d->frame->format;
        d->swr_in_rate = d->frame->sample_rate;
    }

    int max_out = out_cap / 4; // s16 stereo: 4 bytes per frame
    int n = swr_convert(d->swr, &out, max_out,
                        (const uint8_t **) d->frame->extended_data,
                        d->frame->nb_samples);
    if (n < 0) return -1;
    *out_bytes = n * 4;
    return 0;
}

void sct_adec_free(void *v) {
    sct_adec *d = v;
    if (!d) return;
    if (d->swr) swr_free(&d->swr);
    if (d->frame) av_frame_free(&d->frame);
    if (d->pkt) av_packet_free(&d->pkt);
    if (d->ctx) avcodec_free_context(&d->ctx);
    free(d);
}

const char *sct_av_version(void) { return avcodec_configuration(); }

// ---------------------------------------------------------------------------
// Cell packing: W x H RGBA -> (W x H/2) uint64 cells.
// Each cell = top pixel (fg) + bottom pixel (bg) of one half-block character.
// RGB channels are quantized to 3-bit (mask 0xE0) so equal neighbors merge
// into long runs, which is what makes terminal output cheap.
// ---------------------------------------------------------------------------

static inline uint64_t sct_mkkey(uint32_t top, uint32_t bot) {
    return ((uint64_t) (top & 0x00E0E0E0u)) | ((uint64_t) (bot & 0x00E0E0E0u) << 32);
}

#if defined(__x86_64__) && defined(__AVX2__)

#include <immintrin.h>

static void sct_pack_cells_simd(const uint8_t *rgba, int w, int h, uint64_t *keys) {
    const int rows = h / 2;
    const __m256i mask = _mm256_set1_epi32(0x00E0E0E0u);
    for (int y = 0; y < rows; ++y) {
        uint32_t *top = (uint32_t *) rgba + (size_t) (2 * y) * w;
        uint32_t *bot = top + w;
        uint64_t *row = keys + (size_t) y * w;
        int x = 0;
        for (; x + 8 <= w; x += 8) {
            __m256i t = _mm256_and_si256(
                _mm256_loadu_si256((const __m256i *) (top + x)), mask);
            __m256i b = _mm256_and_si256(
                _mm256_loadu_si256((const __m256i *) (bot + x)), mask);
            // interleave (t0,b0,t1,b1..) per 128-bit lane
            __m256i lo = _mm256_unpacklo_epi32(t, b); // lane0: k0,k1  lane1: k4,k5
            __m256i hi = _mm256_unpackhi_epi32(t, b); // lane0: k2,k3  lane1: k6,k7
            __m256i r0 = _mm256_permute2x128_si256(lo, hi, 0x20); // k0..k3
            __m256i r1 = _mm256_permute2x128_si256(lo, hi, 0x31); // k4..k7
            _mm256_storeu_si256((__m256i *) (row + x), r0);
            _mm256_storeu_si256((__m256i *) (row + x + 4), r1);
        }
        for (; x < w; ++x) {
            row[x] = sct_mkkey(top[x], bot[x]);
        }
    }
}

#elif defined(__aarch64__)

#include <arm_neon.h>

static void sct_pack_cells_simd(const uint8_t *rgba, int w, int h, uint64_t *keys) {
    const int rows = h / 2;
    const uint32x4_t mask = vdupq_n_u32(0x00E0E0E0u);
    for (int y = 0; y < rows; ++y) {
        uint32_t *top = (uint32_t *) rgba + (size_t) (2 * y) * w;
        uint32_t *bot = top + w;
        uint64_t *row = keys + (size_t) y * w;
        int x = 0;
        for (; x + 4 <= w; x += 4) {
            uint32x4_t t = vandq_u32(vld1q_u32(top + x), mask);
            uint32x4_t b = vandq_u32(vld1q_u32(bot + x), mask);
            uint32x4_t lo = vzip1q_u32(t, b); // k0,k1 per 64-bit lane
            uint32x4_t hi = vzip2q_u32(t, b); // k2,k3
            vst1q_u64((uint64_t *) (row + x), vreinterpretq_u64_u32(lo));
            vst1q_u64((uint64_t *) (row + x + 2), vreinterpretq_u64_u32(hi));
        }
        for (; x < w; ++x) {
            row[x] = sct_mkkey(top[x], bot[x]);
        }
    }
}

#else

static void sct_pack_cells_simd(const uint8_t *rgba, int w, int h, uint64_t *keys) {
    const int rows = h / 2;
    for (int y = 0; y < rows; ++y) {
        const uint32_t *top = (const uint32_t *) rgba + (size_t) (2 * y) * w;
        const uint32_t *bot = top + w;
        uint64_t *row = keys + (size_t) y * w;
        for (int x = 0; x < w; ++x) {
            row[x] = sct_mkkey(top[x], bot[x]);
        }
    }
}

#endif

void sct_pack_cells(const uint8_t *rgba, int w, int h, uint64_t *keys) {
    sct_pack_cells_simd(rgba, w, h, keys);
}

// ---------------------------------------------------------------------------
// s16 fixed-point gain (q8). Used for local volume + mute.
// ---------------------------------------------------------------------------

#if defined(__x86_64__) && defined(__AVX2__)

static void sct_gain_s16_simd(int16_t *buf, int n, int g) {
    const __m256i gain = _mm256_set1_epi32(g);
    int i = 0;
    for (; i + 16 <= n; i += 16) {
        __m128i lo = _mm_loadu_si128((const __m128i *) (buf + i));
        __m128i hi = _mm_loadu_si128((const __m128i *) (buf + i + 8));
        __m256i w0 = _mm256_cvtepi16_epi32(lo);
        __m256i w1 = _mm256_cvtepi16_epi32(hi);
        w0 = _mm256_srai_epi32(_mm256_mullo_epi32(w0, gain), 8);
        w1 = _mm256_srai_epi32(_mm256_mullo_epi32(w1, gain), 8);
        __m256i p0 = _mm256_packs_epi32(w0, w1); // 16x int16
        _mm256_storeu_si256((__m256i *) (buf + i), p0);
    }
    for (; i < n; ++i) buf[i] = (int16_t) ((buf[i] * g) >> 8);
}

#elif defined(__aarch64__)

static void sct_gain_s16_simd(int16_t *buf, int n, int g) {
    const int32x4_t gain = vdupq_n_s32(g);
    int i = 0;
    for (; i + 8 <= n; i += 8) {
        int16x8_t v = vld1q_s16(buf + i);
        int32x4_t w0 = vmovl_s16(vget_low_s16(v));
        int32x4_t w1 = vmovl_high_s16(v);
        w0 = vshrq_n_s32(vmulq_s32(w0, gain), 8);
        w1 = vshrq_n_s32(vmulq_s32(w1, gain), 8);
        vst1q_s16(buf + i, vcombine_s16(vqmovn_s32(w0), vqmovn_s32(w1)));
    }
    for (; i < n; ++i) buf[i] = (int16_t) ((buf[i] * g) >> 8);
}

#else

static void sct_gain_s16_simd(int16_t *buf, int n, int g) {
    for (int i = 0; i < n; ++i) buf[i] = (int16_t) ((buf[i] * g) >> 8);
}

#endif

void sct_gain_s16(int16_t *buf, int n, int gain_q8) {
    sct_gain_s16_simd(buf, n, gain_q8);
}
