// Hammock Simulator: the process that shows and drives simulator panes.
//
// One host per daemon, like the browser host. It talks to CoreSimulator and
// SimulatorKit (private frameworks; see docs/internals/simulator-panes.md) so
// that no Simulator.app window is needed:
//
// - Display: a device's framebuffer is an IOSurface, updated in place. The
//   host puts it in a layer of its own CAContext; the app shows that context
//   through CALayerHost (as browser panes do), so frames are never copied.
// - Input: touches, buttons and keys become Indigo HID messages sent through
//   SimulatorKit's HID client.
// - Screenshots for agents, as PNG.
// - Video for remote viewers (the iPhone, guests): H.264 per viewer
//   (helpers/media/encoder.m), sent on a "media" connection (ADR-0012).
//
// The host outlives the app and the daemon; devices keep running. It quits
// after a while with no clients.
//
// Protocol: newline-delimited JSON over a Unix socket, any number of clients.
// "id" is the pane id; "udid" the device.
//   client -> host: open {id, udid}, close {id}, attach {id}, detach {id},
//                   touch {id, phase: down|move|up, x, y (0..1 of the screen)},
//                   button {id, name: home|lock|siri|side|keyboard},
//                   rotate {id, value, seq}, gesture {id, paths, seq} (through the
//                   device's XCTest runner; a foldable rotates through its guest
//                   helper), hinge {id, angle, ms, seq} (a foldable's fold),
//                   text {id, text}, key {id, usage, shift, cmd},
//                   paste {id, text} (through the device's pasteboard),
//                   copy {id, seq} (⌘C, then the device's pasteboard),
//                   shot {id, seq}, devices {seq}, barrier {seq} (answered
//                   once the lines before it are handled), quit,
//                   media (this connection carries video from now on)
//   on a media connection:
//                   encode {id, sid, bitrate, fps, maxPixels} (a viewer's
//                   session), rate {sid, bitrate, fps}, keyframe {sid},
//                   refresh {sid} (after a loss: a frame from an acknowledged
//                   long-term reference), ltr {sid, tokens} (LTR frames the
//                   viewer has), hold {sid, on} (pause encoding while the
//                   viewer's link is backed up), stop {sid}
//   host -> client: hello {pid, built, ids}, state {id, udid, name, booted,
//                   w, h (points), scale, chrome (a .devicechrome's Resources
//                   directory), mask (the screen's shape, a PDF)},
//                   layer {id, ctx} (to viewers), copyResult {seq, text},
//                   shotResult {seq, png | error}, devicesResult {seq, devices},
//                   barrierResult {seq}, runnerResult {seq, error?}; state
//                   also has foldable, hinge
//   on a media connection, after one line {"t":"mediaReady"}: binary
//   records as HammockMediaRecord writes them (helpers/media/encoder.h): sid,
//   flags (keyframe, refresh), capture time, size, LTR token, then the frame
//   (AVCC; keyframes lead with their parameter sets: VPS, SPS and PPS for
//   HEVC, SPS and PPS for H.264).
#import <AppKit/AppKit.h>
#import <CoreImage/CoreImage.h>
#import <IOSurface/IOSurface.h>

#import "../media/encoder.h"
#import <QuartzCore/QuartzCore.h>
#import <Security/Security.h>
#import <objc/message.h>
#import <objc/runtime.h>
#include <dlfcn.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>

#include "../common/peer.h"

@interface CAContext : NSObject
+ (instancetype)contextWithCGSConnection:(uint32_t)cid options:(NSDictionary *)opts;
@property(readonly) uint32_t contextId;
@property(retain) CALayer *layer;
@end
extern uint32_t CGSMainConnectionID(void);
@interface CALayer (Private)
- (void)setContentsChanged;  // re-read an IOSurface updated in place
@end

// SimulatorKit's Indigo HID message builders (Xcode 27 signatures).
typedef void *(*MouseFn)(CGPoint *, CGPoint *, uint32_t target, NSEventType type, uint32_t edge, double w, double h);
typedef void *(*ButtonFn)(uint32_t source, uint32_t direction, uint32_t target);
typedef void *(*KeyFn)(uint32_t usage, uint32_t direction);
static MouseFn indigoMouse;
static ButtonFn indigoButton;
static KeyFn indigoKey;

static const uint32_t kTouchScreen = 0x32, kHardware = 0x33, kDown = 1, kUp = 2;

#define MSG(obj, sel, ...) ((id(*)(id, SEL, ...))objc_msgSend)(obj, NSSelectorFromString(sel), ##__VA_ARGS__)

// Where Xcode is: the one xcode-select chose (its link, read without running
// it), else /Applications/Xcode.app. Not DEVELOPER_DIR: library validation
// is off (it loads Xcode's private frameworks), so whoever could set this
// process's environment could have it load their code. hmkd starts it with
// a filtered environment that has no DEVELOPER_DIR anyway.
static NSString *developerDir(void) {
  static NSString *dir;
  if (!dir) {
    NSString *chosen = [NSFileManager.defaultManager destinationOfSymbolicLinkAtPath:@"/var/db/xcode_select_link" error:nil];
    NSString *sk = [chosen stringByAppendingPathComponent:@"../SharedFrameworks/SimulatorKit.framework"];
    dir = sk && [NSFileManager.defaultManager fileExistsAtPath:sk] ? chosen : @"/Applications/Xcode.app/Contents/Developer";
  }
  return dir;
}

// Whether the code at path (a framework or binary) is Apple's own, signed by
// Apple, as Xcode's frameworks are: checked before loading it, since library
// validation can't.
static BOOL signedByApple(NSString *path) {
  SecStaticCodeRef code = NULL;
  SecRequirementRef req = NULL;
  BOOL ok = SecStaticCodeCreateWithPath((__bridge CFURLRef)[NSURL fileURLWithPath:path], kSecCSDefaultFlags, &code) == errSecSuccess &&
            SecRequirementCreateWithString(CFSTR("anchor apple"), kSecCSDefaultFlags, &req) == errSecSuccess &&
            SecStaticCodeCheckValidity(code, kSecCSDoNotValidateResources, req) == errSecSuccess;
  if (code) CFRelease(code);
  if (req) CFRelease(req);
  return ok;
}

static id serviceContext(void) {
  static id ctx;
  if (!ctx) {
    ctx = ((id(*)(id, SEL, id, id *))objc_msgSend)(objc_getClass("SimServiceContext"), NSSelectorFromString(@"sharedServiceContextForDeveloperDir:error:"), developerDir(), nil);
  }
  return ctx;
}

static NSArray *allDevices(void) {
  id set = ((id(*)(id, SEL, id *))objc_msgSend)(serviceContext(), NSSelectorFromString(@"defaultDeviceSetWithError:"), nil);
  return [set valueForKey:@"devices"] ?: @[];
}

static id deviceFor(NSString *udid) {
  for (id d in allDevices()) {
    if ([[[d valueForKey:@"UDID"] UUIDString] isEqualToString:udid]) return d;
  }
  return nil;
}

static BOOL booted(id dev) { return [[dev valueForKey:@"state"] integerValue] == 3; }

#pragma mark - Panes

@class Conn;
@class Session;

@interface Pane : NSObject
@property(copy) NSString *pid, *udid;
@property(strong) id device, screen, hid;
@property(strong) CAContext *context;
@property(strong) CALayer *root, *content;
@property(weak) Conn *viewer;
@property CGSize pixels;  // as it's showing (turned, if the device is)
@property double scale;
@property int turn;  // degrees clockwise the picture is turned
// A foldable has two screens: the cover (shown closed) and the larger inner
// one (open). The pane shows whichever is lit.
@property BOOL foldable, inner;
// Where its touches go: 0 for the usual touch screen, else a screen's own
// (SimulatorKit: 0x40000000 | screenID; a foldable's inner screen needs it).
@property uint32_t touchTarget;
@property(copy) NSString *callbackID;
@property(strong) NSMutableArray<Session *> *sessions;
@end
@implementation Pane
@end

@interface Conn : NSObject
@property int fd;
@property(strong) dispatch_source_t src, writeSrc;
@property(strong) NSMutableData *buf, *out;
@property BOOL media;
@end
@implementation Conn
@end

// One remote viewer's video of a pane.
@interface Session : NSObject
@property(copy) NSString *sid;
@property(weak) Pane *pane;
@property(weak) Conn *conn;
@property(strong) HammockEncoder *encoder;
@property CFTimeInterval last;  // when a frame was last encoded
@property BOOL scheduled, held, dirty;
@property BOOL lost;  // frames dropped for a backlog: waiting for a keyframe (HammockAdmitFrame)
@end
@implementation Session
@end

// A device's XCTest runner (helpers/simulator/runner): rotation and
// multi-finger gestures, which only an XCTest session may synthesize. Started
// on first use by `xcodebuild test-without-building`; it connects back to a
// socket of ours and takes one JSON command per line, answering each in turn.
typedef void (^RunnerDone)(NSString *error);
@interface Runner : NSObject
@property(copy) NSString *udid, *socketPath;
@property(strong) NSTask *task;
@property int listenFd, fd;
@property(strong) dispatch_source_t acceptSrc, readSrc;
@property(strong) NSMutableData *buf;
@property(strong) NSMutableArray<NSData *> *queued;        // before it's ready
@property(strong) NSMutableArray<RunnerDone> *waiting;    // sent or queued, in order
@property BOOL ready, stopping;
@end
@implementation Runner
@end

// A foldable's guest helper: `xcrun simctl spawn` of hmk-sim-guest, taking
// one command per line on stdin and answering each with OK or ERROR.
@interface Guest : NSObject
@property(copy) NSString *udid;
@property(strong) NSTask *task;
@property(strong) NSFileHandle *in;
@property(strong) NSMutableData *buf;
@property(strong) NSMutableArray<NSString *> *queued;    // before READY
@property(strong) NSMutableArray<RunnerDone> *waiting;  // in order
@property BOOL ready;
@end
@implementation Guest
@end

@interface Host : NSObject
@property(copy) NSString *socketPath;
@property(strong) NSMutableDictionary<NSString *, Pane *> *panes;
@property(strong) NSMutableDictionary<NSString *, Runner *> *runners;  // by udid
// The orientation each foldable was last turned to (by udid), by
// rotateFoldable, which turns left and right from it: kept only in this
// process, so after the host restarts a relative turn starts from portrait.
// (How a screen is drawn turned comes from its uiOrientation instead.)
@property(strong) NSMutableDictionary<NSString *, NSNumber *> *rotations;
// Foldables: each one's hinge angle as last set, and its guest helper
// (helpers/simulator/guest, run inside the device), by udid.
@property(strong) NSMutableDictionary<NSString *, NSNumber *> *hinges;
@property(strong) NSMutableDictionary<NSString *, Guest *> *guests;
@property(strong) NSMutableArray<Conn *> *conns;
@property(strong) dispatch_source_t acceptSrc;
@property NSTimeInterval lonelySince;
@property time_t built;
@end

static Host *gHost;

@implementation Host

- (void)listen {
  [NSFileManager.defaultManager createDirectoryAtPath:self.socketPath.stringByDeletingLastPathComponent withIntermediateDirectories:YES attributes:nil error:nil];
  unlink(self.socketPath.fileSystemRepresentation);
  int fd = socket(AF_UNIX, SOCK_STREAM, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  strlcpy(a.sun_path, self.socketPath.fileSystemRepresentation, sizeof a.sun_path);
  if (bind(fd, (struct sockaddr *)&a, sizeof a) || listen(fd, 8)) {
    perror("simulator-host: listen");
    exit(1);
  }
  chmod(a.sun_path, 0600);
  self.acceptSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, fd, 0, dispatch_get_main_queue());
  dispatch_source_set_event_handler(self.acceptSrc, ^{
    int c = accept(fd, NULL, NULL);
    if (c < 0) return;
    // Only hmkd and the Hammock app, by their signatures (helpers/common/
    // peer.h): not anything else of yours, an agent in a pane, past hmkd's
    // checks.
    pid_t peer = 0;
    const char *why = "";
    if (!HammockPeerAllowed(c, HammockPeerDaemon | HammockPeerApp, &peer, &why)) {
      fprintf(stderr, "%s: refused a connection from pid %d\n", getprogname(), peer);
      close(c);
      return;
    }
    fprintf(stderr, "%s: connection from pid %d (%s)\n", getprogname(), peer, why);
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
    [self send:@{@"t" : @"hello", @"pid" : @(getpid()), @"ids" : self.panes.allKeys, @"built" : @(self.built)} to:conn];
    for (Pane *p in self.panes.allValues) [self send:[self stateOf:p] to:conn];
  });
  dispatch_resume(self.acceptSrc);
  // No clients for ten minutes (the daemon is gone for good): quit. The
  // devices keep running; they belong to CoreSimulator.
  self.lonelySince = NSDate.date.timeIntervalSince1970;
  [NSTimer scheduledTimerWithTimeInterval:30 repeats:YES block:^(NSTimer *timer) {
    if (self.lonelySince && NSDate.date.timeIntervalSince1970 - self.lonelySince > 600) exit(0);
  }];
}

- (void)drop:(Conn *)c {
  if (!c || ![self.conns containsObject:c]) return;
  for (Pane *p in self.panes.allValues) {
    for (Session *s in [p.sessions copy]) {
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
  if (c.out.length > 64 << 20) {
    [self drop:c];
    return;
  }
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
    [self resumeLost:c];
  }
}

// The connection caught up: viewers whose frames were dropped (HammockAdmitFrame)
// start again from a keyframe now, not when the screen next changes.
- (void)resumeLost:(Conn *)c {
  for (Pane *p in self.panes.allValues) {
    for (Session *s in p.sessions) {
      if (s.conn != c || !s.lost || s.held) continue;
      [s.encoder requestKeyframe];
      [self encode:s];
    }
  }
}

- (void)broadcast:(NSDictionary *)msg {
  for (Conn *c in [self.conns copy]) [self send:msg to:c];
}

- (NSDictionary *)stateOf:(Pane *)p {
  id dev = p.device ?: deviceFor(p.udid);
  CGFloat s = p.scale ?: 1;
  NSMutableDictionary *m = [@{@"t" : @"state", @"id" : p.pid, @"udid" : p.udid ?: @"", @"name" : [dev valueForKey:@"name"] ?: @"",
                              @"booted" : (dev && booted(dev)) ? @YES : @NO, @"w" : @(p.pixels.width / s), @"h" : @(p.pixels.height / s),
                              @"scale" : @(s)} mutableCopy];
  // The device's frame, as DeviceHub draws it: the shown screen's own (a
  // foldable's screens each have one), else the device type's.
  id type = [dev valueForKey:@"deviceType"];
  id props = p.screen ? MSG(p.screen, @"screenProperties") : nil;
  NSString *chromeID = (props ? MSG(props, @"chromeIdentifier") : nil) ?: [type valueForKey:@"chromeIdentifier"];
  if (chromeID.length) {
    NSString *dir = [NSString stringWithFormat:@"/Library/Developer/DeviceKit/Chrome/%@.devicechrome/Contents/Resources", chromeID.pathExtension];
    if ([NSFileManager.defaultManager fileExistsAtPath:[dir stringByAppendingPathComponent:@"chrome.json"]]) m[@"chrome"] = dir;
  }
  // The screen's shape: its own mask, a PDF in the device type's bundle.
  NSString *maskID = props ? MSG(props, @"framebufferMaskIdentifier") : nil;
  NSString *own = maskID.length ? [NSString stringWithFormat:@"%@/Contents/Resources/%@.pdf", [type valueForKey:@"bundlePath"], maskID] : nil;
  NSURL *mask = [type valueForKey:@"framebufferMaskURL"];
  if (own && [NSFileManager.defaultManager fileExistsAtPath:own]) m[@"mask"] = own;
  else if (mask.path) m[@"mask"] = mask.path;
  if (p.foldable) {
    m[@"foldable"] = @YES;
    m[@"hinge"] = self.hinges[p.udid] ?: @0;
  }
  return m;
}

#pragma mark Devices

// The device's screens: display ports with a framebuffer (two on a foldable).
static NSArray *screensOf(id dev) {
  NSMutableArray *out = [NSMutableArray array];
  for (id port in MSG([dev valueForKey:@"io"], @"ioPorts")) {
    id desc = MSG(port, @"descriptor");
    if (![desc conformsToProtocol:objc_getProtocol("SimDisplayIOSurfaceRenderable")]) continue;
    if (MSG(desc, @"framebufferSurface")) [out addObject:desc];
  }
  return out;
}

static double area(id screen) {
  IOSurfaceRef s = (__bridge IOSurfaceRef)MSG(screen, @"framebufferSurface");
  return s ? (double)IOSurfaceGetWidth(s) * IOSurfaceGetHeight(s) : 0;
}

// How much of a screen is lit, from a grid of samples: a foldable's other
// screen is black.
static double lit(id screen) {
  IOSurfaceRef s = (__bridge IOSurfaceRef)MSG(screen, @"framebufferSurface");
  if (!s || IOSurfaceGetBytesPerElement(s) != 4) return 0;
  IOSurfaceLock(s, kIOSurfaceLockReadOnly, NULL);
  const uint8_t *base = IOSurfaceGetBaseAddress(s);
  size_t w = IOSurfaceGetWidth(s), h = IOSurfaceGetHeight(s), row = IOSurfaceGetBytesPerRow(s);
  int on = 0, n = 0;
  for (int i = 1; i < 16; i++) {
    for (int j = 1; j < 16; j++, n++) {
      const uint8_t *px = base + (h * j / 16) * row + (w * i / 16) * 4;
      if (px[0] > 8 || px[1] > 8 || px[2] > 8) on++;
    }
  }
  IOSurfaceUnlock(s, kIOSurfaceLockReadOnly, NULL);
  return (double)on / n;
}

// The screen to show: the only one, or the lit one of a foldable's two
// (both dark, as while the device sleeps: the one already shown).
- (id)screenFor:(Pane *)p device:(id)dev {
  NSArray *screens = screensOf(dev);
  p.foldable = screens.count > 1;
  if (screens.count < 2) return screens.firstObject;
  id best = nil;
  double most = 0.02;
  for (id s in screens) {
    double l = lit(s);
    if (l > most) best = s, most = l;
  }
  if (best) return best;
  for (id s in screens) {
    if (s == p.screen) return s;
  }
  // The cover: the smaller.
  return [screens sortedArrayUsingComparator:^NSComparisonResult(id a, id b) { return area(a) < area(b) ? NSOrderedAscending : NSOrderedDescending; }].firstObject;
}

// A foldable after its hinge moved: show the screen that's now lit.
- (void)recheckScreens:(NSString *)udid {
  for (Pane *p in self.panes.allValues) {
    if (![p.udid isEqualToString:udid] || !p.device || !p.foldable) continue;
    id screen = [self screenFor:p device:p.device];
    if (screen && screen != p.screen) [self attach:p screen:screen];
  }
}

- (void)open:(NSString *)pid udid:(NSString *)udid {
  Pane *p = self.panes[pid];
  if (p && [p.udid isEqualToString:udid] && p.screen) return;
  if (p) [self teardown:p];
  p = [Pane new];
  p.pid = pid;
  p.udid = udid;
  self.panes[pid] = p;
  p.context = [CAContext contextWithCGSConnection:CGSMainConnectionID() options:@{}];
  p.root = [CALayer layer];
  p.root.anchorPoint = CGPointZero;
  p.root.geometryFlipped = YES;
  p.content = [CALayer layer];
  p.content.anchorPoint = CGPointZero;
  p.content.contentsGravity = kCAGravityResize;
  [p.root addSublayer:p.content];
  p.context.layer = p.root;
  [self connect:p attempt:0];
}

// Boots the device if it needs it, then attaches to its screen and HID.
- (void)connect:(Pane *)p attempt:(int)attempt {
  if (self.panes[p.pid] != p) return;
  id dev = deviceFor(p.udid);
  p.device = dev;
  if (!dev) {
    [self broadcast:[self stateOf:p]];
    return;
  }
  if (!booted(dev)) {
    if (attempt == 0) {
      NSTask *t = [NSTask launchedTaskWithLaunchPath:@"/usr/bin/xcrun" arguments:@[ @"simctl", @"boot", p.udid ]];
      (void)t;
    }
    [self broadcast:[self stateOf:p]];
    if (attempt < 120) {
      dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{ [self connect:p attempt:attempt + 1]; });
    }
    return;
  }
  id screen = [self screenFor:p device:dev];
  if (!screen) {
    // Booted, but the display isn't up yet.
    if (attempt < 120) {
      dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC / 2), dispatch_get_main_queue(), ^{ [self connect:p attempt:attempt + 1]; });
    }
    return;
  }
  NSError *err = nil;
  p.hid = ((id(*)(id, SEL, id, NSError **))objc_msgSend)([objc_getClass("_TtC12SimulatorKit24SimDeviceLegacyHIDClient") alloc],
                                                         NSSelectorFromString(@"initWithDevice:error:"), dev, &err);
  if (!p.hid) fprintf(stderr, "simulator-host: no HID for %s: %s\n", p.udid.UTF8String, err.description.UTF8String);
  // A foldable's hinge, until it's set: as its lit screen says.
  if (p.foldable && !self.hinges[p.udid]) self.hinges[p.udid] = @([self isInner:screen of:dev] ? 180 : 0);
  [self attach:p screen:screen];
}

- (BOOL)isInner:(id)screen of:(id)dev {
  NSArray *screens = screensOf(dev);
  if (screens.count < 2) return NO;
  for (id s in screens) {
    if (area(s) > area(screen)) return NO;
  }
  return YES;
}

// Shows a screen in the pane, following its frames.
- (void)attach:(Pane *)p screen:(id)screen {
  [self forgetScreen:p];
  p.screen = screen;
  p.inner = p.foldable && [self isInner:screen of:p.device];
  id screenProps = MSG(screen, @"screenProperties");
  uint32_t sid = screenProps ? (uint32_t)((long (*)(id, SEL))objc_msgSend)(screenProps, NSSelectorFromString(@"screenID")) : 0;
  p.touchTarget = p.foldable && sid ? (0x40000000u | sid) : 0;
  [self showSurface:p];
  p.callbackID = NSUUID.UUID.UUIDString;
  NSUUID *uuid = [[NSUUID alloc] initWithUUIDString:p.callbackID];
  __weak Pane *wp = p;
  // New frames update the surface in place; a new surface (rotation) swaps it.
  void (^frame)(void) = ^{
    dispatch_async(dispatch_get_main_queue(), ^{
      [wp.content setContentsChanged];
      for (Session *s in wp.sessions) [gHost frameFor:s];
    });
  };
  void (^surfaces)(void) = ^{
    dispatch_async(dispatch_get_main_queue(), ^{ if (wp) [gHost showSurface:wp]; });
  };
  void (^props)(void) = ^{
    dispatch_async(dispatch_get_main_queue(), ^{ if (wp) [gHost showSurface:wp]; });  // its orientation, say
  };
  if ([screen respondsToSelector:NSSelectorFromString(@"registerScreenCallbacksWithUUID:callbackQueue:frameCallback:surfacesChangedCallback:propertiesChangedCallback:")]) {
    ((void(*)(id, SEL, id, id, id, id, id))objc_msgSend)(screen, NSSelectorFromString(@"registerScreenCallbacksWithUUID:callbackQueue:frameCallback:surfacesChangedCallback:propertiesChangedCallback:"),
                                                        uuid, dispatch_get_main_queue(), frame, surfaces, props);
  } else {
    ((void(*)(id, SEL, id, id))objc_msgSend)(screen, NSSelectorFromString(@"registerCallbackWithUUID:damageRectanglesCallback:"), uuid, ^(id rects) { frame(); });
    ((void(*)(id, SEL, id, id))objc_msgSend)(screen, NSSelectorFromString(@"registerCallbackWithUUID:ioSurfacesChangeCallback:"), uuid, ^(id s) { surfaces(); });
  }
  [self broadcast:[self stateOf:p]];
  if (p.viewer) [self sendLayer:p];
}

// Stops following a pane's screen, but only while its device is booted. A
// device that's shut down or deleted (by simctl, an agent, or a shutdown
// racing a close) takes its screen's remote proxy with it, and messaging
// that proxy crashes inside SimulatorKit (EXC_BAD_ACCESS in ROCK's
// invocation): there's nothing left to unregister from. The device's state
// (a local SimDevice, safe to read) catches up a moment after a shutdown, so
// this narrows the window for shutdowns from outside Hammock rather than
// closing it; the daemon's own shutdown waits for the close (barrier).
- (void)forgetScreen:(Pane *)p {
  if (!p.callbackID || !p.screen) return;
  NSUUID *uuid = [[NSUUID alloc] initWithUUIDString:p.callbackID];
  p.callbackID = nil;
  if (!p.device || !booted(p.device)) return;
  if ([p.screen respondsToSelector:NSSelectorFromString(@"unregisterScreenCallbacksWithUUID:")]) {
    MSG(p.screen, @"unregisterScreenCallbacksWithUUID:", uuid);
  }
}

- (void)showSurface:(Pane *)p {
  IOSurfaceRef s = (__bridge IOSurfaceRef)MSG(p.screen, @"framebufferSurface");
  if (!s) return;
  CGSize native = CGSizeMake(IOSurfaceGetWidth(s), IOSurfaceGetHeight(s));
  // Phones and tablets are 3x or 2x; points keep the layer the device's size.
  double scale = MIN(native.width, native.height) >= 1000 ? 3 : 2;
  int turn = [self rotationOf:p];
  // As it's showing: turned on its side, the picture is landscape.
  CGSize px = turn % 180 ? CGSizeMake(native.height, native.width) : native;
  BOOL changed = !CGSizeEqualToSize(px, p.pixels) || turn != p.turn;
  p.pixels = px;
  p.scale = scale;
  p.turn = turn;
  [CATransaction begin];
  [CATransaction setDisableActions:YES];
  p.root.frame = CGRectMake(0, 0, px.width / scale, px.height / scale);
  // Turned about its centre (the root's y runs down, so positive is clockwise).
  p.content.bounds = CGRectMake(0, 0, native.width / scale, native.height / scale);
  p.content.anchorPoint = CGPointMake(0.5, 0.5);
  p.content.position = CGPointMake(px.width / scale / 2, px.height / scale / 2);
  p.content.affineTransform = CGAffineTransformMakeRotation(turn * M_PI / 180);
  p.content.contents = (__bridge id)s;
  [CATransaction commit];
  for (Session *ses in p.sessions) ses.encoder.rotation = turn;
  if (changed) [self broadcast:[self stateOf:p]];
}

- (void)teardown:(Pane *)p {
  for (Session *s in p.sessions) [s.encoder invalidate];
  [p.sessions removeAllObjects];
  [self forgetScreen:p];
  p.screen = nil;
  p.hid = nil;
  p.context.layer = nil;
  [self.panes removeObjectForKey:p.pid];
  for (Pane *other in self.panes.allValues) {
    if ([other.udid isEqualToString:p.udid]) return;
  }
  [self stopRunner:self.runners[p.udid ?: @""] error:@"the simulator pane closed"];
  [self stopGuest:self.guests[p.udid ?: @""] error:@"the simulator pane closed"];
}

- (void)sendLayer:(Pane *)p {
  [self send:@{@"t" : @"layer", @"id" : p.pid, @"ctx" : @(p.context.contextId)} to:p.viewer];
}

#pragma mark Input

- (void)sendHID:(Pane *)p message:(void *)msg {
  if (!p.hid || !msg) {
    free(msg);
    return;
  }
  ((void(*)(id, SEL, void *, BOOL, id, id))objc_msgSend)(p.hid, NSSelectorFromString(@"sendWithMessage:freeWhenDone:completionQueue:completion:"), msg, YES,
                                                        dispatch_get_main_queue(), ^(NSError *e) {
    if (e) fprintf(stderr, "simulator-host: HID: %s\n", e.description.UTF8String);
  });
}

// How far the picture turns: the screen's interface orientation, as the
// device reports it (SimScreenProperties.uiOrientation: 1 portrait, 2 upside
// down, 3 and 4 landscape). The framebuffer itself never turns. Each screen
// reports its own (a foldable's inner screen sits a quarter turn round).
- (int)rotationOf:(Pane *)p {
  if (!p.screen) return 0;
  id props = MSG(p.screen, @"screenProperties");
  long o = props ? ((long (*)(id, SEL))objc_msgSend)(props, NSSelectorFromString(@"uiOrientation")) : 1;
  switch (o) {
    case 2: return 180;
    case 3: return 90;
    case 4: return 270;
    default: return 0;
  }
}

// Re-reads a device's screens as a rotation settles.
- (void)refreshSoon:(NSString *)udid {
  static const double after[] = {0.2, 0.6, 1.2};
  for (int i = 0; i < 3; i++) {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(after[i] * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
      for (Pane *p in gHost.panes.allValues) {
        if ([p.udid isEqualToString:udid] && p.screen) [gHost showSurface:p];
      }
    });
  }
}

// A point as it's showing (0–1, y down) in the device's own portrait.
static CGPoint unturn(CGPoint q, int turn) {
  switch (turn) {
    case 90: return CGPointMake(q.y, 1 - q.x);
    case 180: return CGPointMake(1 - q.x, 1 - q.y);
    case 270: return CGPointMake(1 - q.y, q.x);
    default: return q;
  }
}

// One finger. (A second point builds a two-finger message, but the legacy
// HID client's devices ignore it: pinches go through the runner.)
- (void)touch:(Pane *)p phase:(NSString *)phase x:(double)x y:(double)y {
  NSEventType t = [phase isEqualToString:@"down"] ? NSEventTypeLeftMouseDown : [phase isEqualToString:@"up"] ? NSEventTypeLeftMouseUp : NSEventTypeLeftMouseDragged;
  CGPoint pt = unturn(CGPointMake(fmin(fmax(x, 0), 1), fmin(fmax(y, 0), 1)), [self rotationOf:p]);
  [self sendHID:p message:indigoMouse(&pt, NULL, p.touchTarget ?: kTouchScreen, t, 0, 1.0, 1.0)];
}

- (void)button:(Pane *)p name:(NSString *)name {
  // keyboard: shows or hides the software keyboard (as DeviceHub's ⌘K).
  NSDictionary *sources = @{@"home" : @0x0, @"lock" : @0x1, @"side" : @0xbb8, @"siri" : @0x400002, @"keyboard" : @0x3f0};
  NSNumber *src = sources[name];
  if (!src) return;
  [self sendHID:p message:indigoButton(src.unsignedIntValue, kDown, kHardware)];
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC / 20), dispatch_get_main_queue(), ^{
    [self sendHID:p message:indigoButton(src.unsignedIntValue, kUp, kHardware)];
  });
}

static const uint32_t kShift = 225, kCommand = 227, kUsageC = 6, kUsageV = 25;

- (void)key:(Pane *)p usage:(uint32_t)usage shift:(BOOL)shift cmd:(BOOL)cmd {
  if (cmd) [self sendHID:p message:indigoKey(kCommand, kDown)];
  if (shift) [self sendHID:p message:indigoKey(kShift, kDown)];
  [self sendHID:p message:indigoKey(usage, kDown)];
  [self sendHID:p message:indigoKey(usage, kUp)];
  if (shift) [self sendHID:p message:indigoKey(kShift, kUp)];
  if (cmd) [self sendHID:p message:indigoKey(kCommand, kUp)];
}

- (void)key:(Pane *)p usage:(uint32_t)usage shift:(BOOL)shift {
  [self key:p usage:usage shift:shift cmd:NO];
}

// simctl pbcopy/pbpaste: the device's pasteboard.
static void runSimctl(NSArray *args, NSData *input, void (^done)(NSData *out)) {
  NSTask *t = [NSTask new];
  t.launchPath = @"/usr/bin/xcrun";
  t.arguments = [@[ @"simctl" ] arrayByAddingObjectsFromArray:args];
  NSPipe *in = [NSPipe pipe], *out = [NSPipe pipe];
  t.standardInput = in;
  t.standardOutput = out;
  t.terminationHandler = ^(NSTask *task) {
    NSData *d = [out.fileHandleForReading readDataToEndOfFile];
    dispatch_async(dispatch_get_main_queue(), ^{ done(d); });
  };
  if (![t launchAndReturnError:nil]) {
    done(nil);
    return;
  }
  if (input) [in.fileHandleForWriting writeData:input];
  [in.fileHandleForWriting closeFile];
}

// Puts text on the device's pasteboard and presses ⌘V.
- (void)paste:(Pane *)p text:(NSString *)text {
  runSimctl(@[ @"pbcopy", p.udid ], [text dataUsingEncoding:NSUTF8StringEncoding], ^(NSData *_) {
    [self key:p usage:kUsageV shift:NO cmd:YES];
  });
}

// Presses ⌘C, then reads the device's pasteboard.
- (void)copy:(Pane *)p seq:(id)seq to:(Conn *)c {
  [self key:p usage:kUsageC shift:NO cmd:YES];
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC / 3), dispatch_get_main_queue(), ^{
    runSimctl(@[ @"pbpaste", p.udid ], nil, ^(NSData *out) {
      NSString *s = out ? [[NSString alloc] initWithData:out encoding:NSUTF8StringEncoding] : nil;
      [self send:@{@"t" : @"copyResult", @"seq" : seq, @"text" : s ?: @""} to:c];
    });
  });
}

// HID keyboard usages for typing ASCII on a US layout.
static BOOL usageFor(unichar c, uint32_t *usage, BOOL *shift) {
  static NSString *plain = @"abcdefghijklmnopqrstuvwxyz1234567890\n\e\b\t -=[]\\;'`,./";
  static NSString *shifted = @"ABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*()\n\e\b\t _+{}|:\"~<>?";
  static const uint32_t usages[] = {4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
                                    30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 51, 52, 53, 54, 55, 56};
  NSRange r = [plain rangeOfString:[NSString stringWithCharacters:&c length:1]];
  if (r.location != NSNotFound) {
    *usage = usages[r.location];
    *shift = NO;
    return YES;
  }
  r = [shifted rangeOfString:[NSString stringWithCharacters:&c length:1]];
  if (r.location != NSNotFound) {
    *usage = usages[r.location];
    *shift = YES;
    return YES;
  }
  return NO;
}

- (void)type:(Pane *)p text:(NSString *)text {
  // Keys for ASCII; anything else (accents, emoji) goes in by pasting.
  for (NSUInteger i = 0; i < text.length; i++) {
    uint32_t usage;
    BOOL shift;
    unichar c = [text characterAtIndex:i];
    if (c != '\r' && !usageFor(c, &usage, &shift)) {
      [self paste:p text:text];
      return;
    }
  }
  for (NSUInteger i = 0; i < text.length; i++) {
    uint32_t usage;
    BOOL shift;
    unichar c = [text characterAtIndex:i];
    if (c == '\r') c = '\n';
    if (usageFor(c, &usage, &shift)) [self key:p usage:usage shift:shift];
  }
}

#pragma mark Runner

// The runner's xctestrun, ready to run: made once per process by prepareRunner.
static NSString *gRunnerXCTestRun;

// Output of a command (xcode-select -p), trimmed; nil if it fails.
static NSString *commandOutput(NSString *path, NSArray<NSString *> *args) {
  NSTask *t = [NSTask new];
  t.launchPath = path;
  t.arguments = args;
  NSPipe *out = [NSPipe pipe];
  t.standardOutput = out;
  t.standardError = [NSFileHandle fileHandleWithNullDevice];
  if (![t launchAndReturnError:nil]) return nil;
  NSData *d = [out.fileHandleForReading readDataToEndOfFile];
  [t waitUntilExit];
  if (t.terminationStatus != 0) return nil;
  return [[[NSString alloc] initWithData:d encoding:NSUTF8StringEncoding]
      stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
}

// The runner, ready to start: Contents/Resources/runner (build.sh) holds only Hammock's parts (our test bundle,
// in its runner app's PlugIns; the host app; the xctestrun). The runner app itself is Apple's XCTRunner,
// which isn't ours to ship, so it's made here as xcodebuild makes it, from the template in this Mac's own
// Xcode (the one xcodebuild will run with): the app copied, its executable named after the runner app and
// its Info.plist's placeholders filled in. Into a folder of this process's own, so two hosts (prod and β)
// never share one.
static NSString *prepareRunner(NSString **error) {
  if (gRunnerXCTestRun) return gRunnerXCTestRun;
  NSFileManager *fm = NSFileManager.defaultManager;
  NSString *bundled = [NSBundle.mainBundle.resourcePath stringByAppendingPathComponent:@"runner"];
  if (![fm fileExistsAtPath:bundled]) {
    *error = @"rotation and gestures need the simulator runner, which wasn't built (just simulator, with xcodegen installed)";
    return nil;
  }
  NSString *developer = commandOutput(@"/usr/bin/xcode-select", @[ @"-p" ]);
  NSString *template = [developer stringByAppendingPathComponent:
                                      @"Platforms/iPhoneSimulator.platform/Developer/Library/Xcode/Agents/XCTRunner.app"];
  if (!developer || ![fm fileExistsAtPath:[template stringByAppendingPathComponent:@"XCTRunner"]]) {
    *error = [NSString stringWithFormat:@"rotation and gestures need Xcode's XCTest runner, which isn't in %@ (is Xcode selected? xcode-select -p)",
                                        developer ?: @"the selected developer folder"];
    return nil;
  }
  NSString *dir = [NSTemporaryDirectory() stringByAppendingFormat:@"hmk-sim-runner-%d", getpid()];
  [fm removeItemAtPath:dir error:nil];
  NSError *e = nil;
  if (![fm copyItemAtPath:bundled toPath:dir error:&e]) {
    *error = [NSString stringWithFormat:@"couldn't prepare the simulator runner: %@", e.localizedDescription];
    return nil;
  }
  NSString *xctestrun = nil;
  for (NSString *f in [fm contentsOfDirectoryAtPath:dir error:nil]) {
    if ([f.pathExtension isEqualToString:@"xctestrun"]) xctestrun = [dir stringByAppendingPathComponent:f];
  }
  BOOL made = NO;
  for (NSString *rel in [fm subpathsOfDirectoryAtPath:dir error:nil]) {
    // <products>/HammockSimRunner-Runner.plugins (build.sh) becomes HammockSimRunner-Runner.app, named for
    // the test target, as the xctestrun expects.
    if (![rel.lastPathComponent hasSuffix:@"-Runner.plugins"]) continue;
    NSString *plugins = [dir stringByAppendingPathComponent:rel];
    NSString *name = rel.lastPathComponent.stringByDeletingPathExtension;
    NSString *app = [[plugins stringByDeletingLastPathComponent] stringByAppendingPathComponent:[name stringByAppendingPathExtension:@"app"]];
    NSMutableDictionary *info = [NSMutableDictionary dictionaryWithContentsOfFile:[template stringByAppendingPathComponent:@"Info.plist"]];
    if (!info) break;
    info[@"CFBundleExecutable"] = name;
    info[@"CFBundleName"] = name;
    info[@"CFBundleIdentifier"] = @"com.homegrownsw.hammock.simrunner.xctrunner";
    made = [fm createDirectoryAtPath:app withIntermediateDirectories:NO attributes:nil error:&e] &&
           [fm moveItemAtPath:plugins toPath:[app stringByAppendingPathComponent:@"PlugIns"] error:&e] &&
           [fm copyItemAtPath:[template stringByAppendingPathComponent:@"XCTRunner"] toPath:[app stringByAppendingPathComponent:name] error:&e] &&
           [info writeToURL:[NSURL fileURLWithPath:[app stringByAppendingPathComponent:@"Info.plist"]] error:&e];
    [fm copyItemAtPath:[template stringByAppendingPathComponent:@"PkgInfo"] toPath:[app stringByAppendingPathComponent:@"PkgInfo"] error:nil];
    break;
  }
  if (!xctestrun || !made) {
    [fm removeItemAtPath:dir error:nil];
    *error = [NSString stringWithFormat:@"couldn't prepare the simulator runner from %@%@", template,
                                        e ? [@": " stringByAppendingString:e.localizedDescription] : @""];
    return nil;
  }
  gRunnerXCTestRun = xctestrun;
  return xctestrun;
}

// Sends one command to the device's runner, starting it if need be (the
// first start takes a while: xcodebuild installs and launches it).
- (void)runner:(NSString *)udid command:(NSDictionary *)cmd done:(RunnerDone)done {
  Runner *r = self.runners[udid];
  if (!r) {
    NSString *error = nil;
    r = [self startRunner:udid error:&error];
    if (!r) {
      done(error);
      return;
    }
  }
  NSMutableData *line = [[NSJSONSerialization dataWithJSONObject:cmd options:0 error:nil] mutableCopy];
  [line appendBytes:"\n" length:1];
  [r.waiting addObject:[done copy]];
  if (r.ready) (void)write(r.fd, line.bytes, line.length);
  else [r.queued addObject:line];
}

- (Runner *)startRunner:(NSString *)udid error:(NSString **)error {
  NSString *xctestrun = prepareRunner(error);
  if (!xctestrun) return nil;
  Runner *r = [Runner new];
  r.udid = udid;
  r.fd = -1;
  r.buf = [NSMutableData data];
  r.queued = [NSMutableArray array];
  r.waiting = [NSMutableArray array];
  // Short: a Unix socket path has 104 bytes at most.
  r.socketPath = [NSTemporaryDirectory() stringByAppendingFormat:@"hmk-sim-%@.sock", [udid substringToIndex:MIN(8, udid.length)]];
  unlink(r.socketPath.fileSystemRepresentation);
  r.listenFd = socket(AF_UNIX, SOCK_STREAM, 0);
  struct sockaddr_un addr = {.sun_family = AF_UNIX};
  strlcpy(addr.sun_path, r.socketPath.fileSystemRepresentation, sizeof(addr.sun_path));
  if (bind(r.listenFd, (struct sockaddr *)&addr, sizeof(addr)) != 0 || listen(r.listenFd, 1) != 0) {
    *error = [NSString stringWithFormat:@"simulator runner socket: %s", strerror(errno)];
    close(r.listenFd);
    return nil;
  }
  self.runners[udid] = r;
  r.acceptSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, r.listenFd, 0, dispatch_get_main_queue());
  __weak Runner *wr = r;
  dispatch_source_set_event_handler(r.acceptSrc, ^{ [gHost runnerConnected:wr]; });
  dispatch_resume(r.acceptSrc);

  NSTask *t = [NSTask new];
  t.launchPath = @"/usr/bin/xcrun";
  t.arguments = @[ @"xcodebuild", @"test-without-building", @"-xctestrun", xctestrun, @"-destination", [@"id=" stringByAppendingString:udid],
                   @"-only-testing", @"HammockSimRunner/HammockSimRunner/testServe", @"-test-timeouts-enabled", @"NO",
                   @"-collect-test-diagnostics", @"never", @"-parallel-testing-enabled", @"NO" ];
  NSMutableDictionary *env = [NSProcessInfo.processInfo.environment mutableCopy];
  env[@"TEST_RUNNER_HMK_SIM_RUNNER_SOCKET"] = r.socketPath;
  t.environment = env;
  NSString *log = [NSTemporaryDirectory() stringByAppendingFormat:@"hmk-sim-runner-%@.log", udid];
  [NSFileManager.defaultManager createFileAtPath:log contents:nil attributes:nil];
  NSFileHandle *lh = [NSFileHandle fileHandleForWritingAtPath:log];
  t.standardOutput = lh;
  t.standardError = lh;
  t.terminationHandler = ^(NSTask *task) {
    dispatch_async(dispatch_get_main_queue(), ^{
      Runner *r = wr;
      if (r) [gHost stopRunner:r error:[NSString stringWithFormat:@"the simulator runner stopped (xcodebuild exited %d; see %@)", task.terminationStatus, log]];
    });
  };
  NSError *e = nil;
  if (![t launchAndReturnError:&e]) {
    [self stopRunner:r error:nil];
    *error = [NSString stringWithFormat:@"couldn't start xcodebuild: %@", e.localizedDescription];
    return nil;
  }
  r.task = t;
  fprintf(stderr, "simulator-host: starting the runner for %s (log %s)\n", udid.UTF8String, log.UTF8String);
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 180 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
    Runner *r = wr;
    if (r && !r.ready) [gHost stopRunner:r error:@"the simulator runner didn't start in time"];
  });
  return r;
}

- (void)runnerConnected:(Runner *)r {
  if (!r || r.fd >= 0) return;
  int fd = accept(r.listenFd, NULL, NULL);
  if (fd < 0) return;
  r.fd = fd;
  dispatch_source_cancel(r.acceptSrc);
  r.readSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, fd, 0, dispatch_get_main_queue());
  __weak Runner *wr = r;
  dispatch_source_set_event_handler(r.readSrc, ^{
    Runner *r = wr;
    if (!r) return;
    char b[16384];
    ssize_t n = read(r.fd, b, sizeof b);
    if (n <= 0) {
      [gHost stopRunner:r error:@"the simulator runner disconnected"];
      return;
    }
    [r.buf appendBytes:b length:n];
    for (;;) {
      NSRange nl = [r.buf rangeOfData:[NSData dataWithBytes:"\n" length:1] options:0 range:NSMakeRange(0, r.buf.length)];
      if (nl.location == NSNotFound) break;
      NSData *line = [r.buf subdataWithRange:NSMakeRange(0, nl.location)];
      [r.buf replaceBytesInRange:NSMakeRange(0, nl.location + 1) withBytes:NULL length:0];
      [gHost runner:r said:[NSJSONSerialization JSONObjectWithData:line options:0 error:nil]];
    }
  });
  dispatch_resume(r.readSrc);
}

- (void)runner:(Runner *)r said:(NSDictionary *)m {
  if (![m isKindOfClass:NSDictionary.class]) return;
  if (m[@"device"]) [self refreshSoon:r.udid];  // it may have turned
  if ([m[@"ready"] boolValue]) {
    r.ready = YES;
    fprintf(stderr, "simulator-host: runner ready for %s\n", r.udid.UTF8String);
    for (NSData *line in r.queued) (void)write(r.fd, line.bytes, line.length);
    [r.queued removeAllObjects];
    return;
  }
  if (r.waiting.count == 0) return;
  RunnerDone done = r.waiting.firstObject;
  [r.waiting removeObjectAtIndex:0];
  done([m[@"ok"] boolValue] ? nil : (m[@"error"] ?: @"the runner failed"));
}

// Ends the runner: closing its socket ends its test, and xcodebuild with it.
- (void)stopRunner:(Runner *)r error:(NSString *)error {
  if (!r || r.stopping) return;
  r.stopping = YES;
  if (self.runners[r.udid] == r) [self.runners removeObjectForKey:r.udid];
  if (r.acceptSrc && !dispatch_source_testcancel(r.acceptSrc)) dispatch_source_cancel(r.acceptSrc);
  if (r.readSrc) dispatch_source_cancel(r.readSrc);
  if (r.fd >= 0) close(r.fd);
  close(r.listenFd);
  unlink(r.socketPath.fileSystemRepresentation);
  NSTask *t = r.task;
  if (t.running) {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
      if (t.running) [t terminate];
    });
  }
  NSArray *waiting = [r.waiting copy];
  [r.waiting removeAllObjects];
  for (RunnerDone done in waiting) done(error ?: @"the simulator runner stopped");
}

#pragma mark Guest helper (foldables)

// Sends a line to the device's guest helper, starting it if need be.
- (void)guest:(NSString *)udid command:(NSString *)line done:(RunnerDone)done {
  Guest *g = self.guests[udid];
  if (!g) {
    NSString *path = [NSBundle.mainBundle.resourcePath stringByAppendingPathComponent:@"hmk-sim-guest"];
    if (![NSFileManager.defaultManager isExecutableFileAtPath:path]) {
      done(@"the simulator guest helper wasn't built (just simulator)");
      return;
    }
    g = [Guest new];
    g.udid = udid;
    g.buf = [NSMutableData data];
    g.queued = [NSMutableArray array];
    g.waiting = [NSMutableArray array];
    NSTask *t = [NSTask new];
    t.launchPath = @"/usr/bin/xcrun";
    t.arguments = @[ @"simctl", @"spawn", udid, path ];
    NSPipe *in = [NSPipe pipe], *out = [NSPipe pipe];
    t.standardInput = in;
    t.standardOutput = out;
    t.standardError = [NSFileHandle fileHandleWithNullDevice];
    __weak Guest *wg = g;
    out.fileHandleForReading.readabilityHandler = ^(NSFileHandle *h) {
      NSData *d = h.availableData;
      dispatch_async(dispatch_get_main_queue(), ^{
        Guest *g = wg;
        if (!g) return;
        if (d.length == 0) {
          h.readabilityHandler = nil;
          [gHost stopGuest:g error:@"the simulator guest helper stopped"];
          return;
        }
        [g.buf appendData:d];
        for (;;) {
          NSRange nl = [g.buf rangeOfData:[NSData dataWithBytes:"\n" length:1] options:0 range:NSMakeRange(0, g.buf.length)];
          if (nl.location == NSNotFound) break;
          NSString *reply = [[NSString alloc] initWithData:[g.buf subdataWithRange:NSMakeRange(0, nl.location)] encoding:NSUTF8StringEncoding];
          [g.buf replaceBytesInRange:NSMakeRange(0, nl.location + 1) withBytes:NULL length:0];
          [gHost guest:g said:reply ?: @""];
        }
      });
    };
    t.terminationHandler = ^(NSTask *_) {
      dispatch_async(dispatch_get_main_queue(), ^{ if (wg) [gHost stopGuest:wg error:@"the simulator guest helper stopped"]; });
    };
    NSError *e = nil;
    if (![t launchAndReturnError:&e]) {
      done([NSString stringWithFormat:@"couldn't start the guest helper: %@", e.localizedDescription]);
      return;
    }
    g.task = t;
    g.in = in.fileHandleForWriting;
    self.guests[udid] = g;
  }
  [g.waiting addObject:[done copy]];
  if (g.ready) [g.in writeData:[[line stringByAppendingString:@"\n"] dataUsingEncoding:NSUTF8StringEncoding]];
  else [g.queued addObject:line];
}

- (void)guest:(Guest *)g said:(NSString *)reply {
  if (!g.ready) {
    if ([reply isEqualToString:@"READY"]) {
      g.ready = YES;
      for (NSString *line in g.queued) [g.in writeData:[[line stringByAppendingString:@"\n"] dataUsingEncoding:NSUTF8StringEncoding]];
      [g.queued removeAllObjects];
    } else {
      [self stopGuest:g error:reply.length ? reply : @"the simulator guest helper failed"];
    }
    return;
  }
  if (g.waiting.count == 0) return;
  RunnerDone done = g.waiting.firstObject;
  [g.waiting removeObjectAtIndex:0];
  done([reply isEqualToString:@"OK"] ? nil : [reply stringByReplacingOccurrencesOfString:@"ERROR " withString:@""]);
}

- (void)stopGuest:(Guest *)g error:(NSString *)error {
  if (!g) return;
  if (self.guests[g.udid] == g) [self.guests removeObjectForKey:g.udid];
  @try {
    [g.in closeFile];  // its stdin ends: it exits
  } @catch (NSException *e) {
  }
  NSArray *waiting = [g.waiting copy];
  [g.waiting removeAllObjects];
  [g.queued removeAllObjects];
  for (RunnerDone done in waiting) done(error);
}

// The fold: hinge to degrees (0 closed, 180 flat) over ms, then the lit
// screen is shown.
- (void)hinge:(Pane *)p angle:(double)angle ms:(double)ms done:(RunnerDone)done {
  NSString *udid = p.udid;
  [self guest:udid command:[NSString stringWithFormat:@"hinge %g %g", angle, ms] done:^(NSString *error) {
    if (!error) {
      gHost.hinges[udid] = @(angle);
      for (Pane *q in gHost.panes.allValues) {
        if ([q.udid isEqualToString:udid]) [gHost broadcast:[gHost stateOf:q]];
      }
    }
    // The device swaps screens as it settles.
    static const double settle[] = {0.3, 0.8, 1.5, 3};
    for (int i = 0; i < 4; i++) {
      dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(settle[i] * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{ [gHost recheckScreens:udid]; });
    }
    done(error);
  }];
}

// A foldable turns through its motion relay (the guest helper), which is
// quicker than the XCTest runner and works open or closed.
- (void)rotateFoldable:(Pane *)p value:(NSString *)value done:(RunnerDone)done {
  NSArray *clockwise = @[ @"portrait", @"landscape-right", @"upside-down", @"landscape-left" ];
  NSDictionary *turns = @{@"portrait" : @0, @"landscape-right" : @90, @"upside-down" : @180, @"landscape-left" : @270};
  NSString *udid = p.udid;
  int now = [self.rotations[udid] intValue];
  NSString *to = value;
  if ([value isEqualToString:@"left"] || [value isEqualToString:@"right"]) {
    to = clockwise[(now / 90 + ([value isEqualToString:@"right"] ? 1 : 3)) % 4];
  }
  if (!turns[to]) {
    done(@"orientation: portrait, upside-down, landscape-left, landscape-right, left or right");
    return;
  }
  [self guest:udid command:[@"orientation " stringByAppendingString:to] done:^(NSString *error) {
    if (!error) {
      gHost.rotations[udid] = turns[to];
      [gHost refreshSoon:udid];
    }
    done(error);
  }];
}

#pragma mark Screenshots

- (void)shot:(Pane *)p pixels:(BOOL)pixels seq:(id)seq to:(Conn *)c {
  IOSurfaceRef s = p.screen ? (__bridge IOSurfaceRef)MSG(p.screen, @"framebufferSurface") : NULL;
  if (!s) {
    [self send:@{@"t" : @"shotResult", @"seq" : seq, @"error" : @"the simulator isn't showing anything yet"} to:c];
    return;
  }
  CIImage *img = [CIImage imageWithIOSurface:s];
  // In points: what an agent reads doesn't need the Retina pixels. A
  // screenshot someone saves keeps them.
  if (!pixels) {
    double k = 1 / (p.scale ?: 1);
    img = [img imageByApplyingTransform:CGAffineTransformMakeScale(k, k)];
  }
  if (p.turn) {
    // As it's showing (CIImage's y runs up, so clockwise is negative).
    img = [img imageByApplyingTransform:CGAffineTransformMakeRotation(-p.turn * M_PI / 180)];
    img = [img imageByApplyingTransform:CGAffineTransformMakeTranslation(-img.extent.origin.x, -img.extent.origin.y)];
  }
  NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCIImage:img];
  NSData *png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
  [self send:@{@"t" : @"shotResult", @"seq" : seq, @"png" : [png base64EncodedStringWithOptions:0], @"w" : @(rep.pixelsWide), @"h" : @(rep.pixelsHigh)} to:c];
}

- (NSArray *)deviceList {
  NSMutableArray *out = [NSMutableArray array];
  for (id d in allDevices()) {
    if (![[d valueForKey:@"available"] boolValue]) continue;
    id rt = [d valueForKey:@"runtime"];
    [out addObject:@{@"udid" : [[d valueForKey:@"UDID"] UUIDString], @"name" : [d valueForKey:@"name"] ?: @"",
                     @"runtime" : [rt valueForKey:@"name"] ?: @"", @"booted" : @(booted(d))}];
  }
  return out;
}

#pragma mark Video

- (Session *)session:(NSString *)sid {
  for (Pane *p in self.panes.allValues) {
    for (Session *s in p.sessions) {
      if ([s.sid isEqualToString:sid]) return s;
    }
  }
  return nil;
}

- (void)stopSession:(Session *)s {
  [s.encoder invalidate];
  [s.pane.sessions removeObject:s];
}

// A new frame on the device: encode it for this viewer, at most fps times a
// second; one that comes too soon is encoded when its turn comes, so the
// last frame of a burst is never lost.
- (void)frameFor:(Session *)s {
  s.dirty = YES;
  if (s.scheduled || s.held) return;
  CFTimeInterval interval = 1.0 / (s.encoder.fps ?: 60), wait = s.last + interval - CACurrentMediaTime();
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

- (void)encode:(Session *)s {
  Pane *p = s.pane;
  IOSurfaceRef surf = p.screen ? (__bridge IOSurfaceRef)MSG(p.screen, @"framebufferSurface") : NULL;
  if (!surf) return;
  s.dirty = NO;
  s.last = CACurrentMediaTime();
  [s.encoder encodeSurface:surf];
}

- (void)sendFrame:(NSData *)avcc info:(HammockFrameInfo)info session:(Session *)s {
  Conn *c = s.conn;
  if (!c || ![self.conns containsObject:c]) return;
  BOOL lost = s.lost;
  BOOL admit = HammockAdmitFrame(&lost, info.keyframe, c.out.length, s.encoder);
  s.lost = lost;
  if (!admit) return;
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
    Pane *p = self.panes[m[@"id"] ?: @""];
    if (!p) return;
    Session *old = [self session:sid];
    if (old) [self stopSession:old];
    Session *s = [Session new];
    s.sid = sid;
    s.pane = p;
    s.conn = c;
    __weak Session *ws = s;
    s.encoder = [[HammockEncoder alloc] initWithBitrate:[m[@"bitrate"] intValue] ?: 4000000 fps:[m[@"fps"] intValue] ?: 60
                                           maxPixels:[m[@"maxPixels"] intValue]
                                              output:^(NSData *avcc, HammockFrameInfo info) {
      if (ws) [gHost sendFrame:avcc info:info session:ws];
    }];
    s.encoder.rotation = p.turn;
    if (!p.sessions) p.sessions = [NSMutableArray array];
    [p.sessions addObject:s];
    [self encode:s];  // the first frame (a keyframe) straight away
    return;
  }
  Session *s = [self session:sid];
  if (!s) return;
  if ([t isEqualToString:@"rate"]) {
    if (m[@"bitrate"]) s.encoder.bitrate = [m[@"bitrate"] intValue];
    if (m[@"fps"]) s.encoder.fps = [m[@"fps"] intValue];
  } else if ([t isEqualToString:@"keyframe"]) {
    // Encoded now even if the screen is still: the viewer has nothing.
    [s.encoder requestKeyframe];
    if (!s.held) [self encode:s];
    else s.dirty = YES;
  } else if ([t isEqualToString:@"refresh"]) {
    // After a loss: a frame from an LTR the viewer has (or a keyframe).
    [s.encoder requestRefresh];
    if (!s.held) [self encode:s];
    else s.dirty = YES;
  } else if ([t isEqualToString:@"ltr"]) {
    [s.encoder acknowledgeLTR:m[@"tokens"]];
  } else if ([t isEqualToString:@"hold"]) {
    s.held = [m[@"on"] boolValue];
    if (!s.held && s.dirty) [self frameFor:s];
  } else if ([t isEqualToString:@"stop"]) {
    [self stopSession:s];
  }
}

#pragma mark Messages

- (void)handle:(NSDictionary *)m from:(Conn *)c {
  NSString *t = m[@"t"];
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
  // Answered once everything sent before it on this connection is done
  // (lines are handled in order): a close's teardown, before the daemon
  // shuts the device down.
  if ([t isEqualToString:@"barrier"]) {
    [self send:@{@"t" : @"barrierResult", @"seq" : m[@"seq"] ?: @0} to:c];
    return;
  }
  if ([t isEqualToString:@"devices"]) {
    [self send:@{@"t" : @"devicesResult", @"seq" : m[@"seq"] ?: @0, @"devices" : [self deviceList]} to:c];
    return;
  }
  NSString *pid = m[@"id"] ?: @"";
  if ([t isEqualToString:@"open"]) {
    [self open:pid udid:m[@"udid"] ?: @""];
    return;
  }
  Pane *p = self.panes[pid];
  if ([t isEqualToString:@"shot"]) {
    if (p) [self shot:p pixels:[m[@"pixels"] boolValue] seq:m[@"seq"] ?: @0 to:c];
    else [self send:@{@"t" : @"shotResult", @"seq" : m[@"seq"] ?: @0, @"error" : @"no such simulator pane"} to:c];
    return;
  }
  if ([t isEqualToString:@"copy"]) {
    if (p) [self copy:p seq:m[@"seq"] ?: @0 to:c];
    else [self send:@{@"t" : @"copyResult", @"seq" : m[@"seq"] ?: @0, @"text" : @""} to:c];
    return;
  }
  if ([t isEqualToString:@"rotate"] || [t isEqualToString:@"gesture"] || [t isEqualToString:@"hinge"]) {
    id seq = m[@"seq"] ?: @0;
    if (!p) {
      [self send:@{@"t" : @"runnerResult", @"seq" : seq, @"error" : @"no such simulator pane"} to:c];
      return;
    }
    __weak Conn *wc = c;
    RunnerDone reply = ^(NSString *error) {
      Conn *c = wc;
      if (!c) return;
      NSMutableDictionary *r = [@{@"t" : @"runnerResult", @"seq" : seq} mutableCopy];
      if (error) r[@"error"] = error;
      [gHost send:r to:c];
    };
    if ([t isEqualToString:@"hinge"]) {
      if (!p.foldable) reply(@"this simulator doesn't fold");
      else [self hinge:p angle:fmin(fmax([m[@"angle"] doubleValue], 0), 180) ms:[m[@"ms"] doubleValue] done:reply];
      return;
    }
    if ([t isEqualToString:@"rotate"] && p.foldable) {
      [self rotateFoldable:p value:m[@"value"] ?: @"" done:reply];
      return;
    }
    NSDictionary *cmd = @{@"cmd" : @"orientation", @"value" : m[@"value"] ?: @""};
    if ([t isEqualToString:@"gesture"]) {
      // Fingers as they're showing, mapped onto the device's portrait.
      int turn = [self rotationOf:p];
      NSMutableArray *paths = [NSMutableArray array];
      for (NSArray *path in [m[@"paths"] isKindOfClass:NSArray.class] ? m[@"paths"] : @[]) {
        NSMutableArray *out = [NSMutableArray array];
        for (NSDictionary *s in [path isKindOfClass:NSArray.class] ? path : @[]) {
          CGPoint q = unturn(CGPointMake([s[@"x"] doubleValue], [s[@"y"] doubleValue]), turn);
          [out addObject:@{@"x" : @(q.x), @"y" : @(q.y), @"t" : s[@"t"] ?: @0}];
        }
        [paths addObject:out];
      }
      cmd = @{@"cmd" : @"gesture", @"paths" : paths};
    }
    [self runner:p.udid command:cmd done:reply];
    return;
  }
  if (!p) return;
  if ([t isEqualToString:@"close"]) {
    [self teardown:p];
  } else if ([t isEqualToString:@"attach"]) {
    p.viewer = c;
    [self sendLayer:p];
    [self send:[self stateOf:p] to:c];
  } else if ([t isEqualToString:@"detach"]) {
    if (p.viewer == c) p.viewer = nil;
  } else if ([t isEqualToString:@"touch"]) {
    [self touch:p phase:m[@"phase"] x:[m[@"x"] doubleValue] y:[m[@"y"] doubleValue]];
  } else if ([t isEqualToString:@"button"]) {
    [self button:p name:m[@"name"]];
  } else if ([t isEqualToString:@"text"]) {
    [self type:p text:m[@"text"] ?: @""];
  } else if ([t isEqualToString:@"key"]) {
    [self key:p usage:[m[@"usage"] unsignedIntValue] shift:[m[@"shift"] boolValue] cmd:[m[@"cmd"] boolValue]];
  } else if ([t isEqualToString:@"paste"]) {
    [self paste:p text:m[@"text"] ?: @""];
  }
}

@end

int main(int argc, char **argv) {
  @autoreleasepool {
    NSString *socketPath = nil;
    for (int i = 1; i < argc; i++) {
      if (!strncmp(argv[i], "--socket=", 9)) socketPath = @(argv[i] + 9);
    }
    if (!socketPath) {
      fprintf(stderr, "usage: Hammock Simulator --socket=PATH\n");
      return 2;
    }
    // Only Apple's own frameworks are loaded (library validation is off).
    NSString *cs = @"/Library/Developer/PrivateFrameworks/CoreSimulator.framework";
    if (!signedByApple(cs) || !dlopen([cs stringByAppendingPathComponent:@"CoreSimulator"].fileSystemRepresentation, RTLD_NOW)) {
      fprintf(stderr, "simulator-host: CoreSimulator not found, or not signed by Apple (install Xcode)\n");
      return 1;
    }
    NSString *skDir = [developerDir() stringByAppendingPathComponent:@"../SharedFrameworks/SimulatorKit.framework"];
    void *sk = NULL;
    if (signedByApple(skDir)) {
      sk = dlopen([skDir stringByAppendingPathComponent:@"SimulatorKit"].fileSystemRepresentation, RTLD_NOW);
    } else {
      fprintf(stderr, "simulator-host: SimulatorKit at %s isn't signed by Apple; not loading it\n", skDir.fileSystemRepresentation);
    }
    indigoMouse = (MouseFn)(sk ? dlsym(sk, "IndigoHIDMessageForMouseNSEvent") : NULL);
    indigoButton = (ButtonFn)(sk ? dlsym(sk, "IndigoHIDMessageForButton") : NULL);
    indigoKey = (KeyFn)(sk ? dlsym(sk, "IndigoHIDMessageForKeyboardArbitrary") : NULL);
    if (!indigoMouse || !indigoButton || !indigoKey) fprintf(stderr, "simulator-host: SimulatorKit input unavailable: %s\n", dlerror());

    NSApplication *app = NSApplication.sharedApplication;
    app.activationPolicy = NSApplicationActivationPolicyProhibited;
    gHost = [Host new];
    gHost.socketPath = socketPath;
    gHost.panes = [NSMutableDictionary dictionary];
    gHost.runners = [NSMutableDictionary dictionary];
    gHost.rotations = [NSMutableDictionary dictionary];
    gHost.hinges = [NSMutableDictionary dictionary];
    gHost.guests = [NSMutableDictionary dictionary];
    gHost.conns = [NSMutableArray array];
    struct stat st;
    gHost.built = stat(argv[0], &st) == 0 ? st.st_mtime : 0;
    [gHost listen];
    [app run];
  }
  return 0;
}
