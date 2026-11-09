// Spike: VideoToolbox long-term references (LTR) as loss recovery.
// Encodes synthetic 1206x2622 frames with LTR on, acks LTR tokens as a
// viewer would, "loses" a run of frames, forces an LTR refresh, and decodes
// what a viewer would receive (skipping the lost frames) with
// VTDecompressionSession, reporting decode errors and frame sizes.
//   clang -fobjc-arc -framework Foundation -framework VideoToolbox -framework CoreMedia -framework CoreVideo spikes/ltr/main.m -o /tmp/ltr && /tmp/ltr
#import <CoreMedia/CoreMedia.h>
#import <Foundation/Foundation.h>
#import <VideoToolbox/VideoToolbox.h>

typedef struct {
  int n;
  BOOL key, refresh;
  NSNumber *token;
  CMSampleBufferRef sample;
} Out;

static NSMutableArray<NSValue *> *outs;
static NSMutableArray *samples;
static int W = 1206, H = 2622;

static void encoded(void *ref, void *frameRef, OSStatus st, VTEncodeInfoFlags f, CMSampleBufferRef s) {
  if (st || !s) {
    printf("frame %d dropped (%d)\n", (int)(intptr_t)frameRef, (int)st);
    return;
  }
  NSMutableDictionary *d = [NSMutableDictionary dictionary];
  d[@"n"] = @((int)(intptr_t)frameRef);
  CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(s, false);
  BOOL key = YES;
  if (atts && CFArrayGetCount(atts)) {
    CFDictionaryRef a = CFArrayGetValueAtIndex(atts, 0);
    key = !CFDictionaryContainsKey(a, kCMSampleAttachmentKey_NotSync);
    id tok = (__bridge id)CFDictionaryGetValue(a, kVTSampleAttachmentKey_RequireLTRAcknowledgementToken);
    if (tok) d[@"token"] = tok;
  }
  d[@"key"] = @(key);
  d[@"bytes"] = @(CMSampleBufferGetTotalSampleSize(s));
  d[@"sample"] = (__bridge id)s;
  @synchronized(samples) { [samples addObject:d]; }
}

static CVPixelBufferRef frame(int i) {
  CVPixelBufferRef pb;
  CVPixelBufferCreate(NULL, W, H, kCVPixelFormatType_32BGRA, (__bridge CFDictionaryRef) @{(id)kCVPixelBufferIOSurfacePropertiesKey : @{}}, &pb);
  CVPixelBufferLockBaseAddress(pb, 0);
  uint8_t *p = CVPixelBufferGetBaseAddress(pb);
  size_t bpr = CVPixelBufferGetBytesPerRow(pb);
  for (int y = 0; y < H; y++) {
    for (int x = 0; x < W; x++) {
      uint8_t *px = p + y * bpr + x * 4;
      px[0] = (uint8_t)(x + i * 3);
      px[1] = (uint8_t)(y / 4);
      px[2] = ((x / 64 + y / 64 + i / 4) & 1) ? 230 : 30;  // moving checkerboard
      px[3] = 255;
    }
  }
  // a box that moves, like scrolling content
  for (int y = 400 + i * 8 % 1600; y < 600 + i * 8 % 1600; y++)
    for (int x = 200; x < 800; x++) memset(p + y * bpr + x * 4, 255, 4);
  CVPixelBufferUnlockBaseAddress(pb, 0);
  return pb;
}

// How close a decoded frame is to its source (BGR, dB).
static double PSNR(CVImageBufferRef img, int i) {
  CVPixelBufferRef src = frame(i);
  CVPixelBufferLockBaseAddress(img, kCVPixelBufferLock_ReadOnly);
  CVPixelBufferLockBaseAddress(src, kCVPixelBufferLock_ReadOnly);
  const uint8_t *a = CVPixelBufferGetBaseAddress(img), *b = CVPixelBufferGetBaseAddress(src);
  size_t ra = CVPixelBufferGetBytesPerRow(img), rb = CVPixelBufferGetBytesPerRow(src);
  double se = 0;
  long cnt = 0;
  for (int y = 0; y < H; y += 2)
    for (int x = 0; x < W; x += 2)
      for (int c = 0; c < 3; c++) {
        double d = (double)a[y * ra + x * 4 + c] - b[y * rb + x * 4 + c];
        se += d * d;
        cnt++;
      }
  CVPixelBufferUnlockBaseAddress(img, kCVPixelBufferLock_ReadOnly);
  CVPixelBufferUnlockBaseAddress(src, kCVPixelBufferLock_ReadOnly);
  CVPixelBufferRelease(src);
  double mse = se / cnt;
  return mse <= 0 ? 99 : 10 * log10(255.0 * 255.0 / mse);
}

int main(int argc, char **argv) {
  @autoreleasepool {
    // mode: ltr (refresh at 50), key (keyframe at 50), none (no recovery)
    const char *mode = argc > 1 ? argv[1] : "ltr";
    BOOL ltr = strcmp(mode, "ltr") == 0, key = strcmp(mode, "key") == 0;
    samples = [NSMutableArray array];
    VTCompressionSessionRef s;
    NSDictionary *spec = @{(id)kVTVideoEncoderSpecification_EnableLowLatencyRateControl : @YES};
    OSStatus st = VTCompressionSessionCreate(NULL, W, H, kCMVideoCodecType_H264, (__bridge CFDictionaryRef)spec, NULL, NULL, encoded, NULL, &s);
    if (st) { printf("create %d\n", (int)st); return 1; }
    VTSessionSetProperty(s, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
    VTSessionSetProperty(s, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
    VTSessionSetProperty(s, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_ConstrainedHigh_AutoLevel);
    VTSessionSetProperty(s, kVTCompressionPropertyKey_AverageBitRate, (__bridge CFNumberRef) @12000000);
    VTSessionSetProperty(s, kVTCompressionPropertyKey_ExpectedFrameRate, (__bridge CFNumberRef) @60);
    VTSessionSetProperty(s, kVTCompressionPropertyKey_MaxKeyFrameInterval, (__bridge CFNumberRef) @(INT32_MAX));
    if (ltr) {
      st = VTSessionSetProperty(s, kVTCompressionPropertyKey_EnableLTR, kCFBooleanTrue);
      printf("EnableLTR: %d\n", (int)st);
    }
    VTCompressionSessionPrepareToEncodeFrames(s);

    // Viewer model: frames 40..49 are lost. Tokens are acked when the frame
    // is "received" (the next encode call); a refresh is forced at 50.
    NSMutableSet *lost = [NSMutableSet set];
    for (int i = 40; i < 50; i++) [lost addObject:@(i)];
    NSMutableArray *acked = [NSMutableArray array];
    NSUInteger seen = 0;
    for (int i = 0; i < 90; i++) {
      VTCompressionSessionCompleteFrames(s, kCMTimeInvalid);
      @synchronized(samples) {
        for (; seen < samples.count; seen++) {
          NSDictionary *d = samples[seen];
          if (d[@"token"] && ![lost containsObject:d[@"n"]]) [acked addObject:d[@"token"]];
        }
      }
      NSMutableDictionary *opt = [NSMutableDictionary dictionary];
      if (i == 0) opt[(id)kVTEncodeFrameOptionKey_ForceKeyFrame] = @YES;
      if (ltr && acked.count) {
        opt[(id)kVTEncodeFrameOptionKey_AcknowledgedLTRTokens] = [acked copy];
        [acked removeAllObjects];
      }
      if (i == 50) {
        if (ltr) opt[(id)kVTEncodeFrameOptionKey_ForceLTRRefresh] = @YES;
        else if (key) opt[(id)kVTEncodeFrameOptionKey_ForceKeyFrame] = @YES;
      }
      CVPixelBufferRef pb = frame(i);
      VTCompressionSessionEncodeFrame(s, pb, CMTimeMake(i, 60), kCMTimeInvalid, (__bridge CFDictionaryRef)opt, (void *)(intptr_t)i, NULL);
      CVPixelBufferRelease(pb);
    }
    VTCompressionSessionCompleteFrames(s, kCMTimeInvalid);

    // Sizes.
    for (NSDictionary *d in samples) {
      int n = [d[@"n"] intValue];
      if (n < 2 || [d[@"key"] boolValue] || (n >= 38 && n <= 54))
        printf("frame %2d %6d bytes%s%s%s\n", n, [d[@"bytes"] intValue], [d[@"key"] boolValue] ? " KEY" : "",
               d[@"token"] ? [[NSString stringWithFormat:@" LTR token %@", d[@"token"]] UTF8String] : "", [lost containsObject:d[@"n"]] ? " (lost)" : "");
    }

    // Decode what the viewer got.
    CMVideoFormatDescriptionRef fmt = CMSampleBufferGetFormatDescription((__bridge CMSampleBufferRef)samples[0][@"sample"]);
    VTDecompressionSessionRef dec;
    st = VTDecompressionSessionCreate(NULL, fmt, NULL, (__bridge CFDictionaryRef) @{(id)kCVPixelBufferPixelFormatTypeKey : @(kCVPixelFormatType_32BGRA)}, NULL, &dec);
    if (st) { printf("decoder %d\n", (int)st); return 1; }
    int ok = 0, bad = 0;
    for (NSDictionary *d in samples) {
      if ([lost containsObject:d[@"n"]]) continue;
      __block OSStatus got = 0;
      __block double psnr = 0;
      OSStatus e = VTDecompressionSessionDecodeFrameWithOutputHandler(dec, (__bridge CMSampleBufferRef)d[@"sample"], 0, NULL,
          ^(OSStatus status, VTDecodeInfoFlags f, CVImageBufferRef img, CMTime pts, CMTime dur) {
            got = status ? status : (img ? 0 : -1);
            if (img) psnr = PSNR(img, [d[@"n"] intValue]);
          });
      VTDecompressionSessionWaitForAsynchronousFrames(dec);
      if (e || got) {
        bad++;
        printf("decode frame %d: %d / %d\n", [d[@"n"] intValue], (int)e, (int)got);
      } else ok++;
      int n = [d[@"n"] intValue];
      if (n >= 36 && n <= 60 || n % 10 == 0) printf("  shown %2d  PSNR %5.1f dB\n", n, psnr);
    }
    printf("%s: decoded %d, errors %d\n", mode, ok, bad);
  }
  return 0;
}
