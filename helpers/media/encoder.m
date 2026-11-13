#import "encoder.h"

#import <CoreMedia/CoreMedia.h>
#import <VideoToolbox/VideoToolbox.h>

static uint64_t hostMicros(void) {
  return (uint64_t)(CMTimeGetSeconds(CMClockGetTime(CMClockGetHostTimeClock())) * 1e6);
}

@interface HammockEncoder ()
- (void)emit:(CMSampleBufferRef)sample refresh:(BOOL)refresh;
@end

// What VideoToolbox holds for a session (its output callback's refcon): the
// encoder, weakly. The session retains it until it's invalidated, so a frame
// still in flight then finds no encoder rather than a freed one.
@interface HammockEncoderBox : NSObject
@property(weak) HammockEncoder *encoder;
@end
@implementation HammockEncoderBox
@end

@implementation HammockEncoder {
  VTCompressionSessionRef _session;
  VTPixelTransferSessionRef _scaler;
  VTPixelRotationSessionRef _rotator;
  CVPixelBufferPoolRef _rotPool;
  int _rotW, _rotH, _rotAngle;  // what _rotator and _rotPool were made for
  CVPixelBufferPoolRef _pool;
  int _width, _height;  // encoded size
  int _srcW, _srcH;     // source size the session was made for
  int _maxPixels;
  double _frameAspect;  // 0: the source's
  BOOL _wantKey, _wantRefresh;
  BOOL _sharp;  // the session running is the sharp tier (HEVC 4:4:4)
  BOOL _ltr;       // the session does LTR
  BOOL _anyAcked;  // an LTR has been acknowledged since the session started
  NSMutableArray<NSNumber *> *_acked;  // to pass with the next frame
  HammockEncoderOutput _output;
  void *_box;              // the session's HammockEncoderBox, retained for it
  CFAbsoluteTime _failedAt;  // a session couldn't be made: not again for a while
  NSMutableDictionary<NSNumber *, NSValue *> *_crops;  // frames in flight that are cropped, by capture time
}

- (instancetype)initWithBitrate:(int)bitsPerSecond fps:(int)fps maxPixels:(int)maxPixels output:(HammockEncoderOutput)output {
  if ((self = [super init])) {
    _bitrate = bitsPerSecond;
    _fps = fps > 0 ? fps : 60;
    _maxPixels = maxPixels;
    _output = [output copy];
    _acked = [NSMutableArray array];
    _wantKey = YES;
  }
  return self;
}

- (void)dealloc {
  [self invalidate];
}

- (void)invalidate {
  if (_session) {
    // Frames in flight come out first (to the box, which still has us).
    VTCompressionSessionCompleteFrames(_session, kCMTimeInvalid);
    VTCompressionSessionInvalidate(_session);
    CFRelease(_session);
    _session = NULL;
  }
  if (_box) {
    ((__bridge HammockEncoderBox *)_box).encoder = nil;
    CFRelease(_box);
    _box = NULL;
  }
  if (_scaler) {
    VTPixelTransferSessionInvalidate(_scaler);
    CFRelease(_scaler);
    _scaler = NULL;
  }
  if (_pool) {
    CVPixelBufferPoolRelease(_pool);
    _pool = NULL;
  }
  [self dropRotator];
}

- (void)setBitrate:(int)bitrate {
  _bitrate = bitrate;
  if (_sharp) [self applySharpRate];
  else if (_session) VTSessionSetProperty(_session, kVTCompressionPropertyKey_AverageBitRate, (__bridge CFNumberRef) @(bitrate));
}

- (void)setFps:(int)fps {
  _fps = fps > 0 ? fps : 60;
  if (_session && !_sharp) VTSessionSetProperty(_session, kVTCompressionPropertyKey_ExpectedFrameRate, (__bridge CFNumberRef) @(_fps));
}

- (int)encodedWidth {
  return _session ? _width : 0;
}

- (int)encodedHeight {
  return _session ? _height : 0;
}

- (void)requestKeyframe {
  _wantKey = YES;
}

- (void)requestRefresh {
  _wantRefresh = YES;
}

- (int)maxPixels {
  return _maxPixels;
}

- (void)setMaxPixels:(int)maxPixels {
  if (maxPixels == _maxPixels) return;
  _maxPixels = maxPixels;
  [self invalidate];  // made again, at this size, with the next frame
  _srcW = _srcH = 0;
  _wantKey = YES;
}

- (double)frameAspect {
  return _frameAspect;
}

- (void)setFrameAspect:(double)frameAspect {
  if (fabs(frameAspect - _frameAspect) < 0.001) return;
  _frameAspect = frameAspect;
  [self invalidate];  // made again, in this shape, with the next frame
  _srcW = _srcH = 0;
  _wantKey = YES;
}

- (void)setHevc:(BOOL)hevc {
  if (hevc == _hevc) return;
  _hevc = hevc;
  [self invalidate];  // made again, in this tier, with the next frame
  _srcW = _srcH = 0;
  _wantKey = YES;
}

// The sharp tier's quality (the bitrate cap is what limits it, in practice),
// and the frame rate it's told to expect (see startSharp).
static const double kSharpQuality = 0.6;
static const int kSharpExpectedFPS = 120;

// The sharp tier's cap: what the link carries in a second, and no burst of
// more than a fifth of that in any tenth of a second (a dragged window
// made single frames of a whole second's worth, which arrived too late).
- (void)applySharpRate {
  if (!_session || !_sharp) return;
  int bytes = MAX(_bitrate, 1000000) / 8;
  NSArray *limits = @[ @(bytes), @1, @(bytes / 5), @0.1 ];
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_DataRateLimits, (__bridge CFArrayRef)limits);
}

- (void)acknowledgeLTR:(NSArray<NSNumber *> *)tokens {
  for (NSNumber *t in tokens) {
    if (t.unsignedLongLongValue > 0) [_acked addObject:@(t.unsignedLongLongValue - 1)];
  }
}

enum { kRecoveryKey = 1, kRecoveryRefresh = 2 };

static void outputCallback(void *refcon, void *frameRefcon, OSStatus status, VTEncodeInfoFlags flags, CMSampleBufferRef sample) {
  HammockEncoder *enc = ((__bridge HammockEncoderBox *)refcon).encoder;
  if (!enc) return;  // invalidated, or gone
  intptr_t recovery = (intptr_t)frameRefcon;
  if (status != noErr || !sample || (flags & kVTEncodeInfo_FrameDropped)) {
    // A dropped recovery frame: ask again on the next one (the daemon
    // also asks again if nothing comes). Weakly: it may be gone by then.
    __weak HammockEncoder *w = enc;
    dispatch_queue_t q = enc.workQueue ?: dispatch_get_main_queue();
    if (recovery == kRecoveryKey) dispatch_async(q, ^{ [w requestKeyframe]; });
    if (recovery == kRecoveryRefresh) dispatch_async(q, ^{ [w requestRefresh]; });
    return;
  }
  [enc emit:sample refresh:recovery == kRecoveryRefresh];
}

- (BOOL)start:(int)srcW height:(int)srcH {
  [self invalidate];
  _srcW = srcW;
  _srcH = srcH;
  _width = srcW;
  _height = srcH;
  int maxPx = _maxPixels;
  if (!_hevc && _lowLatencyMaxPixels > 0) maxPx = maxPx > 0 ? MIN(maxPx, _lowLatencyMaxPixels) : _lowLatencyMaxPixels;
  if (_frameAspect > 0) {
    // The viewer's shape, with as many pixels as the source's own (or
    // fewer, as maxPixels says).
    double area = maxPx > 0 ? MIN((double)maxPx, (double)srcW * srcH) : (double)srcW * srcH;
    _width = ((int)sqrt(area * _frameAspect)) & ~1;
    _height = ((int)sqrt(area / _frameAspect)) & ~1;
  } else if (maxPx > 0 && (long)srcW * srcH > maxPx) {
    double k = sqrt((double)maxPx / ((double)srcW * srcH));
    _width = ((int)(srcW * k)) & ~1;
    _height = ((int)(srcH * k)) & ~1;
  }
  // What the hardware H.264 encoder takes: 4096 a side, and a 4K frame's
  // area (a 5K or 6K display, a whole one shared, is scaled to fit).
  double fit = MIN(1.0, MIN(4096.0 / _width, 4096.0 / _height));
  fit = MIN(fit, sqrt(4096.0 * 2304.0 / ((double)_width * _height)));
  if (fit < 1) {
    _width = ((int)(_width * fit)) & ~1;
    _height = ((int)(_height * fit)) & ~1;
  }
  HammockEncoderBox *box = [HammockEncoderBox new];
  box.encoder = self;
  _sharp = NO;
  if (_hevc && [self startSharp:box]) {
    _box = (__bridge_retained void *)box;
    _failedAt = 0;
    [_acked removeAllObjects];
    _ltr = NO;
    _anyAcked = NO;
    VTCompressionSessionPrepareToEncodeFrames(_session);
    [self makeScaler];
    _wantKey = YES;
    return YES;
  }
  NSDictionary *spec = @{(__bridge NSString *)kVTVideoEncoderSpecification_EnableLowLatencyRateControl : @YES};
  OSStatus s = VTCompressionSessionCreate(NULL, _width, _height, kCMVideoCodecType_H264, (__bridge CFDictionaryRef)spec, NULL, NULL,
                                          outputCallback, (__bridge void *)box, &_session);
  if (s != noErr) {
    // Said once per failure, not for every frame that tries again.
    if (_failedAt == 0) fprintf(stderr, "encoder: VTCompressionSessionCreate %d (trying again each second)\n", (int)s);
    _failedAt = CFAbsoluteTimeGetCurrent();
    _session = NULL;
    return NO;
  }
  _failedAt = 0;
  _box = (__bridge_retained void *)box;
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
  if (VTSessionSetProperty(_session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_ConstrainedHigh_AutoLevel) != noErr) {
    VTSessionSetProperty(_session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_ConstrainedBaseline_AutoLevel);
  }
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_AverageBitRate, (__bridge CFNumberRef) @(_bitrate));
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_ExpectedFrameRate, (__bridge CFNumberRef) @(_fps));
  // Keyframes only when asked (a new viewer, a lost frame).
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_MaxKeyFrameInterval, (__bridge CFNumberRef) @(INT32_MAX));
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_PrioritizeEncodingSpeedOverQuality, kCFBooleanTrue);
  // Long-term references for loss recovery (tokens belong to this session).
  _ltr = VTSessionSetProperty(_session, kVTCompressionPropertyKey_EnableLTR, kCFBooleanTrue) == noErr;
  _anyAcked = NO;
  [_acked removeAllObjects];
  VTCompressionSessionPrepareToEncodeFrames(_session);
  [self makeScaler];
  _wantKey = YES;
  return YES;
}

// Scaling, or copying a live surface: a transfer into buffers of our own.
- (void)makeScaler {
  VTPixelTransferSessionCreate(NULL, &_scaler);
  // Crops to a frame's clean aperture when it has one (a zoomed-in view).
  VTSessionSetProperty(_scaler, kVTPixelTransferPropertyKey_ScalingMode, kVTScalingMode_CropSourceToCleanAperture);
  NSDictionary *attrs = @{(__bridge NSString *)kCVPixelBufferPixelFormatTypeKey : @(kCVPixelFormatType_32BGRA),
                          (__bridge NSString *)kCVPixelBufferWidthKey : @(_width),
                          (__bridge NSString *)kCVPixelBufferHeightKey : @(_height),
                          (__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey : @{}};
  CVPixelBufferPoolCreate(NULL, NULL, (__bridge CFDictionaryRef)attrs, &_pool);
}

// The sharp tier: HEVC 4:4:4 from the hardware encoder's normal mode, in
// real time with no frame delay, driven by quality and capped by the link.
- (BOOL)startSharp:(HammockEncoderBox *)box {
  NSDictionary *spec = @{(__bridge NSString *)kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder : @YES};
  OSStatus s = VTCompressionSessionCreate(NULL, _width, _height, kCMVideoCodecType_HEVC, (__bridge CFDictionaryRef)spec, NULL, NULL,
                                          outputCallback, (__bridge void *)box, &_session);
  if (s != noErr) {
    fprintf(stderr, "encoder: no HEVC session (%d): H.264 instead\n", (int)s);
    _session = NULL;
    return NO;
  }
  // The profile has no constant in the SDK; a 4:2:0 profile if it's refused.
  if (VTSessionSetProperty(_session, kVTCompressionPropertyKey_ProfileLevel, CFSTR("HEVC_Main444_AutoLevel")) != noErr) {
    fprintf(stderr, "encoder: no HEVC 4:4:4: 4:2:0\n");
    VTSessionSetProperty(_session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_HEVC_Main_AutoLevel);
  }
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_MaxFrameDelayCount, (__bridge CFNumberRef) @0);
  // As fast as it goes: told of fewer frames, a real-time encoder spreads
  // each one's work over its interval to save power (measured at 3072×1728:
  // 12.7 ms a frame expecting 30, spikes to 21; 10.4 expecting 120, at most
  // 11). Its bits follow quality and the cap, not this.
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_ExpectedFrameRate, (__bridge CFNumberRef) @(kSharpExpectedFPS));
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_MaxKeyFrameInterval, (__bridge CFNumberRef) @(INT32_MAX));
  VTSessionSetProperty(_session, kVTCompressionPropertyKey_Quality, (__bridge CFNumberRef) @(kSharpQuality));
  _sharp = YES;
  [self applySharpRate];
  return YES;
}

- (void)encodeSurface:(IOSurfaceRef)surface {
  if (!surface) return;
  CVPixelBufferRef pb = NULL;
  if (CVPixelBufferCreateWithIOSurface(NULL, surface, NULL, &pb) != kCVReturnSuccess || !pb) return;
  CVPixelBufferRef turned = _rotation % 360 ? [self rotate:pb] : NULL;
  // A live surface (a simulator's framebuffer) keeps being drawn into while
  // VideoToolbox reads it, which tore frames: encoded from a copy (a turned
  // frame is one already).
  [self encode:turned ?: pb live:turned == NULL];
  if (turned) CVPixelBufferRelease(turned);
  CVPixelBufferRelease(pb);
}

- (void)dropRotator {
  if (_rotator) {
    VTPixelRotationSessionInvalidate(_rotator);
    CFRelease(_rotator);
    _rotator = NULL;
  }
  if (_rotPool) {
    CVPixelBufferPoolRelease(_rotPool);
    _rotPool = NULL;
  }
}

// The frame turned by rotation degrees clockwise (a new buffer), or NULL.
- (CVPixelBufferRef)rotate:(CVPixelBufferRef)pb CF_RETURNS_RETAINED {
  int angle = ((_rotation % 360) + 360) % 360;
  int w = (int)CVPixelBufferGetWidth(pb), h = (int)CVPixelBufferGetHeight(pb);
  if (!_rotator || !_rotPool || w != _rotW || h != _rotH || angle != _rotAngle) {
    [self dropRotator];
    if (VTPixelRotationSessionCreate(NULL, &_rotator) != noErr) return NULL;
    CFStringRef r = angle == 90 ? kVTRotation_CW90 : angle == 180 ? kVTRotation_180 : kVTRotation_CCW90;
    VTSessionSetProperty(_rotator, kVTPixelRotationPropertyKey_Rotation, r);
    BOOL sideways = angle != 180;
    NSDictionary *attrs = @{
      (id)kCVPixelBufferWidthKey : @(sideways ? h : w),
      (id)kCVPixelBufferHeightKey : @(sideways ? w : h),
      (id)kCVPixelBufferPixelFormatTypeKey : @(CVPixelBufferGetPixelFormatType(pb)),
      (id)kCVPixelBufferIOSurfacePropertiesKey : @{},
    };
    if (CVPixelBufferPoolCreate(NULL, NULL, (__bridge CFDictionaryRef)attrs, &_rotPool) != kCVReturnSuccess) {
      [self dropRotator];
      return NULL;
    }
    _rotW = w, _rotH = h, _rotAngle = angle;
  }
  CVPixelBufferRef out = NULL;
  if (CVPixelBufferPoolCreatePixelBuffer(NULL, _rotPool, &out) != kCVReturnSuccess) return NULL;
  if (VTPixelRotationSessionRotateImage(_rotator, pb, out) != noErr) {
    CVPixelBufferRelease(out);
    return NULL;
  }
  return out;
}

- (void)encodePixelBuffer:(CVPixelBufferRef)buffer {
  [self encode:buffer live:NO];
}

- (void)encode:(CVPixelBufferRef)buffer live:(BOOL)live {
  int w = (int)CVPixelBufferGetWidth(buffer), h = (int)CVPixelBufferGetHeight(buffer);
  if (!_session || w != _srcW || h != _srcH) {
    // After a failure, a second before trying again (not every frame).
    if (!_session && _failedAt > 0 && CFAbsoluteTimeGetCurrent() - _failedAt < 1 && w == _srcW && h == _srcH) return;
    if (![self start:w height:h]) return;
  }
  CVPixelBufferRef src = buffer, scaled = NULL;
  // The part of the frame wanted, in its pixels (even, inside it).
  CGRect crop = CGRectNull;
  if (!CGRectIsEmpty(_crop) && (_crop.size.width < 0.999 || _crop.size.height < 0.999)) {
    CGRect px = CGRectIntegral(CGRectMake(_crop.origin.x * w, _crop.origin.y * h, _crop.size.width * w, _crop.size.height * h));
    px = CGRectIntersection(px, CGRectMake(0, 0, w, h));
    if (!CGRectIsEmpty(px)) crop = CGRectMake((int)px.origin.x & ~1, (int)px.origin.y & ~1, (int)px.size.width & ~1, (int)px.size.height & ~1);
  }
  BOOL cropped = !CGRectIsNull(crop);
  // The transfer crops to the source's clean aperture (its size, and its
  // centre's offset from the frame's), set on a buffer of our own over the
  // same pixels: the frame itself is shared with other viewers' encoders,
  // on other threads.
  CVPixelBufferRef own = NULL;
  if (cropped) {
    IOSurfaceRef surface = CVPixelBufferGetIOSurface(buffer);
    if (surface && CVPixelBufferCreateWithIOSurface(NULL, surface, NULL, &own) == kCVReturnSuccess && own) buffer = own;
    else cropped = NO, crop = CGRectNull;
  }
  if (cropped) {
    NSDictionary *ap = @{
      (__bridge NSString *)kCVImageBufferCleanApertureWidthKey : @(crop.size.width),
      (__bridge NSString *)kCVImageBufferCleanApertureHeightKey : @(crop.size.height),
      (__bridge NSString *)kCVImageBufferCleanApertureHorizontalOffsetKey : @(CGRectGetMidX(crop) - w / 2.0),
      (__bridge NSString *)kCVImageBufferCleanApertureVerticalOffsetKey : @(CGRectGetMidY(crop) - h / 2.0),
    };
    CVBufferSetAttachment(buffer, kCVImageBufferCleanApertureKey, (__bridge CFDictionaryRef)ap, kCVAttachmentMode_ShouldNotPropagate);
  }
  if (_scaler && _pool && (live || cropped || _width != _srcW || _height != _srcH)) {
    if (CVPixelBufferPoolCreatePixelBuffer(NULL, _pool, &scaled) != kCVReturnSuccess) {
      if (own) CVPixelBufferRelease(own);
      return;
    }
    OSStatus te = VTPixelTransferSessionTransferImage(_scaler, buffer, scaled);
    if (own) CVPixelBufferRelease(own);
    own = NULL;
    if (te != noErr) {
      CVPixelBufferRelease(scaled);
      return;
    }
    src = scaled;
  }
  if (own) {  // no scaler to crop with: the whole frame goes
    CVPixelBufferRelease(own);
    cropped = NO;
  }
  uint64_t us = hostMicros();
  CMTime pts = CMTimeMake((int64_t)us, 1000000);
  // Which part this frame shows, for its output (by capture time).
  @synchronized(self) {
    if (!_crops) _crops = [NSMutableDictionary dictionary];
    if (cropped) _crops[@(us)] = [NSValue valueWithRect:NSMakeRect(crop.origin.x / w, crop.origin.y / h, crop.size.width / w, crop.size.height / h)];
  }
  NSMutableDictionary *opts = [NSMutableDictionary dictionary];
  BOOL refresh = NO;
  if (_wantKey) {
    // References don't reach back past a keyframe: start acknowledging again.
    [_acked removeAllObjects];
    _anyAcked = NO;
  }
  if (_ltr && _acked.count) {
    opts[(__bridge NSString *)kVTEncodeFrameOptionKey_AcknowledgedLTRTokens] = [_acked copy];
    [_acked removeAllObjects];
    _anyAcked = YES;
  }
  if (_wantRefresh && !_wantKey) {
    if (_ltr && _anyAcked) {
      opts[(__bridge NSString *)kVTEncodeFrameOptionKey_ForceLTRRefresh] = @YES;
      refresh = YES;
    } else {
      _wantKey = YES;
    }
  }
  if (_wantKey) opts[(__bridge NSString *)kVTEncodeFrameOptionKey_ForceKeyFrame] = @YES;
  // The frame's refcon: what it's recovering with, to ask again if it's dropped.
  intptr_t recovery = _wantKey ? kRecoveryKey : refresh ? kRecoveryRefresh : 0;
  _wantKey = _wantRefresh = NO;
  OSStatus s = VTCompressionSessionEncodeFrame(_session, src, pts, kCMTimeInvalid, (__bridge CFDictionaryRef)opts, (void *)recovery, NULL);
  if (s != noErr) {
    fprintf(stderr, "encoder: encode %d\n", (int)s);
    _wantKey = YES;
    if (s == kVTInvalidSessionErr) [self invalidate];
  }
  if (scaled) CVPixelBufferRelease(scaled);
}

static void appendNAL(NSMutableData *out, const void *bytes, size_t len) {
  uint32_t be = CFSwapInt32HostToBig((uint32_t)len);
  [out appendBytes:&be length:4];
  [out appendBytes:bytes length:len];
}

- (void)emit:(CMSampleBufferRef)sample refresh:(BOOL)refresh {
  BOOL key = YES;
  uint64_t ltr = 0;
  CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sample, false);
  if (atts && CFArrayGetCount(atts) > 0) {
    CFDictionaryRef a = CFArrayGetValueAtIndex(atts, 0);
    key = !CFDictionaryContainsKey(a, kCMSampleAttachmentKey_NotSync);
    CFNumberRef tok = CFDictionaryGetValue(a, kVTSampleAttachmentKey_RequireLTRAcknowledgementToken);
    int64_t t = 0;
    if (tok && CFNumberGetValue(tok, kCFNumberSInt64Type, &t) && t >= 0) ltr = (uint64_t)t + 1;
  }
  NSMutableData *out = [NSMutableData data];
  CMFormatDescriptionRef fmt = CMSampleBufferGetFormatDescription(sample);
  BOOL hevc = CMFormatDescriptionGetMediaSubType(fmt) == kCMVideoCodecType_HEVC;
  if (key) {
    size_t count = 0;
    int nalLen = 4;
    if (hevc) {
      CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(fmt, 0, NULL, NULL, &count, &nalLen);
    } else {
      CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, 0, NULL, NULL, &count, &nalLen);
    }
    for (size_t i = 0; i < count; i++) {
      const uint8_t *ps;
      size_t len;
      OSStatus e = hevc ? CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(fmt, i, &ps, &len, NULL, NULL)
                        : CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, i, &ps, &len, NULL, NULL);
      if (e == noErr) appendNAL(out, ps, len);
    }
  }
  CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sample);
  size_t total = CMBlockBufferGetDataLength(bb);
  NSMutableData *body = [NSMutableData dataWithLength:total];
  CMBlockBufferCopyDataBytes(bb, 0, total, body.mutableBytes);
  [out appendData:body];  // already AVCC with 4-byte lengths
  // The capture time exactly as given (µs: the timescale encode used), not
  // through a double, which came out a microsecond off and lost frames'
  // crops (looked up by it), so they showed stretched.
  CMTime ptsTime = CMSampleBufferGetPresentationTimeStamp(sample);
  uint64_t us = ptsTime.timescale == 1000000 ? (uint64_t)ptsTime.value : (uint64_t)llround(CMTimeGetSeconds(ptsTime) * 1e6);
  // The frame's own size (this runs on VideoToolbox's thread, while a resize
  // on main may be starting a session of another).
  CMVideoDimensions dim = CMVideoFormatDescriptionGetDimensions(CMSampleBufferGetFormatDescription(sample));
  HammockFrameInfo info = {.keyframe = key, .refresh = refresh && !key, .ptsMicros = us, .width = dim.width, .height = dim.height, .ltr = ltr, .hevc = hevc,
                        .encodedMicros = hostMicros()};
  @synchronized(self) {
    NSValue *c = _crops[@(us)];
    if (c) {
      info.cropped = YES;
      info.crop = NSRectToCGRect(c.rectValue);
      [_crops removeObjectForKey:@(us)];
    }
    if (_crops.count > 64) [_crops removeAllObjects];  // frames dropped: never coming out
  }
  HammockEncoderOutput o = _output;
  dispatch_async(dispatch_get_main_queue(), ^{ o(out, info); });
}

@end

void HammockEncoderWarmUp(void) {
  dispatch_semaphore_t done = dispatch_semaphore_create(0);
  HammockEncoder *e = [[HammockEncoder alloc] initWithBitrate:500000 fps:30 maxPixels:0 output:^(NSData *avcc, HammockFrameInfo info) {
    dispatch_semaphore_signal(done);
  }];
  CVPixelBufferRef pb = NULL;
  NSDictionary *attrs = @{(__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey : @{}};
  if (CVPixelBufferCreate(NULL, 64, 64, kCVPixelFormatType_32BGRA, (__bridge CFDictionaryRef)attrs, &pb) != kCVReturnSuccess) return;
  [e encodePixelBuffer:pb];
  [e invalidate];  // waits for the frame
  CVPixelBufferRelease(pb);
}

NSData *HammockMediaRecord(NSString *sid, NSData *avcc, HammockFrameInfo info) {
  NSData *sd = [sid dataUsingEncoding:NSUTF8StringEncoding];
  uint32_t len = (uint32_t)(1 + sd.length + 1 + 8 + 2 + 2 + 8 + (info.cropped ? 8 : 0) + avcc.length);
  NSMutableData *rec = [NSMutableData dataWithCapacity:4 + len];
  uint32_t be = CFSwapInt32HostToBig(len);
  [rec appendBytes:&be length:4];
  uint8_t sl = (uint8_t)sd.length, flags = (info.keyframe ? 1 : 0) | (info.refresh ? 2 : 0) | (info.hevc ? 4 : 0) | (info.cropped ? 8 : 0);
  [rec appendBytes:&sl length:1];
  [rec appendData:sd];
  [rec appendBytes:&flags length:1];
  uint64_t pbe = CFSwapInt64HostToBig(info.ptsMicros);
  [rec appendBytes:&pbe length:8];
  uint16_t wb = CFSwapInt16HostToBig((uint16_t)info.width), hb = CFSwapInt16HostToBig((uint16_t)info.height);
  [rec appendBytes:&wb length:2];
  [rec appendBytes:&hb length:2];
  uint64_t lbe = CFSwapInt64HostToBig(info.ltr);
  [rec appendBytes:&lbe length:8];
  if (info.cropped) {
    double v[4] = {info.crop.origin.x, info.crop.origin.y, info.crop.size.width, info.crop.size.height};
    for (int i = 0; i < 4; i++) {
      uint16_t q = CFSwapInt16HostToBig((uint16_t)lround(MIN(MAX(v[i], 0), 1) * 65535));
      [rec appendBytes:&q length:2];
    }
  }
  [rec appendData:avcc];
  return rec;
}

static const NSUInteger kHammockBacklog = 2 << 20;

BOOL HammockAdmitFrame(BOOL *lost, BOOL keyframe, NSUInteger queued, HammockEncoder *encoder) {
  if (queued > kHammockBacklog) {
    if (!*lost) fprintf(stderr, "media: %lu KB queued for the daemon: dropping a viewer's frames until it catches up\n", (unsigned long)(queued >> 10));
    *lost = YES;
    return NO;
  }
  if (*lost) {
    if (!keyframe) {
      [encoder requestKeyframe];  // to start again from
      return NO;
    }
    *lost = NO;
  }
  return YES;
}
