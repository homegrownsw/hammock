// Hammock Screen: the process that shares this Mac's displays (ADR-0014).
//
// One per daemon. It captures a display with ScreenCaptureKit while someone
// watches it, encodes it for each viewer (helpers/media/encoder.m), and posts
// their input as the Mac's own events (CGEventPost). It needs Screen
// Recording for the picture and Accessibility for input; it asks for neither
// on its own, only when told to (request).
//
// Who it serves: only the daemon that started it. Its socket's peer can't
// say who that is (a process of yours can connect, then exec a suspended
// hmkd in its own place, and the peer then looks like hmkd:
// helpers/common/rendezvous.h), so:
//   - it's started with --rendezvous=NAME, a Mach service of the daemon's,
//     and asks it for a secret, believing the answer only if the message's
//     audit token (its sender as it sent it) is hmkd, signed by this
//     helper's own team with the hardened runtime (ad hoc: the hmkd of its
//     own build), and only for the socket it was told to serve. Without
//     one it doesn't start: started by anything else, it serves no one.
//   - each connection's hello carries a fresh challenge, and nothing is
//     served until the client answers it: auth {pid, mac}, the HMAC-SHA256
//     under the secret of the challenge and both ends' pids (the daemon's,
//     which must be this socket's peer, and this host's own), so the secret
//     never crosses the socket and a relay can't pass the daemon's answer
//     on. The daemon's end, and why: internal/daemon/screenauth.go. A
//     daemon that can't answer (a restarted one, with a new secret) hears
//     hello {refused}, and replaces this host with its own.
//   - and, from before that, the peer's code signature (helpers/common/
//     peer.h): hmkd's, checked as it connects.
//
// Protocol: newline-delimited JSON over a Unix socket, any number of
// clients. A display is its CGDirectDisplayID, as a string where an "id" is
// asked for.
//   client -> host, first: auth {pid, mac} (above), then:
//                   displays {seq}, request {seq, what: screen|input},
//                   input {display, op, args} (ScreenInput's ops: mouse,
//                   wheel, key, text), quit,
//                   media (this connection carries video from now on)
//   on a media connection: encode {id, sid, bitrate, fps, maxPixels, codec
//                   (hevc444: the sharp tier; else low-latency H.264)},
//                   codec {sid, codec} (a tier change),
//                   rate {sid, bitrate, fps}, keyframe {sid}, refresh {sid},
//                   ltr {sid, tokens}, hold {sid, on}, stop {sid}: as for the
//                   simulator host (helpers/simulator/host.m)
//   host -> client: hello {pid, built, challenge (base64)}, authed (or
//                   hello {pid, refused}, and it hangs up), displaysResult {seq, displays: [{id,
//                   name, w, h (points), scale, main}], screen (Screen
//                   Recording allowed), input (Accessibility allowed)},
//                   requestResult {seq, screen, input}
//   on a media connection, after one line {"t":"mediaReady"}: binary
//   records as HammockMediaRecord writes them.
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <CoreMedia/CoreMedia.h>
#import <QuartzCore/QuartzCore.h>
#import <IOKit/pwr_mgt/IOPMLib.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreImage/CoreImage.h>
#import <ImageIO/ImageIO.h>

#import "../media/encoder.h"
#include "../common/peer.h"
#include "../common/rendezvous.h"
#import <CommonCrypto/CommonHMAC.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>

@class Session;

@interface Conn : NSObject
@property int fd;
@property(strong) dispatch_source_t src, writeSrc;
@property(strong) NSMutableData *buf, *out;
@property BOOL media;
@property BOOL authed;               // it answered its challenge
@property(strong) NSData *challenge;  // what it must answer (one try)
@end
@implementation Conn
@end

// A display someone watches: one capture, shared by its viewers.
@interface Capture : NSObject <SCStreamOutput, SCStreamDelegate>
@property CGDirectDisplayID display;
@property(strong) SCStream *stream;
@property(strong) NSMutableArray<Session *> *sessions;
@property(nonatomic) CVPixelBufferRef latest;  // the newest complete frame (retained)
@property BOOL starting;
@property int startTries;  // the display waking up: not capturable yet
@property int capW, capH;  // what the stream captures at
@end

// The screen as a grid, for crisp frames: when each cell last changed, and
// whether it has since the last crisp frame (it's soft).
enum { kGridW = 32, kGridH = 18, kCells = kGridW * kGridH };

// One viewer's video of a display.
@interface Session : NSObject {
 @public
  double changedAt[kCells];
  uint8_t soft[kCells];
}
@property(copy) NSString *sid;
@property(weak) Capture *capture;
@property(weak) Conn *conn;
// The encoder is used only on queue (each session's own, so scaling and
// encoding for one viewer never waits on another's, nor holds up main),
// with at most one frame waiting there, the newest; main keeps what it
// needs to know of it: fps, tier, and the size it encodes at.
@property(strong) HammockEncoder *encoder;
@property(strong) dispatch_queue_t queue;
@property BOOL busy;
@property(nonatomic) CVPixelBufferRef waiting;  // the next frame, while busy (retained)
@property int fps;
@property BOOL hevc;
@property int encW, encH;
@property int maxPixels;
@property CGRect view;  // the part of the display the viewer last said it shows (fractions; zero: all)
@property CFTimeInterval last;
@property BOOL scheduled, held, dirty, lost;
// Crisp frames (the sharp tier): when one was last sent, and whether a
// check for the next is waiting.
@property CFTimeInterval lastCrisp;
// What it did in the last 5 s (logged): frames captured, frames encoded,
// their bytes, and how long encoding took (submitted → out).
@property int statIn, statOut, statKeys;
@property long statBytes;
@property double statLatSum, statLatMax, statSendSum;
@property CFTimeInterval statSince;
@property BOOL crispCheck;
@end
@implementation Session
- (void)setWaiting:(CVPixelBufferRef)pb {
  if (pb) CVPixelBufferRetain(pb);
  if (_waiting) CVPixelBufferRelease(_waiting);
  _waiting = pb;
}
- (void)dealloc {
  if (_waiting) CVPixelBufferRelease(_waiting);
}
// Runs b with the encoder, on its queue.
- (void)use:(void (^)(HammockEncoder *e))b {
  HammockEncoder *e = self.encoder;
  if (e) dispatch_async(self.queue, ^{ b(e); });
}
@end

@interface Host : NSObject
@property(copy) NSString *socketPath;
@property(strong) NSData *secret;  // from the daemon that started it (main)
@property(strong) NSMutableArray<Conn *> *conns;
@property(strong) NSMutableDictionary<NSNumber *, Capture *> *captures;
// Each display's last snapshot (a JPEG, and when): what a sleeping one showed.
@property(strong) NSMutableDictionary<NSNumber *, NSDictionary *> *snapshots;
@property(strong) dispatch_source_t acceptSrc;
@property(strong) dispatch_queue_t frames;
@property NSTimeInterval lonelySince;
@property time_t built;
@property CGEventSourceRef source;
@property int clicks;                 // the click count of the press going on
@property(strong) NSCountedSet *inputs;  // input posted lately, by kind (logged)
@property CFTimeInterval inputsSince;
@property CGMouseButton pressedButton;
@property CGPoint lastPoint;  // where the pointer was last put (drags' deltas)
// The pointer, sent on its own while anyone watches: a timer, the shape
// it last had (by id) and the shapes already sent, and what was last sent
// for each display.
@property(strong) NSTimer *cursorTimer;
@property uint32_t cursorShape;
@property int cursorTick;
@property(strong) NSMutableSet<NSNumber *> *sentShapes;
@property(strong) NSMutableDictionary<NSNumber *, NSString *> *sentCursor;
// The lock screen: whether it's up, and the display kept awake while
// anyone watches.
@property BOOL locked;
@property IOPMAssertionID awake;
@property BOOL pressed;
- (void)frameFor:(Session *)s;
- (void)send:(NSDictionary *)msg to:(Conn *)c;
- (void)changed:(NSArray<NSValue *> *)rects on:(Session *)s;
- (void)encode:(Session *)s;
@end

static Host *gHost;

#pragma mark Displays

static NSString *displayName(CGDirectDisplayID d) {
  for (NSScreen *s in NSScreen.screens) {
    if ([s.deviceDescription[@"NSScreenNumber"] unsignedIntValue] == d) return s.localizedName;
  }
  return CGDisplayIsBuiltin(d) ? @"Built-in Display" : @"Display";
}

static NSArray *displayList(void) {
  // Online, not active: a display asleep (the Mac locked and idle) isn't
  // active, and watching it wakes it (keepAwake).
  CGDirectDisplayID ids[16];
  uint32_t n = 0;
  if (CGGetOnlineDisplayList(16, ids, &n) != kCGErrorSuccess) return @[];
  NSMutableArray *out = [NSMutableArray array];
  for (uint32_t i = 0; i < n; i++) {
    if (!CGDisplayIsActive(ids[i]) && !CGDisplayIsAsleep(ids[i])) continue;  // off (a closed lid's)
    if (CGDisplayMirrorsDisplay(ids[i]) != kCGNullDirectDisplay) continue;  // a mirror shows the same thing
    CGRect b = CGDisplayBounds(ids[i]);
    CGDisplayModeRef mode = CGDisplayCopyDisplayMode(ids[i]);
    double scale = mode && b.size.width > 0 ? CGDisplayModeGetPixelWidth(mode) / b.size.width : 1;
    if (mode) CGDisplayModeRelease(mode);
    [out addObject:@{@"id" : @(ids[i]), @"name" : displayName(ids[i]), @"w" : @((int)b.size.width), @"h" : @((int)b.size.height),
                     @"scale" : @(scale), @"main" : CGDisplayIsMain(ids[i]) ? @YES : @NO}];
  }
  return out;
}

// The display's size in pixels.
static void displayPixels(CGDirectDisplayID d, int *w, int *h) {
  CGDisplayModeRef mode = CGDisplayCopyDisplayMode(d);
  if (mode) {
    *w = (int)CGDisplayModeGetPixelWidth(mode);
    *h = (int)CGDisplayModeGetPixelHeight(mode);
    CGDisplayModeRelease(mode);
  } else {
    CGRect b = CGDisplayBounds(d);
    *w = (int)b.size.width;
    *h = (int)b.size.height;
  }
}

#pragma mark Capture

@implementation Capture

- (void)dealloc {
  if (_latest) CVPixelBufferRelease(_latest);
}

- (void)setLatest:(CVPixelBufferRef)pb {
  if (pb) CVPixelBufferRetain(pb);
  if (_latest) CVPixelBufferRelease(_latest);
  _latest = pb;
}

// The size to capture at: the display's own pixels, always, so a viewer
// zoomed in (a view) gets its detail at full resolution; each encoder
// crops and scales to its own size from there.
- (void)wantedSize:(int *)w height:(int *)h {
  displayPixels(self.display, w, h);
}

- (SCStreamConfiguration *)configuration {
  int w, h;
  [self wantedSize:&w height:&h];
  self.capW = w;
  self.capH = h;
  SCStreamConfiguration *c = [SCStreamConfiguration new];
  c.width = w;
  c.height = h;
  c.minimumFrameInterval = CMTimeMake(1, 60);
  c.pixelFormat = kCVPixelFormatType_32BGRA;
  c.queueDepth = 5;
  c.showsCursor = NO;  // the pointer goes on its own (cursor), drawn by the viewer at once
  c.colorSpaceName = kCGColorSpaceSRGB;
  return c;
}

- (void)start {
  if (self.stream || self.starting) return;
  self.starting = YES;
  [SCShareableContent getShareableContentExcludingDesktopWindows:NO
                                             onScreenWindowsOnly:YES
                                               completionHandler:^(SCShareableContent *content, NSError *error) {
    dispatch_async(dispatch_get_main_queue(), ^{
      self.starting = NO;
      if (!self.sessions.count) return;  // everyone left meanwhile
      SCDisplay *display = nil;
      for (SCDisplay *d in content.displays) {
        if (d.displayID == self.display) display = d;
      }
      if (!display && self.startTries < 20) {
        // Asleep a moment ago (keepAwake's waking it): again shortly.
        self.startTries++;
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC / 2), dispatch_get_main_queue(), ^{ [self start]; });
        return;
      }
      self.startTries = 0;
      if (!display) {
        fprintf(stderr, "screen-host: display %u not capturable: %s\n", self.display, error.localizedDescription.UTF8String ?: "not found");
        return;
      }
      SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
      self.stream = [[SCStream alloc] initWithFilter:filter configuration:[self configuration] delegate:self];
      NSError *err = nil;
      if (![self.stream addStreamOutput:self type:SCStreamOutputTypeScreen sampleHandlerQueue:gHost.frames error:&err]) {
        fprintf(stderr, "screen-host: addStreamOutput: %s\n", err.localizedDescription.UTF8String);
        self.stream = nil;
        return;
      }
      [self.stream startCaptureWithCompletionHandler:^(NSError *e) {
        if (e) fprintf(stderr, "screen-host: startCapture: %s\n", e.localizedDescription.UTF8String);
      }];
    });
  }];
}

- (void)stop {
  [self.stream stopCaptureWithCompletionHandler:nil];
  self.stream = nil;
  self.latest = NULL;
}

// Viewers changed: capture at the size they need now.
- (void)resize {
  if (!self.stream) return;
  int w, h;
  [self wantedSize:&w height:&h];
  if (w == self.capW && h == self.capH) return;
  [self.stream updateConfiguration:[self configuration] completionHandler:nil];
}

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sample ofType:(SCStreamOutputType)type {
  if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(sample)) return;
  // Only complete frames carry new pixels; idle ones say nothing changed,
  // so a still screen costs no encoding.
  CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sample, false);
  if (!atts || CFArrayGetCount(atts) == 0) return;
  NSDictionary *a = (__bridge NSDictionary *)CFArrayGetValueAtIndex(atts, 0);
  if ([a[SCStreamFrameInfoStatus] integerValue] != SCFrameStatusComplete) return;
  CVPixelBufferRef pb = CMSampleBufferGetImageBuffer(sample);
  if (!pb) return;
  // What changed (its dirty rects, in the frame's pixels), as fractions of
  // the frame; all of it when it doesn't say.
  NSArray *dirty = a[SCStreamFrameInfoDirtyRects];
  double fw = CVPixelBufferGetWidth(pb), fh = CVPixelBufferGetHeight(pb);
  NSMutableArray<NSValue *> *rects = [NSMutableArray array];
  if ([dirty isKindOfClass:NSArray.class] && fw > 0 && fh > 0) {
    for (id r in dirty) {
      CGRect rect;
      if (CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)r, &rect)) {
        [rects addObject:[NSValue valueWithRect:NSMakeRect(rect.origin.x / fw, rect.origin.y / fh, rect.size.width / fw, rect.size.height / fh)]];
      }
    }
  } else {
    [rects addObject:[NSValue valueWithRect:NSMakeRect(0, 0, 1, 1)]];
  }
  CVPixelBufferRetain(pb);
  dispatch_async(dispatch_get_main_queue(), ^{
    self.latest = pb;
    CVPixelBufferRelease(pb);
    for (Session *s in self.sessions) {
      [gHost changed:rects on:s];
      [gHost frameFor:s];
    }
  });
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
  fprintf(stderr, "screen-host: capture stopped: %s\n", error.localizedDescription.UTF8String);
  dispatch_async(dispatch_get_main_queue(), ^{
    if (self.stream != stream) return;
    self.stream = nil;
    // Try again shortly (a display reconfigured, the session locked).
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{
      if (self.sessions.count) [self start];
    });
  });
}

@end

#pragma mark Input

static CGEventFlags flagsFor(NSArray *mods) {
  CGEventFlags f = 0;
  for (NSString *m in mods) {
    if (![m isKindOfClass:NSString.class]) continue;
    if ([m isEqualToString:@"shift"]) f |= kCGEventFlagMaskShift;
    else if ([m isEqualToString:@"ctrl"]) f |= kCGEventFlagMaskControl;
    else if ([m isEqualToString:@"alt"]) f |= kCGEventFlagMaskAlternate;
    else if ([m isEqualToString:@"meta"]) f |= kCGEventFlagMaskCommand;
    else if ([m isEqualToString:@"fn"]) f |= kCGEventFlagMaskSecondaryFn;
  }
  return f;
}

// A position given as fractions of the display, in global coordinates.
static CGPoint pointOn(CGDirectDisplayID d, NSDictionary *a) {
  CGRect b = CGDisplayBounds(d);
  double x = MIN(MAX([a[@"x"] doubleValue], 0), 1), y = MIN(MAX([a[@"y"] doubleValue], 0), 1);
  return CGPointMake(b.origin.x + x * (b.size.width - 1), b.origin.y + y * (b.size.height - 1));
}

@implementation Host (Input)

// Counts input by kind, logged every 5 s while it comes (when a drag
// doesn't drag: did the down, the drags and the up all arrive?).
- (void)noteInput:(NSString *)kind {
  if (!self.inputs) self.inputs = [NSCountedSet set];
  [self.inputs addObject:kind ?: @"?"];
  CFTimeInterval now = CACurrentMediaTime();
  if (!self.inputsSince) self.inputsSince = now;
  if (now - self.inputsSince < 5) return;
  NSMutableArray *parts = [NSMutableArray array];
  for (NSString *k in self.inputs) [parts addObject:[NSString stringWithFormat:@"%@ %lu", k, (unsigned long)[self.inputs countForObject:k]]];
  fprintf(stderr, "screen-host: input in 5 s: %s (Accessibility %s)\n", [parts componentsJoinedByString:@", "].UTF8String, AXIsProcessTrusted() ? "yes" : "no");
  [self.inputs removeAllObjects];
  self.inputsSince = now;
}

- (void)post:(CGEventRef)e {
  if (!e) return;
  CGEventPost(kCGHIDEventTap, e);
  CFRelease(e);
}

- (void)input:(CGDirectDisplayID)d op:(NSString *)op args:(NSDictionary *)a {
  if (![a isKindOfClass:NSDictionary.class]) a = @{};
  CGEventFlags flags = flagsFor(a[@"mods"]);
  if ([op isEqualToString:@"mouse"]) {
    NSString *type = a[@"type"], *button = a[@"button"] ?: @"left";
    CGMouseButton b = [button isEqualToString:@"right"] ? kCGMouseButtonRight : [button isEqualToString:@"other"] ? kCGMouseButtonCenter : kCGMouseButtonLeft;
    CGPoint p = pointOn(d, a);
    CGEventType t;
    if ([type isEqualToString:@"down"]) {
      t = b == kCGMouseButtonLeft ? kCGEventLeftMouseDown : b == kCGMouseButtonRight ? kCGEventRightMouseDown : kCGEventOtherMouseDown;
      self.pressed = YES;
      self.pressedButton = b;
      self.clicks = MAX([a[@"clicks"] intValue], 1);
    } else if ([type isEqualToString:@"up"]) {
      t = b == kCGMouseButtonLeft ? kCGEventLeftMouseUp : b == kCGMouseButtonRight ? kCGEventRightMouseUp : kCGEventOtherMouseUp;
      self.pressed = NO;
      if ([a[@"clicks"] intValue] > 0) self.clicks = [a[@"clicks"] intValue];
    } else if ([a[@"pressed"] boolValue] || self.pressed) {
      b = self.pressedButton;
      t = b == kCGMouseButtonLeft ? kCGEventLeftMouseDragged : b == kCGMouseButtonRight ? kCGEventRightMouseDragged : kCGEventOtherMouseDragged;
    } else {
      t = kCGEventMouseMoved;
    }
    CGEventRef e = CGEventCreateMouseEvent(self.source, t, p, b);
    if (t != kCGEventMouseMoved) CGEventSetIntegerValueField(e, kCGMouseEventClickState, self.clicks ?: 1);
    // How far it moved: the window server moves a window dragged by its
    // title bar by these, and a made-up event has none of its own (so a
    // drag dragged nothing).
    CGEventSetIntegerValueField(e, kCGMouseEventDeltaX, (int64_t)lround(p.x - self.lastPoint.x));
    CGEventSetIntegerValueField(e, kCGMouseEventDeltaY, (int64_t)lround(p.y - self.lastPoint.y));
    self.lastPoint = p;
    [self noteInput:t == kCGEventMouseMoved ? @"move" : (t == kCGEventLeftMouseDragged || t == kCGEventRightMouseDragged || t == kCGEventOtherMouseDragged) ? @"drag" : type];
    CGEventSetFlags(e, flags);
    [self post:e];
  } else if ([op isEqualToString:@"wheel"]) {
    CGEventRef move = CGEventCreateMouseEvent(self.source, kCGEventMouseMoved, pointOn(d, a), kCGMouseButtonLeft);
    if (!self.pressed) self.lastPoint = pointOn(d, a);
    if (!self.pressed) [self post:move];
    else CFRelease(move);
    int32_t dy = (int32_t)lround([a[@"dy"] doubleValue]), dx = (int32_t)lround([a[@"dx"] doubleValue]);
    CGEventRef e = CGEventCreateScrollWheelEvent2(self.source, kCGScrollEventUnitPixel, 2, dy, dx, 0);
    NSString *phase = a[@"phase"] ?: @"";
    // Trackpad-style phases, so scrolling has the Mac's own feel (and apps
    // that look at phases, like maps, behave).
    if ([phase isEqualToString:@"began"]) CGEventSetIntegerValueField(e, kCGScrollWheelEventScrollPhase, kCGScrollPhaseBegan);
    else if ([phase isEqualToString:@"changed"]) CGEventSetIntegerValueField(e, kCGScrollWheelEventScrollPhase, kCGScrollPhaseChanged);
    else if ([phase isEqualToString:@"ended"]) CGEventSetIntegerValueField(e, kCGScrollWheelEventScrollPhase, kCGScrollPhaseEnded);
    else if ([phase isEqualToString:@"momentum"]) CGEventSetIntegerValueField(e, kCGScrollWheelEventMomentumPhase, kCGMomentumScrollPhaseContinue);
    CGEventSetIntegerValueField(e, kCGScrollWheelEventIsContinuous, 1);
    CGEventSetFlags(e, flags);
    [self post:e];
  } else if ([op isEqualToString:@"key"]) {
    CGKeyCode vk = (CGKeyCode)[a[@"vk"] unsignedIntValue];
    CGEventRef e = CGEventCreateKeyboardEvent(self.source, vk, ![a[@"type"] isEqualToString:@"up"]);
    CGEventSetFlags(e, flags);
    [self post:e];
  } else if ([op isEqualToString:@"text"]) {
    NSString *text = [a[@"text"] isKindOfClass:NSString.class] ? a[@"text"] : @"";
    // In chunks: an event carries at most 20 UTF-16 units.
    // At most 20 UTF-16 units an event (what one carries), whole
    // characters where they fit; a longer one (a letter under a pile of
    // combining marks) goes in pieces.
    enum { kChunk = 20 };
    unichar buf[kChunk];
    for (NSUInteger i = 0; i < text.length;) {
      NSRange r = [text rangeOfComposedCharacterSequencesForRange:NSMakeRange(i, MIN(16, text.length - i))];
      if (r.length > kChunk) r = NSMakeRange(i, MIN((NSUInteger)kChunk, text.length - i));
      [text getCharacters:buf range:r];
      for (int down = 1; down >= 0; down--) {
        CGEventRef e = CGEventCreateKeyboardEvent(self.source, 0, down);
        CGEventKeyboardSetUnicodeString(e, r.length, buf);
        [self post:e];
      }
      i = NSMaxRange(r);
    }
  }
}

@end

#pragma mark Pointer

static uint32_t fnv(const uint8_t *p, size_t n) {
  uint32_t h = 2166136261u;
  for (size_t i = 0; i < n; i++) h = (h ^ p[i]) * 16777619u;
  return h ? h : 1;
}

@implementation Host (Pointer)

- (void)sendControl:(NSDictionary *)m {
  for (Conn *c in self.conns.copy) {
    if (!c.media) [self send:m to:c];
  }
}

// Watched displays, sampled 60 times a second: where the pointer is on
// each (sent when it changes) and its shape (looked at 10 times a second;
// an image the first time a shape's seen).
- (void)startCursor {
  if (self.cursorTimer) return;
  if (!self.sentShapes) self.sentShapes = [NSMutableSet set];
  if (!self.sentCursor) self.sentCursor = [NSMutableDictionary dictionary];
  self.cursorTimer = [NSTimer scheduledTimerWithTimeInterval:1.0 / 60 repeats:YES block:^(NSTimer *t) { [self cursorTickNow]; }];
  self.cursorTimer.tolerance = 0.002;
}

- (void)stopCursor {
  [self.cursorTimer invalidate];
  self.cursorTimer = nil;
  [self.sentCursor removeAllObjects];
}

- (void)cursorTickNow {
  if (!self.captures.count) {
    [self stopCursor];
    return;
  }
  if (self.cursorTick++ % 6 == 0) [self lookAtShape];
  CGEventRef e = CGEventCreate(NULL);
  CGPoint p = CGEventGetLocation(e);
  CFRelease(e);
  for (Capture *cap in self.captures.allValues) {
    CGRect b = CGDisplayBounds(cap.display);
    BOOL on = CGRectContainsPoint(b, p);
    double x = b.size.width > 0 ? (p.x - b.origin.x) / b.size.width : 0, y = b.size.height > 0 ? (p.y - b.origin.y) / b.size.height : 0;
    NSString *key = [NSString stringWithFormat:@"%d %.5f %.5f %u", on, on ? x : 0, on ? y : 0, self.cursorShape];
    if ([self.sentCursor[@(cap.display)] isEqualToString:key]) continue;
    self.sentCursor[@(cap.display)] = key;
    [self sendControl:@{@"t" : @"cursor", @"display" : @(cap.display), @"main" : CGDisplayIsMain(cap.display) ? @YES : @NO,
                        @"x" : @(x), @"y" : @(y), @"visible" : on ? @YES : @NO, @"shape" : @(self.cursorShape)}];
  }
}

- (void)lookAtShape {
  NSCursor *c = NSCursor.currentSystemCursor;
  NSImage *img = c.image;
  if (!img) return;
  CGImageRef cg = [img CGImageForProposedRect:NULL context:nil hints:nil];
  if (!cg) return;
  NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:cg];
  NSData *png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
  if (!png.length) return;
  uint32_t id = fnv(png.bytes, png.length) ^ (uint32_t)lround(c.hotSpot.x * 31 + c.hotSpot.y * 7);
  if (!id) id = 1;
  if (![self.sentShapes containsObject:@(id)]) {
    [self.sentShapes addObject:@(id)];
    [self sendControl:@{@"t" : @"cursorShape", @"id" : @(id), @"png" : [png base64EncodedStringWithOptions:0],
                        @"hotX" : @(c.hotSpot.x), @"hotY" : @(c.hotSpot.y), @"w" : @(img.size.width), @"h" : @(img.size.height)}];
  }
  self.cursorShape = id;
}

@end

#pragma mark Lock screen

// Whether the session's screen is locked (the lock screen is up).
static BOOL screenLocked(void) {
  NSDictionary *d = CFBridgingRelease(CGSessionCopyCurrentDictionary());
  return [d[@"CGSSessionScreenIsLocked"] boolValue];
}

@implementation Host (Lock)

- (void)watchLock {
  self.locked = screenLocked();
  NSDistributedNotificationCenter *dc = NSDistributedNotificationCenter.defaultCenter;
  [dc addObserverForName:@"com.apple.screenIsLocked" object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *n) { [self lockChanged:YES]; }];
  [dc addObserverForName:@"com.apple.screenIsUnlocked" object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *n) { [self lockChanged:NO]; }];
}

- (void)lockChanged:(BOOL)locked {
  self.locked = locked;
  fprintf(stderr, "screen-host: screen %s\n", locked ? "locked" : "unlocked");
  [self sendLock];
  // The capture may have stopped at the lock (or come back blank): start
  // it again, so what's shown is what's on screen now.
  for (Capture *cap in self.captures.allValues) {
    if (!cap.stream && cap.sessions.count) [cap start];
  }
}

- (void)sendLock {
  for (Conn *c in self.conns.copy) {
    if (!c.media) [self send:@{@"t" : @"screenState", @"locked" : self.locked ? @YES : @NO} to:c];
  }
}

// Someone's watching: wake the display if it's asleep, and keep it awake
// while they do (else there's nothing to see, and no lock screen to type at).
- (void)keepAwake:(BOOL)on {
  if (on) {
    IOPMAssertionID wake = 0;
    IOPMAssertionDeclareUserActivity(CFSTR("Hammock Screen Sharing: someone's watching"), kIOPMUserActiveLocal, &wake);
    if (!self.awake) {
      IOPMAssertionID a = 0;
      if (IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleDisplaySleep, kIOPMAssertionLevelOn,
                                      CFSTR("Hammock Screen Sharing: someone's watching"), &a) == kIOReturnSuccess) self.awake = a;
    }
  } else if (self.awake) {
    IOPMAssertionRelease(self.awake);
    self.awake = 0;
  }
}

@end

#pragma mark Host

@implementation Host

- (void)listen {
  [NSFileManager.defaultManager createDirectoryAtPath:self.socketPath.stringByDeletingLastPathComponent withIntermediateDirectories:YES attributes:nil error:nil];
  // Another host already answering here (started twice: a slow first
  // launch): that one serves, this one goes, rather than taking the socket
  // and leaving it running unseen.
  {
    int probe = socket(AF_UNIX, SOCK_STREAM, 0);
    struct sockaddr_un pa = {.sun_family = AF_UNIX};
    strlcpy(pa.sun_path, self.socketPath.fileSystemRepresentation, sizeof pa.sun_path);
    BOOL live = probe >= 0 && connect(probe, (struct sockaddr *)&pa, sizeof pa) == 0;
    if (probe >= 0) close(probe);
    if (live) {
      fprintf(stderr, "screen-host: another host is already serving %s\n", pa.sun_path);
      exit(0);
    }
  }
  unlink(self.socketPath.fileSystemRepresentation);
  int fd = socket(AF_UNIX, SOCK_STREAM, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  strlcpy(a.sun_path, self.socketPath.fileSystemRepresentation, sizeof a.sun_path);
  if (bind(fd, (struct sockaddr *)&a, sizeof a) || listen(fd, 8)) {
    perror("screen-host: listen");
    exit(1);
  }
  chmod(a.sun_path, 0600);
  self.acceptSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, fd, 0, dispatch_get_main_queue());
  dispatch_source_set_event_handler(self.acceptSrc, ^{
    int c = accept(fd, NULL, NULL);
    if (c < 0) return;
    // Only hmkd, by its signature (helpers/common/peer.h): anything else
    // of yours (an agent in a pane) would have its Screen Recording and
    // Accessibility. Not the app either: it never connects here. One
    // refused is told so: a daemon this host doesn't take (one of another
    // build, signed otherwise) then replaces it with its own.
    pid_t peer = 0;
    const char *why = "";
    if (!HammockPeerAllowed(c, HammockPeerDaemon, &peer, &why)) {
      NSData *no = [NSJSONSerialization dataWithJSONObject:@{@"t" : @"hello", @"pid" : @(getpid()), @"refused" : @YES} options:0 error:nil];
      write(c, no.bytes, no.length);
      write(c, "\n", 1);
      close(c);
      fprintf(stderr, "screen-host: refused a connection from pid %d\n", peer);
      return;
    }
    fprintf(stderr, "screen-host: connection from pid %d (%s)\n", peer, why);
    int on = 1;
    setsockopt(c, SOL_SOCKET, SO_NOSIGPIPE, &on, sizeof on);
    fcntl(c, F_SETFL, fcntl(c, F_GETFL) | O_NONBLOCK);
    Conn *conn = [Conn new];
    conn.fd = c;
    conn.buf = [NSMutableData data];
    conn.out = [NSMutableData data];
    conn.src = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, c, 0, dispatch_get_main_queue());
    __weak Conn *weak = conn;
    dispatch_source_set_event_handler(conn.src, ^{ [self read:weak]; });
    dispatch_resume(conn.src);
    [self.conns addObject:conn];
    self.lonelySince = 0;
    uint8_t challenge[32];
    arc4random_buf(challenge, sizeof challenge);
    conn.challenge = [NSData dataWithBytes:challenge length:sizeof challenge];
    [self send:@{@"t" : @"hello", @"pid" : @(getpid()), @"built" : @(self.built),
                 @"challenge" : [conn.challenge base64EncodedStringWithOptions:0]}
            to:conn];
    // A new connection (the daemon again, after a restart) hears the
    // pointer's shapes and place afresh.
    [self.sentShapes removeAllObjects];
    [self.sentCursor removeAllObjects];
  });
  dispatch_resume(self.acceptSrc);
  // No clients for ten minutes (the daemon is gone for good): quit.
  self.lonelySince = NSDate.date.timeIntervalSince1970;
  [NSTimer scheduledTimerWithTimeInterval:30 repeats:YES block:^(NSTimer *timer) {
    if (self.lonelySince && NSDate.date.timeIntervalSince1970 - self.lonelySince > 600) exit(0);
  }];
}

- (void)drop:(Conn *)c {
  if (!c || ![self.conns containsObject:c]) return;
  for (Capture *cap in self.captures.allValues.copy) {
    for (Session *s in cap.sessions.copy) {
      if (s.conn == c) [self stopSession:s];
    }
  }
  dispatch_source_cancel(c.src);
  if (c.writeSrc) dispatch_source_cancel(c.writeSrc);
  close(c.fd);
  [self.conns removeObject:c];
  if (!self.conns.count) self.lonelySince = NSDate.date.timeIntervalSince1970;
}

- (void)read:(Conn *)c {
  if (!c) return;
  char buf[65536];
  ssize_t n = read(c.fd, buf, sizeof buf);
  if (n < 0 && (errno == EAGAIN || errno == EINTR)) return;
  if (n <= 0) {
    [self drop:c];
    return;
  }
  [c.buf appendBytes:buf length:n];
  const char *p = (const char *)c.buf.bytes;
  NSUInteger len = c.buf.length, start = 0;
  for (NSUInteger i = 0; i < len; i++) {
    if (p[i] != '\n') continue;
    NSData *line = [NSData dataWithBytes:p + start length:i - start];
    start = i + 1;
    NSDictionary *m = [NSJSONSerialization JSONObjectWithData:line options:0 error:nil];
    if ([m isKindOfClass:NSDictionary.class]) [self handle:m from:c];
  }
  [c.buf replaceBytesInRange:NSMakeRange(0, start) withBytes:NULL length:0];
}

- (void)send:(NSDictionary *)msg to:(Conn *)c {
  if (!c || c.media || ![self.conns containsObject:c]) return;
  [c.out appendData:[NSJSONSerialization dataWithJSONObject:msg options:0 error:nil]];
  [c.out appendBytes:"\n" length:1];
  [self flush:c];
}

- (void)flush:(Conn *)c {
  while (c.out.length) {
    ssize_t w = write(c.fd, c.out.bytes, c.out.length);
    if (w < 0 && errno == EINTR) continue;
    if (w < 0 && errno == EAGAIN) break;
    if (w <= 0) {
      [self drop:c];
      return;
    }
    [c.out replaceBytesInRange:NSMakeRange(0, w) withBytes:NULL length:0];
  }
  if (c.out.length && !c.writeSrc) {
    c.writeSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_WRITE, c.fd, 0, dispatch_get_main_queue());
    __weak Conn *weak = c;
    dispatch_source_set_event_handler(c.writeSrc, ^{ if (weak) [self flush:weak]; });
    dispatch_resume(c.writeSrc);
  } else if (!c.out.length && c.writeSrc) {
    dispatch_source_cancel(c.writeSrc);
    c.writeSrc = nil;
    // Caught up: viewers whose frames were dropped start again from a keyframe.
    for (Capture *cap in self.captures.allValues) {
      for (Session *s in cap.sessions) {
        if (s.conn != c || !s.lost || s.held) continue;
        [s use:^(HammockEncoder *e) { [e requestKeyframe]; }];
        [self encode:s];
      }
    }
  }
}

#pragma mark Sessions

- (Session *)session:(NSString *)sid {
  for (Capture *cap in self.captures.allValues) {
    for (Session *s in cap.sessions) {
      if ([s.sid isEqualToString:sid]) return s;
    }
  }
  return nil;
}

- (void)stopSession:(Session *)s {
  [s use:^(HammockEncoder *e) { [e invalidate]; }];
  s.encoder = nil;
  s.waiting = NULL;
  Capture *cap = s.capture;
  [cap.sessions removeObject:s];
  if (!cap) return;
  if (!cap.sessions.count) {
    [cap stop];
    [self.captures removeObjectForKey:@(cap.display)];
    if (!self.captures.count) [self keepAwake:NO];
  } else {
    [cap resize];
  }
}

// A new frame of the display: encode it for this viewer, at most fps times
// a second; one that comes too soon waits its turn, so the last frame of a
// burst is never lost.
- (void)frameFor:(Session *)s {
  s.dirty = YES;
  if (s.scheduled || s.held) return;
  CFTimeInterval interval = 1.0 / (s.fps ?: 60), wait = s.last + interval - CACurrentMediaTime();
  if (wait <= 0) {
    [self encode:s];
    return;
  }
  s.scheduled = YES;
  __weak Session *ws = s;
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(wait * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
    Session *s = ws;
    if (!s) return;
    s.scheduled = NO;
    if (s.dirty && !s.held) [self encode:s];
  });
}

// Sharpening what's settled: the sharp tier's encoder refines unchanged
// parts a little more with every frame it's given (measured: a still area
// went 30 → 36 dB over 18 frames at 16 Mbit/s, 34 → 44 at 40), but a still
// screen sends no frames, so what was last sent mid-change stays soft. So
// once enough of the screen changed since then (it's soft) and has been
// still for kCrispAfter, the same picture goes again kRefineFrames times, a
// few KB each. What never stops changing (a video, a spinner) never counts.
// At most once every kCrispEvery. (A keyframe at higher quality did nothing
// the cap didn't: the quality property and QP caps are ignored by it.)
static const double kCrispAfter = 0.15, kCrispEvery = 1.0, kCrispArea = 0.02;
static const int kRefineFrames = 6;

- (void)changed:(NSArray<NSValue *> *)rects on:(Session *)s {
  CFTimeInterval now = CACurrentMediaTime();
  s.statIn++;
  for (NSValue *v in rects) {
    NSRect r = v.rectValue;
    int x0 = MAX(0, (int)floor(r.origin.x * kGridW)), x1 = MIN(kGridW - 1, (int)ceil((r.origin.x + r.size.width) * kGridW) - 1);
    int y0 = MAX(0, (int)floor(r.origin.y * kGridH)), y1 = MIN(kGridH - 1, (int)ceil((r.origin.y + r.size.height) * kGridH) - 1);
    for (int y = y0; y <= y1; y++) {
      for (int x = x0; x <= x1; x++) {
        s->changedAt[y * kGridW + x] = now;
        s->soft[y * kGridW + x] = 1;
      }
    }
  }
  [self crispCheck:s];
}

// A viewer zoomed in: send the part it shows (fractions of the display).
// It's grown to the display's shape (frames keep their size) and never to
// less than the encoded size in the display's own pixels (no detail is
// gained past that), and kept on the display.
- (void)view:(Session *)s x:(double)x y:(double)y w:(double)w h:(double)h {
  CVPixelBufferRef pb = s.capture.latest;
  int ew = s.encW, eh = s.encH;
  CGRect crop = CGRectZero;
  if (w <= 0 || h <= 0 || w >= 0.999 || !pb || !ew || !eh) {
    crop = CGRectZero;
  } else {
    double fw = CVPixelBufferGetWidth(pb), fh = CVPixelBufferGetHeight(pb);
    double aspect = (fw / fh);  // the crop's width over height, in the display's terms
    double cw = w, ch = h;
    if (cw * fw / (ch * fh) < aspect) cw = ch * fh * aspect / fw; else ch = cw * fw / (aspect * fh);
    double least = MIN(1, ew / fw);  // a crop narrower than this is upscaled
    if (cw < least) {
      ch *= least / cw;
      cw = least;
    }
    cw = MIN(cw, 1);
    ch = MIN(ch, 1);
    double cx = MIN(MAX(x + w / 2 - cw / 2, 0), 1 - cw), cy = MIN(MAX(y + h / 2 - ch / 2, 0), 1 - ch);
    crop = cw >= 0.999 ? CGRectZero : CGRectMake(cx, cy, cw, ch);
  }
  [s use:^(HammockEncoder *e) { e.crop = crop; }];
  // What's shown changed: it's all soft, and goes now (then is refined).
  memset(s->soft, 1, sizeof s->soft);
  for (int i = 0; i < kCells; i++) s->changedAt[i] = CACurrentMediaTime();
  if (!s.held) [self encode:s];
  else s.dirty = YES;
  [self crispCheck:s];
}

// The same picture again, a frame's interval apart, unless the screen
// moves on meanwhile (then those frames carry on as usual).
- (void)refine:(Session *)s left:(int)n {
  if (n <= 0 || !s.capture.latest || !s.hevc) return;
  if (!s.held && !s.scheduled) [self encode:s];
  __weak Session *ws = s;
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(NSEC_PER_SEC / 30)), dispatch_get_main_queue(), ^{
    if (ws) [self refine:ws left:n - 1];
  });
}

// Refines what's settled if it's due, else checks again once it could be.
- (void)crispCheck:(Session *)s {
  if (!s.hevc || !s.capture.latest) return;
  CFTimeInterval now = CACurrentMediaTime();
  int ready = 0;
  CFTimeInterval soonest = 0;  // when a soft cell still changing becomes still enough
  for (int i = 0; i < kCells; i++) {
    if (!s->soft[i]) continue;
    CFTimeInterval still = now - s->changedAt[i];
    if (still >= kCrispAfter) ready++;
    else soonest = soonest ? MIN(soonest, kCrispAfter - still) : kCrispAfter - still;
  }
  BOOL due = ready >= kCells * kCrispArea;
  if (due && !s.held && now - s.lastCrisp >= kCrispEvery) {
    s.lastCrisp = now;
    // Everything's sharp after it (what keeps changing goes soft again).
    memset(s->soft, 0, sizeof s->soft);
    [self refine:s left:kRefineFrames];
    return;
  }
  // Not yet: look again when it could be (a still screen sends no frames).
  double wait = due ? kCrispEvery - (now - s.lastCrisp) : soonest;
  if (wait <= 0 || s.crispCheck) return;
  s.crispCheck = YES;
  __weak Session *ws = s;
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)((wait + 0.01) * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
    Session *s = ws;
    if (!s) return;
    s.crispCheck = NO;
    [self crispCheck:s];
  });
}

- (void)encode:(Session *)s {
  CVPixelBufferRef pb = s.capture.latest;
  if (!pb || !s.encoder) return;
  s.dirty = NO;
  s.last = CACurrentMediaTime();
  if (s.busy) {
    s.waiting = pb;  // goes once the one being scaled and handed over is
    return;
  }
  [self submit:pb to:s];
}

// Scales and hands a frame to the encoder on the session's queue, then the
// newest that came meanwhile.
- (void)submit:(CVPixelBufferRef)pb to:(Session *)s {
  s.busy = YES;
  CVPixelBufferRetain(pb);
  HammockEncoder *e = s.encoder;
  __weak Session *ws = s;
  dispatch_async(s.queue, ^{
    [e encodePixelBuffer:pb];
    CVPixelBufferRelease(pb);
    dispatch_async(dispatch_get_main_queue(), ^{
      Session *s = ws;
      if (!s) return;
      s.busy = NO;
      CVPixelBufferRef next = s.waiting;
      if (next && s.encoder) {
        CVPixelBufferRetain(next);
        s.waiting = NULL;
        [self submit:next to:s];
        CVPixelBufferRelease(next);
      }
    });
  });
}

- (void)sendFrame:(NSData *)avcc info:(HammockFrameInfo)info session:(Session *)s {
  Conn *c = s.conn;
  if (!c || ![self.conns containsObject:c]) return;
  BOOL sized = !s.encW;
  s.encW = info.width;
  s.encH = info.height;
  // The first frame at a (new) size: the view's crop can be worked out now.
  if (sized && !CGRectIsEmpty(s.view)) [self view:s x:s.view.origin.x y:s.view.origin.y w:s.view.size.width h:s.view.size.height];
  double nowUs = (double)(uint64_t)(CMTimeGetSeconds(CMClockGetTime(CMClockGetHostTimeClock())) * 1e6);
  double lat = (double)info.encodedMicros - (double)info.ptsMicros;  // in the encoder
  s.statSendSum += (nowUs - (double)info.encodedMicros) / 1000;    // then waiting for main
  s.statOut++;
  if (info.keyframe) s.statKeys++;
  s.statBytes += avcc.length;
  s.statLatSum += lat / 1000;
  s.statLatMax = MAX(s.statLatMax, lat / 1000);
  CFTimeInterval now = CACurrentMediaTime();
  if (!s.statSince) s.statSince = now;
  if (now - s.statSince >= 5) {
    double secs = now - s.statSince;
    fprintf(stderr, "screen-host: %s %s %dx%d: captured %.0f fps, encoded %.0f fps (%d keyframes), %.1f Mbit/s, encode %.1f ms avg %.1f max, then %.1f ms to send\n",
            s.sid.UTF8String, info.hevc ? "hevc444" : "h264", info.width, info.height, s.statIn / secs, s.statOut / secs, s.statKeys,
            s.statBytes * 8 / secs / 1e6, s.statOut ? s.statLatSum / s.statOut : 0, s.statLatMax, s.statOut ? s.statSendSum / s.statOut : 0);
    s.statIn = s.statOut = s.statKeys = 0;
    s.statBytes = 0;
    s.statLatSum = s.statLatMax = s.statSendSum = 0;
    s.statSince = now;
  }
  BOOL lost = s.lost;
  BOOL admit = HammockAdmitFrame(&lost, info.keyframe, c.out.length, nil);
  s.lost = lost;
  if (!admit) {
    if (lost && !info.keyframe) [s use:^(HammockEncoder *e) { [e requestKeyframe]; }];  // to start again from
    return;
  }
  [c.out appendData:HammockMediaRecord(s.sid, avcc, info)];
  if (c.out.length > 64 << 20) {
    [self drop:c];
    return;
  }
  [self flush:c];
}

- (void)handleMedia:(NSDictionary *)m from:(Conn *)c {
  NSString *t = m[@"t"], *sid = m[@"sid"] ?: @"";
  if ([t isEqualToString:@"encode"]) {
    NSString *idStr = [m[@"id"] description] ?: @"";
    if ([idStr hasPrefix:@"screen:"]) idStr = [idStr substringFromIndex:7];
    CGDirectDisplayID d = (CGDirectDisplayID)idStr.longLongValue;
    if (!d) d = CGMainDisplayID();
    Session *old = [self session:sid];
    if (old) [self stopSession:old];
    Capture *cap = self.captures[@(d)];
    if (!cap) {
      cap = [Capture new];
      cap.display = d;
      cap.sessions = [NSMutableArray array];
      self.captures[@(d)] = cap;
    }
    Session *s = [Session new];
    s.sid = sid;
    s.capture = cap;
    s.conn = c;
    s.maxPixels = [m[@"maxPixels"] intValue];
    __weak Session *ws = s;
    s.encoder = [[HammockEncoder alloc] initWithBitrate:[m[@"bitrate"] intValue] ?: 4000000 fps:[m[@"fps"] intValue] ?: 60
                                           maxPixels:s.maxPixels
                                              output:^(NSData *avcc, HammockFrameInfo info) {
      if (ws) [gHost sendFrame:avcc info:info session:ws];
    }];
    s.queue = dispatch_queue_create("screen.session", DISPATCH_QUEUE_SERIAL);
    s.encoder.workQueue = s.queue;
    s.fps = [m[@"fps"] intValue] ?: 60;
    s.hevc = [m[@"codec"] isEqualToString:@"hevc444"];
    s.encoder.hevc = s.hevc;
    s.encoder.lowLatencyMaxPixels = 1920 * 1080;
    [cap.sessions addObject:s];
    [self keepAwake:YES];
    [self sendLock];  // the viewer hears whether it's locked from the start
    [self startCursor];
    [self.sentCursor removeAllObjects];  // the new viewer hears where it is now
    if (cap.stream) {
      [cap resize];
      [self encode:s];  // the first frame (a keyframe) from what's on screen now
    } else {
      [cap start];
    }
    return;
  }
  Session *s = [self session:sid];
  if (!s) return;
  if ([t isEqualToString:@"codec"]) {
    // A tier change: the encoder starts again, with a keyframe now.
    BOOL hevc = [m[@"codec"] isEqualToString:@"hevc444"];
    s.hevc = hevc;
    [s use:^(HammockEncoder *e) { e.hevc = hevc; }];
    // All of it is soft in the new tier: sent crisp once it's still.
    [self changed:@[ [NSValue valueWithRect:NSMakeRect(0, 0, 1, 1)] ] on:s];
    if (!s.held) [self encode:s];
    else s.dirty = YES;
  } else if ([t isEqualToString:@"view"]) {
    s.view = CGRectMake([m[@"x"] doubleValue], [m[@"y"] doubleValue], [m[@"w"] doubleValue], [m[@"h"] doubleValue]);
    [self view:s x:s.view.origin.x y:s.view.origin.y w:s.view.size.width h:s.view.size.height];
  } else if ([t isEqualToString:@"size"]) {
    // The link's changed: frames at this size from now on (a keyframe),
    // and the view's crop worked out again for it.
    int px = [m[@"maxPixels"] intValue];
    s.maxPixels = px;
    s.encW = s.encH = 0;  // known again from its first frame at the new size
    [s use:^(HammockEncoder *e) { e.maxPixels = px; }];
    [self encode:s];  // its first frame back works the view's crop out again (sendFrame)
  } else if ([t isEqualToString:@"rate"]) {
    int bitrate = [m[@"bitrate"] intValue], fps = [m[@"fps"] intValue];
    if (fps) s.fps = fps;
    [s use:^(HammockEncoder *e) {
      if (bitrate) e.bitrate = bitrate;
      if (fps) e.fps = fps;
    }];
  } else if ([t isEqualToString:@"keyframe"] || [t isEqualToString:@"refresh"]) {
    // Encoded now even if the screen is still: the viewer needs it.
    BOOL key = [t isEqualToString:@"keyframe"];
    [s use:^(HammockEncoder *e) {
      if (key) [e requestKeyframe];
      else [e requestRefresh];
    }];
    if (!s.held) [self encode:s];
    else s.dirty = YES;
  } else if ([t isEqualToString:@"ltr"]) {
    NSArray *tokens = m[@"tokens"];
    [s use:^(HammockEncoder *e) { [e acknowledgeLTR:tokens]; }];
  } else if ([t isEqualToString:@"hold"]) {
    s.held = [m[@"on"] boolValue];
    if (!s.held && s.dirty) [self frameFor:s];
  } else if ([t isEqualToString:@"stop"]) {
    [self stopSession:s];
  }
}

#pragma mark Messages

// What macOS lets us do, as JSON true/false (a C boolean in an NSNumber
// would be 0/1). Accessibility is ours (Hammock Screen Sharing's); Screen
// Recording is recorded for the app we're bundled in (Hammock, or Hammock β),
// which CGPreflightScreenCaptureAccess doesn't see from here: once Settings
// has asked for it, ScreenCaptureKit itself says (before that, asking it
// could bring up macOS's prompt unasked).
- (void)access:(void (^)(NSDictionary *))done {
  NSNumber *input = AXIsProcessTrusted() ? @YES : @NO;
  if (CGPreflightScreenCaptureAccess() || ![NSUserDefaults.standardUserDefaults boolForKey:@"askedScreen"]) {
    done(@{@"screen" : CGPreflightScreenCaptureAccess() ? @YES : @NO, @"input" : input});
    return;
  }
  __block BOOL answered = NO;
  void (^answer)(BOOL) = ^(BOOL ok) {
    if (answered) return;
    answered = YES;
    done(@{@"screen" : ok ? @YES : @NO, @"input" : input});
  };
  [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *content, NSError *error) {
    dispatch_async(dispatch_get_main_queue(), ^{ answer(error == nil && content.displays.count > 0); });
  }];
  // Waiting on a prompt of macOS's: not yet.
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{ answer(NO); });
}

// A display as a small JPEG (Show Screen's card): from the capture going
// on if it's watched, else a screenshot; asleep, the last one taken.
static NSData *jpegOf(CGImageRef img, int maxWidth, int *w, int *h) {
  size_t iw = CGImageGetWidth(img), ih = CGImageGetHeight(img);
  if (!iw || !ih) return nil;
  double k = MIN(1.0, (double)maxWidth / iw);
  *w = (int)lround(iw * k);
  *h = (int)lround(ih * k);
  CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
  CGContextRef ctx = CGBitmapContextCreate(NULL, *w, *h, 8, 0, cs, kCGImageAlphaNoneSkipFirst | kCGBitmapByteOrder32Little);
  CGColorSpaceRelease(cs);
  if (!ctx) return nil;
  CGContextSetInterpolationQuality(ctx, kCGInterpolationHigh);
  CGContextDrawImage(ctx, CGRectMake(0, 0, *w, *h), img);
  CGImageRef small = CGBitmapContextCreateImage(ctx);
  CGContextRelease(ctx);
  NSMutableData *out = [NSMutableData data];
  CGImageDestinationRef dst = CGImageDestinationCreateWithData((__bridge CFMutableDataRef)out, CFSTR("public.jpeg"), 1, NULL);
  CGImageDestinationAddImage(dst, small, (__bridge CFDictionaryRef) @{(__bridge NSString *)kCGImageDestinationLossyCompressionQuality : @0.7});
  BOOL ok = CGImageDestinationFinalize(dst);
  CFRelease(dst);
  CGImageRelease(small);
  return ok ? out : nil;
}

- (void)snapshot:(CGDirectDisplayID)d maxWidth:(int)maxWidth done:(void (^)(NSDictionary *))done {
  maxWidth = MIN(MAX(maxWidth, 160), 2048);
  void (^last)(BOOL) = ^(BOOL asleep) {
    NSMutableDictionary *r = [self.snapshots[@(d)] mutableCopy] ?: [NSMutableDictionary dictionary];
    r[@"asleep"] = asleep ? @YES : @NO;
    done(r);
  };
  void (^keep)(CGImageRef) = ^(CGImageRef img) {
    int w = 0, h = 0;
    NSData *j = img ? jpegOf(img, maxWidth, &w, &h) : nil;
    if (!j) return last(NO);
    NSDictionary *r = @{@"jpeg" : [j base64EncodedStringWithOptions:0], @"w" : @(w), @"h" : @(h),
                        @"taken" : @((long long)(NSDate.date.timeIntervalSince1970 * 1000))};
    if (!self.snapshots) self.snapshots = [NSMutableDictionary dictionary];
    self.snapshots[@(d)] = r;
    done(r);
  };
  if (CGDisplayIsAsleep(d)) return last(YES);  // waking it to look would be rude: what it last showed
  Capture *cap = self.captures[@(d)];
  if (cap.latest) {
    static CIContext *ci;
    if (!ci) ci = [CIContext contextWithOptions:nil];
    CIImage *im = [CIImage imageWithCVPixelBuffer:cap.latest];
    CGImageRef img = [ci createCGImage:im fromRect:im.extent];
    keep(img);
    if (img) CGImageRelease(img);
    return;
  }
  [SCShareableContent getShareableContentExcludingDesktopWindows:NO onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content, NSError *error) {
    SCDisplay *display = nil;
    for (SCDisplay *x in content.displays) if (x.displayID == d) display = x;
    if (!display) {
      dispatch_async(dispatch_get_main_queue(), ^{ last(NO); });
      return;
    }
    SCStreamConfiguration *cfg = [SCStreamConfiguration new];
    double k = MIN(1.0, (double)maxWidth / display.width);
    // At twice the card's size from points, then scaled: sharp text, small capture.
    cfg.width = (size_t)MAX(1, lround(display.width * k * 2));
    cfg.height = (size_t)MAX(1, lround(display.height * k * 2));
    cfg.showsCursor = NO;
    SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
    [SCScreenshotManager captureImageWithFilter:filter configuration:cfg completionHandler:^(CGImageRef img, NSError *e) {
      if (img) CGImageRetain(img);
      dispatch_async(dispatch_get_main_queue(), ^{
        keep(img);
        if (img) CGImageRelease(img);
      });
    }];
  }];
}

// Whether auth m answers c's challenge: HMAC-SHA256 under the secret of
// "hmk-screen-auth-v1\n", the challenge, and the daemon's pid then this
// host's (big-endian 32 bits each), the daemon's being c's peer
// (screenMAC in internal/daemon/screenauth.go). One try per connection.
- (BOOL)authOK:(NSDictionary *)m from:(Conn *)c {
  NSData *challenge = c.challenge;
  c.challenge = nil;
  NSNumber *pid = m[@"pid"];
  NSString *mac = m[@"mac"];
  if (self.secret.length != 32 || challenge.length != 32 || ![pid isKindOfClass:NSNumber.class] ||
      ![mac isKindOfClass:NSString.class])
    return NO;
  pid_t peer = 0;
  socklen_t n = sizeof peer;
  if (getsockopt(c.fd, SOL_LOCAL, LOCAL_PEERPID, &peer, &n) != 0 || peer <= 0 || peer != pid.intValue) return NO;
  NSData *got = [[NSData alloc] initWithBase64EncodedString:mac options:0];
  if (got.length != CC_SHA256_DIGEST_LENGTH) return NO;
  uint32_t pids[2] = {htonl((uint32_t)peer), htonl((uint32_t)getpid())};
  static const char label[] = "hmk-screen-auth-v1\n";
  CCHmacContext ctx;
  CCHmacInit(&ctx, kCCHmacAlgSHA256, self.secret.bytes, self.secret.length);
  CCHmacUpdate(&ctx, label, sizeof label - 1);
  CCHmacUpdate(&ctx, challenge.bytes, challenge.length);
  CCHmacUpdate(&ctx, pids, sizeof pids);
  uint8_t want[CC_SHA256_DIGEST_LENGTH];
  CCHmacFinal(&ctx, want);
  return timingsafe_bcmp(want, got.bytes, sizeof want) == 0;
}

- (void)handle:(NSDictionary *)m from:(Conn *)c {
  NSString *t = m[@"t"];
  if (!c.authed) {
    // Nothing, not even quit, before the daemon proves it's the one that
    // started this host.
    if ([t isEqualToString:@"auth"] && [self authOK:m from:c]) {
      c.authed = YES;
      [self send:@{@"t" : @"authed"} to:c];
      return;
    }
    pid_t peer = 0;
    socklen_t n = sizeof peer;
    getsockopt(c.fd, SOL_LOCAL, LOCAL_PEERPID, &peer, &n);
    fprintf(stderr, "screen-host: pid %d didn't prove it's the daemon that started this host: hung up\n", peer);
    [self send:@{@"t" : @"hello", @"pid" : @(getpid()), @"refused" : @YES} to:c];
    [self drop:c];
    return;
  }
  if ([t isEqualToString:@"quit"]) exit(0);
  if ([t isEqualToString:@"media"]) {
    [self send:@{@"t" : @"mediaReady"} to:c];
    c.media = YES;
    return;
  }
  if (c.media) {
    [self handleMedia:m from:c];
    return;
  }
  if ([t isEqualToString:@"displays"]) {
    NSMutableDictionary *r = [@{@"t" : @"displaysResult", @"seq" : m[@"seq"] ?: @0, @"displays" : displayList()} mutableCopy];
    [self access:^(NSDictionary *a) {
      [r addEntriesFromDictionary:a];
      [self send:r to:c];
    }];
  } else if ([t isEqualToString:@"request"]) {
    // Asked from Settings: macOS shows its own prompt, or its Settings pane.
    if ([m[@"what"] isEqualToString:@"screen"]) {
      [NSUserDefaults.standardUserDefaults setBool:YES forKey:@"askedScreen"];
      CGRequestScreenCaptureAccess();
    } else {
      AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef) @{(__bridge NSString *)kAXTrustedCheckOptionPrompt : @YES});
    }
    NSMutableDictionary *r = [@{@"t" : @"requestResult", @"seq" : m[@"seq"] ?: @0} mutableCopy];
    r[@"screen"] = CGPreflightScreenCaptureAccess() ? @YES : @NO;
    r[@"input"] = AXIsProcessTrusted() ? @YES : @NO;
    [self send:r to:c];
  } else if ([t isEqualToString:@"snapshot"]) {
    CGDirectDisplayID d = (CGDirectDisplayID)[m[@"display"] longLongValue];
    if (!d) d = CGMainDisplayID();
    [self snapshot:d maxWidth:[m[@"maxWidth"] intValue] ?: 960 done:^(NSDictionary *r) {
      NSMutableDictionary *out = [r mutableCopy];
      out[@"t"] = @"snapshotResult";
      out[@"seq"] = m[@"seq"] ?: @0;
      [self send:out to:c];
    }];
  } else if ([t isEqualToString:@"input"]) {
    CGDirectDisplayID d = (CGDirectDisplayID)[m[@"display"] longLongValue];
    if (!d) d = CGMainDisplayID();
    [self input:d op:m[@"op"] ?: @"" args:m[@"args"]];
  }
}

@end

#define HMK_SCREEN_HELLO "hmk-scr-hello-v1"  // 16 bytes: screenHello in internal/daemon/screenauth.go
#define HMK_SCREEN_PROTOCOL 1                // screenProtocol there

// The secret, from the daemon's rendezvous (helpers/common/rendezvous.h),
// if Hammock's daemon sent it, for this socket; else nil, and why.
static NSData *fetchSecret(const char *name, NSString *socketPath) {
  void *data = NULL;
  size_t len = 0;
  audit_token_t token;
  kern_return_t kr = hmk_rv_fetch(name, HMK_SCREEN_HELLO, 10000, &data, &len, &token);
  if (kr != KERN_SUCCESS) {
    fprintf(stderr, "screen-host: no answer from a daemon at %s (mach error %#x)\n", name, kr);
    return nil;
  }
  NSData *bytes = [NSData dataWithBytes:data length:len];
  vm_deallocate(mach_task_self(), (vm_address_t)data, len);
  // Who sent it, as it sent it: a process that has exec'd since isn't found.
  NSString *team = HammockOwnTeam(NULL);
  if (!(team ? HammockSignedAsHammock(token, team, HammockPeerDaemon) : HammockOwnProgram(token, HammockPeerDaemon))) {
    fprintf(stderr, "screen-host: the answer at %s isn't from Hammock's daemon (pid %d)\n", name, (int)token.val[5]);
    return nil;
  }
  NSDictionary *m = [NSJSONSerialization JSONObjectWithData:bytes options:0 error:nil];
  if (![m isKindOfClass:NSDictionary.class] || ![m[@"v"] isKindOfClass:NSNumber.class] ||
      [m[@"v"] intValue] != HMK_SCREEN_PROTOCOL || ![m[@"secret"] isKindOfClass:NSString.class] ||
      ![m[@"socket"] isKindOfClass:NSString.class]) {
    fprintf(stderr, "screen-host: the daemon's answer is from another build of Hammock\n");
    return nil;
  }
  if (![m[@"socket"] isEqualToString:socketPath]) {
    fprintf(stderr, "screen-host: the daemon wants a host on another socket (%s)\n", [m[@"socket"] UTF8String]);
    return nil;
  }
  NSData *secret = [[NSData alloc] initWithBase64EncodedString:m[@"secret"] options:0];
  return secret.length == 32 ? secret : nil;
}

int main(int argc, char **argv) {
  @autoreleasepool {
    NSString *socketPath = nil;
    const char *rendezvous = NULL;
    for (int i = 1; i < argc; i++) {
      if (!strncmp(argv[i], "--socket=", 9)) socketPath = @(argv[i] + 9);
      if (!strncmp(argv[i], "--rendezvous=", 13)) rendezvous = argv[i] + 13;
      // Started through Launch Services, it has no stderr of the daemon's.
      if (!strncmp(argv[i], "--log=", 6) && freopen(argv[i] + 6, "a", stderr)) setvbuf(stderr, NULL, _IONBF, 0);
    }
    if (!socketPath || !rendezvous) {
      fprintf(stderr, "usage: Hammock Screen --socket=PATH --rendezvous=NAME [--log=FILE] (hmkd starts it)\n");
      return 2;
    }
    // Only the daemon that started it: without its secret, it serves no one.
    NSData *secret = fetchSecret(rendezvous, socketPath);
    if (!secret) return 1;
    NSApplication *app = NSApplication.sharedApplication;
    app.activationPolicy = NSApplicationActivationPolicyProhibited;
    gHost = [Host new];
    gHost.socketPath = socketPath;
    gHost.secret = secret;
    gHost.conns = [NSMutableArray array];
    gHost.captures = [NSMutableDictionary dictionary];
    gHost.frames = dispatch_queue_create("screen.frames", DISPATCH_QUEUE_SERIAL);
    // Events as if from the hardware, so apps treat them as a person's.
    gHost.source = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    struct stat st;
    gHost.built = stat(argv[0], &st) == 0 ? st.st_mtime : 0;
    HammockEncoderWarmUp();  // before any HEVC session (see encoder.h)
    [gHost watchLock];
    [gHost listen];
    [app run];
  }
  return 0;
}
