// HammockEncoder: hardware H.264 for remote viewers of visual panes (ADR-0012).
//
// Low-latency VideoToolbox: real-time, no frame reordering, low-latency rate
// control, keyframes only when asked. Frames come out as AVCC (4-byte
// big-endian NAL lengths); a keyframe carries its parameter sets (VPS, SPS
// and PPS for HEVC, SPS and PPS for H.264) as NALs in front, so a viewer can
// start from any keyframe.
//
// Loss recovery uses long-term references (LTR): some frames carry a token;
// once the viewer is known to have that frame (and everything before it),
// the token is acknowledged, and a refresh is a P frame predicted from an
// acknowledged LTR: cheaper than a keyframe, and at full quality. With no
// LTR acknowledged yet (or no LTR support), a refresh is a keyframe.
//
// Shared by the helpers that own pixels (Hammock Simulator, Hammock Browser).
//
// The screen host (Hammock Screen) also has a sharp tier for good links
// (ADR-0014): HEVC with full colour detail (4:4:4), from the encoder's
// normal mode (the low-latency one is 4:2:0 only), driven by quality and
// capped by the bitrate; it has no LTR, so it recovers with keyframes.
#import <CoreGraphics/CoreGraphics.h>
#import <CoreVideo/CoreVideo.h>
#import <Foundation/Foundation.h>
#import <IOSurface/IOSurface.h>

typedef struct {
  BOOL keyframe;
  BOOL refresh;        // decodable from an acknowledged LTR: frames before it may be skipped
  uint64_t ptsMicros;  // capture time, host clock
  int width, height;
  uint64_t ltr;        // the frame's LTR token + 1 (0: not an LTR frame)
  BOOL hevc;           // HEVC (keyframes lead with VPS, SPS, PPS), else H.264
  uint64_t encodedMicros;  // when it came out of the encoder (host clock)
  BOOL cropped;        // shows only crop (fractions of the source)
  CGRect crop;
} HammockFrameInfo;

typedef void (^HammockEncoderOutput)(NSData *avcc, HammockFrameInfo info);

@interface HammockEncoder : NSObject
// Frames are encoded at the size of the first frame given (or scaled to fit
// maxPixels, if set); a different size later restarts the encoder with a
// keyframe.
- (instancetype)initWithBitrate:(int)bitsPerSecond fps:(int)fps maxPixels:(int)maxPixels output:(HammockEncoderOutput)output;
- (void)encodeSurface:(IOSurfaceRef)surface;
- (void)encodePixelBuffer:(CVPixelBufferRef)buffer;
- (void)requestKeyframe;
- (void)requestRefresh;
// The sharp tier (HEVC 4:4:4) instead of low-latency H.264: a change
// restarts the session (the next frame is a keyframe). Falls back to H.264
// where it can't be made.
@property(nonatomic) BOOL hevc;
// The most pixels the low-latency tier encodes, whatever maxPixels says
// (0: no more limit): a whole 4K display at a poor link's bitrate is
// mush, and its rate control drops most frames to pay for it.
@property(nonatomic) int lowLatencyMaxPixels;
// The most pixels frames are encoded at (0: the source's own); a change
// starts the session again, with a keyframe.
@property(nonatomic) int maxPixels;
// Where it asks itself again for a dropped keyframe or refresh: the queue
// it's used on (main unless set).
@property(nonatomic, strong) dispatch_queue_t workQueue;
// Only this part of each frame (fractions of it; CGRectZero: all of it),
// scaled to the encoded size, which doesn't change: zooming in on a screen
// sends its detail at full resolution, without a new session. Give it the
// source's aspect, or it's stretched.
@property(nonatomic) CGRect crop;
// The size frames are encoded at (0 until the first frame).
@property(nonatomic, readonly) int encodedWidth, encodedHeight;
// Tokens (as in HammockFrameInfo.ltr) of LTR frames the viewer has.
- (void)acknowledgeLTR:(NSArray<NSNumber *> *)tokens;
@property(nonatomic) int bitrate;
@property(nonatomic) int fps;
// Degrees clockwise (0, 90, 180, 270) to turn frames before encoding: a
// rotated simulator, whose framebuffer stays in the device's portrait.
@property(nonatomic) int rotation;
- (void)invalidate;
@end

// Encodes one small frame with low-latency H.264 and throws it away.
// Measured (macOS 27, M4 Pro): a process whose first VideoToolbox session
// is the sharp tier's HEVC hangs in its first VTCompressionSessionEncodeFrame
// (a sync XPC call to the encoder service that never returns); after an
// H.264 session, it doesn't. Call once at startup, before any HEVC.
#ifdef __cplusplus
extern "C"
#endif
void HammockEncoderWarmUp(void);

// The helpers' binary media record for a session's frame: u32 length (of the
// rest), u8 sid length, sid, u8 flags (1 keyframe, 2 refresh, 4 HEVC, 8 cropped), u64 capture
// time (µs), u16 width, u16 height, u64 LTR token + 1 (0: none), when cropped u16 x,
// y, w, h (fractions × 65535), then AVCC.
#ifdef __cplusplus
extern "C"
#endif
NSData *HammockMediaRecord(NSString *sid, NSData *avcc, HammockFrameInfo info);

// Whether a viewer's frame should be queued for the daemon, with queued bytes
// already waiting on its connection. Past a couple of megabytes (the daemon
// isn't keeping up, or its hold came late) frames are dropped, not queued, and
// that viewer is "lost" until a keyframe (asked of encoder) can go: it skips
// ahead rather than falling minutes behind, and other viewers carry on.
#ifdef __cplusplus
extern "C"
#endif
BOOL HammockAdmitFrame(BOOL *lost, BOOL keyframe, NSUInteger queued, HammockEncoder *encoder);
