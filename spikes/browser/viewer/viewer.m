// Spike viewer: stands in for a Hammock.app browser pane.
//
// Shows the browser host's Chromium layer through CALayerHost (zero-copy,
// composited by WindowServer) and forwards raw input to the host as CGEvent
// bytes. Quitting the viewer leaves the host (and the page) running; the next
// launch reattaches.
//
// HMK_SPIKE_SCRIPT=1 drives a scripted input sequence (click, typing, dead
// key, copy, trackpad scroll with momentum) and prints timings, so the path
// can be checked without a screen.
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>
#include <spawn.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

@interface CALayerHost : CALayer
@property uint32_t contextId;
@end

extern char **environ;
static double nowMs(void) { return [NSDate date].timeIntervalSince1970 * 1000.0; }
static NSString *supportDir(void) {
  return [NSHomeDirectory() stringByAppendingPathComponent:@"Library/Application Support/Hammock/browser-spike"];
}

static NSCursor *cursorFor(int type) {
  switch (type) {
    case 1: return NSCursor.crosshairCursor;
    case 2: return NSCursor.pointingHandCursor;
    case 3: return NSCursor.IBeamCursor;
    case 6: case 13: case 15: case 18: return NSCursor.resizeLeftRightCursor;
    case 7: case 10: case 14: case 19: return NSCursor.resizeUpDownCursor;
    case 30: return NSCursor.IBeamCursorForVerticalLayout;
    case 32: return NSCursor.contextualMenuCursor;
    case 33: return NSCursor.dragLinkCursor;
    case 35: case 38: case 46: return NSCursor.operationNotAllowedCursor;
    case 36: case 48: return NSCursor.dragCopyCursor;
    case 41: return NSCursor.openHandCursor;
    case 42: return NSCursor.closedHandCursor;
    default: return NSCursor.arrowCursor;
  }
}

#pragma mark - Pane

@class Conn;
@interface BrowserPane : NSView <NSTextInputClient>
@property(strong) NSMutableArray<NSDictionary *> *ops;  // text input calls recorded during keyDown
@property NSRange hostSel, hostMarked;
@property NSRect hostCaret;
@property(weak) Conn *conn;
@property(strong) CALayer *container;
@property(strong) CALayerHost *remote;
@property(strong) NSCursor *cursor;
- (void)showContext:(uint32_t)ctx;
- (void)sendGeometry;
- (void)keyChanged;
- (void)forward:(NSEvent *)e at:(NSPoint)p;
@end

static NSString *argURL(void) {
  for (NSString *a in NSProcessInfo.processInfo.arguments) {
    if ([a hasPrefix:@"--url="]) return [a substringFromIndex:6];
  }
  return nil;
}

@interface Conn : NSObject
@property int fd;
@property(strong) dispatch_source_t src;
@property(strong) NSMutableData *buf;
@property(weak) BrowserPane *pane;
@property(copy) void (^onMessage)(NSDictionary *);
@property BOOL spawned;
- (void)send:(NSDictionary *)m;
- (void)connect;
@end

@implementation Conn
- (NSString *)socketPath { return [supportDir() stringByAppendingPathComponent:@"host.sock"]; }

- (BOOL)tryConnect {
  int fd = socket(AF_UNIX, SOCK_STREAM, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  strlcpy(a.sun_path, self.socketPath.fileSystemRepresentation, sizeof a.sun_path);
  if (connect(fd, (struct sockaddr *)&a, sizeof a)) {
    close(fd);
    return NO;
  }
  self.fd = fd;
  self.buf = [NSMutableData data];
  self.src = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, fd, 0, dispatch_get_main_queue());
  dispatch_source_set_event_handler(self.src, ^{ [self readable]; });
  dispatch_resume(self.src);
  return YES;
}

- (void)spawnHost {
  NSString *host = NSProcessInfo.processInfo.environment[@"HMK_BROWSER_HOST"];
  if (!host) {
    host = [[NSBundle.mainBundle.bundlePath stringByDeletingLastPathComponent]
        stringByAppendingPathComponent:@"BrowserHost.app/Contents/MacOS/BrowserHost"];
  }
  NSString *url = argURL() ?: NSProcessInfo.processInfo.environment[@"HMK_SPIKE_URL"];
  if (!url) {
    NSString *page = [[[NSBundle.mainBundle.bundlePath stringByDeletingLastPathComponent] stringByDeletingLastPathComponent]
        stringByAppendingPathComponent:@"testpage/index.html"];
    url = [NSURL fileURLWithPath:page].absoluteString;
  }
  [NSFileManager.defaultManager createDirectoryAtPath:supportDir() withIntermediateDirectories:YES attributes:nil error:nil];
  NSString *log = [supportDir() stringByAppendingPathComponent:@"host.log"];
  posix_spawn_file_actions_t fa;
  posix_spawn_file_actions_init(&fa);
  posix_spawn_file_actions_addopen(&fa, 0, "/dev/null", O_RDONLY, 0);
  posix_spawn_file_actions_addopen(&fa, 1, log.fileSystemRepresentation, O_WRONLY | O_CREAT | O_APPEND, 0644);
  posix_spawn_file_actions_adddup2(&fa, 1, 2);
  posix_spawnattr_t at;
  posix_spawnattr_init(&at);
  posix_spawnattr_setflags(&at, POSIX_SPAWN_SETSID);  // outlive the viewer
  NSString *urlArg = [@"--url=" stringByAppendingString:url];
  char *argv[] = {(char *)host.fileSystemRepresentation, (char *)urlArg.UTF8String, NULL};
  pid_t pid;
  int err = posix_spawn(&pid, argv[0], &fa, &at, argv, environ);
  posix_spawn_file_actions_destroy(&fa);
  posix_spawnattr_destroy(&at);
  printf("VIEWER:spawned host pid=%d err=%d\n", pid, err);
  fflush(stdout);
  self.spawned = YES;
}

- (void)connect {
  if ([self tryConnect]) {
    printf("VIEWER:connected spawned=%d\n", self.spawned);
    fflush(stdout);
    [self.pane sendGeometry];
    [self.pane keyChanged];
    if (!self.spawned && argURL()) [self send:@{@"t" : @"nav", @"url" : argURL()}];
    return;
  }
  if (!self.spawned) [self spawnHost];
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 200 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{ [self connect]; });
}

- (void)readable {
  char b[65536];
  ssize_t n = read(self.fd, b, sizeof b);
  if (n <= 0) {
    dispatch_source_cancel(self.src);
    close(self.fd);
    self.fd = -1;
    self.spawned = NO;  // host died: start another
    [self connect];
    return;
  }
  [self.buf appendBytes:b length:n];
  const char *p = self.buf.bytes;
  NSUInteger start = 0;
  for (NSUInteger i = 0; i < self.buf.length; i++) {
    if (p[i] != '\n') continue;
    NSDictionary *m = [NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:p + start length:i - start] options:0 error:nil];
    start = i + 1;
    if (m && self.onMessage) self.onMessage(m);
  }
  [self.buf replaceBytesInRange:NSMakeRange(0, start) withBytes:NULL length:0];
}

- (void)send:(NSDictionary *)m {
  if (self.fd < 0) return;
  NSMutableData *d = [[NSJSONSerialization dataWithJSONObject:m options:0 error:nil] mutableCopy];
  [d appendBytes:"\n" length:1];
  write(self.fd, d.bytes, d.length);
}
@end

@implementation BrowserPane

- (instancetype)initWithFrame:(NSRect)r {
  if ((self = [super initWithFrame:r])) {
    self.wantsLayer = YES;
    self.layer.backgroundColor = NSColor.textBackgroundColor.CGColor;
    // Mirror Chromium's own structure: a geometry-flipped container holding
    // the CALayerHost at the origin (the remote tree is in points).
    self.container = [CALayer layer];
    self.container.geometryFlipped = YES;
    self.container.anchorPoint = CGPointZero;
    self.container.frame = self.bounds;
    self.container.autoresizingMask = kCALayerWidthSizable | kCALayerHeightSizable;
    [self.layer addSublayer:self.container];
    self.cursor = NSCursor.arrowCursor;
    [self addTrackingArea:[[NSTrackingArea alloc] initWithRect:NSZeroRect
                                                       options:NSTrackingMouseMoved | NSTrackingMouseEnteredAndExited |
                                                               NSTrackingCursorUpdate | NSTrackingActiveInKeyWindow |
                                                               NSTrackingInVisibleRect
                                                         owner:self userInfo:nil]];
  }
  return self;
}

// Chromium may move to a new GPU context (surface recreated, GPU process
// restart). The new host goes on top, transparent until Chromium draws into
// it, and the old one stays underneath briefly, so the swap never shows a
// blank frame.
- (void)showContext:(uint32_t)ctx {
  if (self.remote.contextId == ctx) return;
  CALayerHost *old = self.remote;
  [CATransaction begin];
  [CATransaction setDisableActions:YES];
  self.remote = [CALayerHost layer];
  self.remote.contextId = ctx;
  self.remote.anchorPoint = CGPointZero;
  self.remote.position = CGPointZero;
  [self.container addSublayer:self.remote];
  [CATransaction commit];
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 250 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
    [CATransaction begin];
    [CATransaction setDisableActions:YES];
    [old removeFromSuperlayer];
    [CATransaction commit];
  });
}

- (BOOL)acceptsFirstResponder { return YES; }
- (BOOL)acceptsFirstMouse:(NSEvent *)e { return YES; }
- (BOOL)becomeFirstResponder { [self sendFocus:YES]; return YES; }
- (BOOL)resignFirstResponder { [self sendFocus:NO]; return YES; }
- (void)sendFocus:(BOOL)on { [self.conn send:@{@"t" : @"focus", @"on" : @(on && (self.window.isKeyWindow || getenv("HMK_SPIKE_SCRIPT")))}]; }

- (void)viewDidMoveToWindow {
  [NSNotificationCenter.defaultCenter removeObserver:self];
  if (!self.window) return;
  for (NSString *n in @[ NSWindowDidMoveNotification, NSWindowDidResizeNotification, NSWindowDidChangeScreenNotification ]) {
    [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(sendGeometry) name:n object:self.window];
  }
  for (NSString *n in @[ NSWindowDidBecomeKeyNotification, NSWindowDidResignKeyNotification ]) {
    [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(keyChanged) name:n object:self.window];
  }
}
- (void)keyChanged { [self sendFocus:self.window.firstResponder == self]; }
- (void)setFrameSize:(NSSize)s { [super setFrameSize:s]; [self sendGeometry]; }

// The host keeps its transparent window exactly behind this pane so popups,
// menus and IME windows it opens land in the right place.
- (void)sendGeometry {
  if (!self.window) return;
  NSRect r = [self.window convertRectToScreen:[self convertRect:self.bounds toView:nil]];
  [self.conn send:@{@"t" : @"geom", @"x" : @(r.origin.x), @"y" : @(r.origin.y), @"w" : @(r.size.width),
                    @"h" : @(r.size.height), @"wid" : @(self.window.windowNumber)}];
}

- (void)forward:(NSEvent *)e at:(NSPoint)p { [self forward:e at:p ops:nil]; }
- (void)forward:(NSEvent *)e at:(NSPoint)p ops:(NSArray *)ops {
  CGEventRef cg = e.CGEvent;
  if (!cg) return;
  NSData *d = CFBridgingRelease(CGEventCreateData(kCFAllocatorDefault, cg));
  double t = nowMs();
  [self.conn send:@{@"t" : @"ev", @"cg" : [d base64EncodedStringWithOptions:0], @"x" : @(p.x),
                    @"y" : @(self.bounds.size.height - p.y), @"sent" : @(t), @"ops" : ops ?: @[]}];
}
- (void)fwd:(NSEvent *)e { [self forward:e at:[self convertPoint:e.locationInWindow fromView:nil]]; }

- (void)mouseDown:(NSEvent *)e { [self.window makeFirstResponder:self]; [self fwd:e]; }
- (void)mouseUp:(NSEvent *)e { [self fwd:e]; }
- (void)mouseDragged:(NSEvent *)e { [self fwd:e]; }
- (void)mouseMoved:(NSEvent *)e { [self fwd:e]; [self.cursor set]; }
- (void)mouseEntered:(NSEvent *)e { [self fwd:e]; }
- (void)mouseExited:(NSEvent *)e { [self fwd:e]; }
- (void)rightMouseDown:(NSEvent *)e { [self.window makeFirstResponder:self]; [self fwd:e]; }
- (void)rightMouseUp:(NSEvent *)e { [self fwd:e]; }
- (void)rightMouseDragged:(NSEvent *)e { [self fwd:e]; }
- (void)otherMouseDown:(NSEvent *)e { [self fwd:e]; }
- (void)otherMouseUp:(NSEvent *)e { [self fwd:e]; }
- (void)otherMouseDragged:(NSEvent *)e { [self fwd:e]; }
- (void)scrollWheel:(NSEvent *)e { [self fwd:e]; }
- (void)magnifyWithEvent:(NSEvent *)e { [self fwd:e]; }
- (void)smartMagnifyWithEvent:(NSEvent *)e { [self fwd:e]; }
- (void)rotateWithEvent:(NSEvent *)e { [self fwd:e]; }
- (void)cursorUpdate:(NSEvent *)e { [self.cursor set]; }
// Text input runs here, in the active app, where dead keys and IME work. The
// NSTextInputClient calls it makes are recorded and sent with the key; the
// host replays them inside Chromium's own keyDown.
- (void)keyDown:(NSEvent *)e {
  self.ops = [NSMutableArray array];
  [self interpretKeyEvents:@[ e ]];
  NSArray *ops = self.ops;
  self.ops = nil;
  if (getenv("HMK_SPIKE_SCRIPT") || getenv("HMK_SPIKE_OPS")) {
    printf("OPS:%s active=%d key=%d\n", [[NSString alloc] initWithData:[NSJSONSerialization dataWithJSONObject:ops options:0 error:nil] encoding:NSUTF8StringEncoding].UTF8String, NSApp.isActive, self.window.isKeyWindow);
    fflush(stdout);
  }
  [self forward:e at:NSZeroPoint ops:ops];
}

static NSArray *rangeJSON(NSRange r) {
  return @[ @(r.location == NSNotFound ? -1 : (long long)r.location), @(r.length) ];
}
// Copied: the text system passes a mutable string it reuses and clears.
static NSString *plain(id s) { return [([s isKindOfClass:NSAttributedString.class] ? [s string] : s) copy]; }

- (void)record:(NSDictionary *)op {
  if (self.ops) {
    [self.ops addObject:op];
  } else {
    [self.conn send:@{@"t" : @"ti", @"ops" : @[ op ]}];  // outside a key press
  }
}
- (void)insertText:(id)s replacementRange:(NSRange)r {
  self.hostMarked = NSMakeRange(NSNotFound, 0);
  [self record:@{@"op" : @"insert", @"s" : plain(s), @"r" : rangeJSON(r)}];
}
- (void)setMarkedText:(id)s selectedRange:(NSRange)sel replacementRange:(NSRange)r {
  NSString *p = plain(s);
  NSUInteger base = self.hostMarked.location != NSNotFound ? self.hostMarked.location : self.hostSel.location;
  self.hostMarked = p.length ? NSMakeRange(base, p.length) : NSMakeRange(NSNotFound, 0);
  [self record:@{@"op" : @"mark", @"s" : p, @"sel" : rangeJSON(sel), @"r" : rangeJSON(r)}];
}
- (void)unmarkText {
  self.hostMarked = NSMakeRange(NSNotFound, 0);
  [self record:@{@"op" : @"unmark"}];
}
- (void)doCommandBySelector:(SEL)sel { [self record:@{@"op" : @"cmd", @"sel" : NSStringFromSelector(sel)}]; }
- (BOOL)hasMarkedText { return self.hostMarked.location != NSNotFound && self.hostMarked.length > 0; }
- (NSRange)markedRange { return self.hasMarkedText ? self.hostMarked : NSMakeRange(NSNotFound, 0); }
- (NSRange)selectedRange { return self.hostSel; }
- (NSArray<NSAttributedStringKey> *)validAttributesForMarkedText { return @[]; }
- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)r actualRange:(NSRangePointer)a { return nil; }
- (NSUInteger)characterIndexForPoint:(NSPoint)p { return NSNotFound; }
- (NSRect)firstRectForCharacterRange:(NSRange)r actualRange:(NSRangePointer)a {
  if (NSIsEmptyRect(self.hostCaret) && self.hostCaret.size.height == 0) {
    return [self.window convertRectToScreen:[self convertRect:NSMakeRect(0, self.bounds.size.height - 20, 1, 20) toView:nil]];
  }
  return self.hostCaret;
}
- (void)hostTextState:(NSDictionary *)m {
  NSArray *s = m[@"sel"], *k = m[@"marked"], *r = m[@"rect"];
  self.hostSel = NSMakeRange([s[0] longLongValue] < 0 ? NSNotFound : [s[0] unsignedIntegerValue], [s[1] unsignedIntegerValue]);
  self.hostMarked = NSMakeRange([k[0] longLongValue] < 0 ? NSNotFound : [k[0] unsignedIntegerValue], [k[1] unsignedIntegerValue]);
  self.hostCaret = NSMakeRect([r[0] doubleValue], [r[1] doubleValue], [r[2] doubleValue], [r[3] doubleValue]);
}
- (void)keyUp:(NSEvent *)e { [self fwd:e]; }
- (void)flagsChanged:(NSEvent *)e { [self fwd:e]; }

// Send ⌘-keys to the page first (as Chrome does); the host bounces back any
// the page didn't handle, and those go to our menus. ⌘Q and ⌘R stay ours.
- (BOOL)performKeyEquivalent:(NSEvent *)e {
  if (self.window.firstResponder != self || e.type != NSEventTypeKeyDown) return NO;
  NSString *k = e.charactersIgnoringModifiers.lowercaseString;
  if ((e.modifierFlags & NSEventModifierFlagCommand) && ([k isEqualToString:@"q"] || [k isEqualToString:@"r"])) return NO;
  [self fwd:e];
  return YES;
}
@end

#pragma mark - App

@interface Delegate : NSObject <NSApplicationDelegate>
@property(strong) NSWindow *window;
@property(strong) BrowserPane *pane;
@property(strong) Conn *conn;
@property(strong) NSTextField *status;
@property int hostPid;
@property uint32_t ctx;
@property(strong) NSMutableArray<NSDictionary *> *pageEvents;
@property NSInteger menuChoice;
@end

@implementation Delegate

- (void)applicationDidFinishLaunching:(NSNotification *)n {
  NSMenu *bar = [NSMenu new];
  NSMenuItem *appItem = [bar addItemWithTitle:@"" action:nil keyEquivalent:@""];
  NSMenu *appMenu = [NSMenu new];
  [appMenu addItemWithTitle:@"Quit (page keeps running)" action:@selector(terminate:) keyEquivalent:@"q"];
  appItem.submenu = appMenu;
  NSMenuItem *pageItem = [bar addItemWithTitle:@"Page" action:nil keyEquivalent:@""];
  NSMenu *pageMenu = [[NSMenu alloc] initWithTitle:@"Page"];
  [pageMenu addItemWithTitle:@"Reload" action:@selector(reload:) keyEquivalent:@"r"];
  [pageMenu addItemWithTitle:@"Stop Browser Host" action:@selector(stopHost:) keyEquivalent:@""];
  pageItem.submenu = pageMenu;
  NSApp.mainMenu = bar;

  self.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(160, 160, 1100, 760)
                                            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                                                      NSWindowStyleMaskResizable | NSWindowStyleMaskMiniaturizable |
                                                      NSWindowStyleMaskFullSizeContentView
                                              backing:NSBackingStoreBuffered defer:NO];
  self.window.title = @"Browser pane spike";
  self.window.titlebarAppearsTransparent = YES;
  NSView *root = self.window.contentView;

  // A sidebar stand-in so the pane sits where it would in Hammock.
  NSVisualEffectView *side = [[NSVisualEffectView alloc] initWithFrame:NSMakeRect(0, 0, 220, root.bounds.size.height)];
  side.material = NSVisualEffectMaterialSidebar;
  side.autoresizingMask = NSViewHeightSizable;
  NSTextField *label = [NSTextField wrappingLabelWithString:
      @"Spaces\n\n● Hammock\n    Terminal\n    Browser  ◀\n\nThe pane on the right is Chromium running in a separate process (BrowserHost). Quit this window and relaunch: the page keeps its state."];
  label.frame = NSMakeRect(16, 120, 190, root.bounds.size.height - 180);
  label.autoresizingMask = NSViewMinYMargin;
  label.textColor = NSColor.secondaryLabelColor;
  [side addSubview:label];
  [root addSubview:side];

  self.status = [NSTextField labelWithString:@"connecting…"];
  self.status.font = [NSFont monospacedSystemFontOfSize:11 weight:NSFontWeightRegular];
  self.status.textColor = NSColor.secondaryLabelColor;
  self.status.frame = NSMakeRect(232, 4, 860, 16);
  self.status.autoresizingMask = NSViewWidthSizable;
  [root addSubview:self.status];

  self.pane = [[BrowserPane alloc] initWithFrame:NSMakeRect(220, 24, root.bounds.size.width - 220, root.bounds.size.height - 24 - 28)];
  self.pane.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
  [root addSubview:self.pane];

  self.pageEvents = [NSMutableArray array];
  self.conn = [Conn new];
  self.conn.fd = -1;
  self.conn.pane = self.pane;
  self.pane.conn = self.conn;
  __weak Delegate *weakSelf = self;
  self.conn.onMessage = ^(NSDictionary *m) { [weakSelf handle:m]; };

  [self.window makeKeyAndOrderFront:nil];
  [self.window makeFirstResponder:self.pane];
  [NSApp activateIgnoringOtherApps:YES];
  [self.conn connect];
  if (NSProcessInfo.processInfo.environment[@"HMK_SPIKE_SCRIPT"]) [self runScript];
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)a { return YES; }

- (void)reload:(id)s { [self.conn send:@{@"t" : @"reload"}]; }
- (void)stopHost:(id)s { if (self.hostPid) kill(self.hostPid, SIGTERM); }

- (void)updateStatus {
  self.status.stringValue = [NSString stringWithFormat:@"BrowserHost pid %d · layer %u · %@", self.hostPid, self.ctx,
                                                       self.window.title];
}

- (void)handle:(NSDictionary *)m {
  NSString *t = m[@"t"];
  if ([t isEqualToString:@"hello"]) {
    self.hostPid = [m[@"pid"] intValue];
    printf("VIEWER:host pid=%d\n", self.hostPid);
  } else if ([t isEqualToString:@"layer"]) {
    self.ctx = [m[@"ctx"] unsignedIntValue];
    [self.pane showContext:self.ctx];
    printf("VIEWER:layer ctx=%u\n", self.ctx);
  } else if ([t isEqualToString:@"cursor"]) {
    self.pane.cursor = cursorFor([m[@"type"] intValue]);
    [self.pane.cursor set];
  } else if ([t isEqualToString:@"popup"]) {
    // target=_blank and window.open. Hammock would open a browser tab;
    // the spike just follows the link in this pane.
    [self.conn send:@{@"t" : @"nav", @"url" : m[@"url"]}];
  } else if ([t isEqualToString:@"menu"]) {
    // After this message is handled: the popup tracks in a nested run loop.
    dispatch_async(dispatch_get_main_queue(), ^{ [self showMenu:m]; });
  } else if ([t isEqualToString:@"ti"]) {
    [self.pane hostTextState:m];
  } else if ([t isEqualToString:@"title"]) {
    self.window.title = m[@"title"];
  } else if ([t isEqualToString:@"console"]) {
    NSDictionary *ev = [NSJSONSerialization JSONObjectWithData:[m[@"msg"] dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
    if (ev) [self.pageEvents addObject:ev];
    printf("PAGE:%s\n", [m[@"msg"] UTF8String]);
  } else if ([t isEqualToString:@"unhandled"]) {
    NSData *d = [[NSData alloc] initWithBase64EncodedString:m[@"cg"] options:0];
    CGEventRef cg = CGEventCreateFromData(kCFAllocatorDefault, (__bridge CFDataRef)d);
    if (cg) {
      NSEvent *e = [NSEvent eventWithCGEvent:cg];
      CFRelease(cg);
      printf("VIEWER:unhandled key %s\n", e.charactersIgnoringModifiers.UTF8String);
      [NSApp.mainMenu performKeyEquivalent:e];
    }
  }
  fflush(stdout);
  [self updateStatus];
}

#pragma mark Select popups

// The host can't keep a menu open (it's never the active app), so <select>
// popups are shown here, the way Chrome shows them: an NSPopUpButtonCell over
// the element, with the current item positioned over it.
- (void)showMenu:(NSDictionary *)m {
  NSMenu *menu = [[NSMenu alloc] initWithTitle:@""];
  menu.autoenablesItems = NO;
  menu.font = [NSFont menuFontOfSize:[m[@"font"] doubleValue]];
  NSInteger i = 0;
  for (NSDictionary *it in m[@"items"]) {
    NSMenuItem *item = [it[@"sep"] boolValue] ? [NSMenuItem separatorItem]
                                              : [[NSMenuItem alloc] initWithTitle:it[@"title"] action:@selector(menuPick:) keyEquivalent:@""];
    item.target = self;
    item.tag = i++;
    item.enabled = [it[@"enabled"] boolValue];
    item.hidden = [it[@"hidden"] boolValue];
    item.indentationLevel = [it[@"indent"] integerValue];
    [menu addItem:item];
  }
  self.menuChoice = -1;
  NSPopUpButtonCell *cell = [[NSPopUpButtonCell alloc] initTextCell:@"" pullsDown:NO];
  cell.menu = menu;
  cell.font = menu.font;
  [cell selectItemWithTag:[m[@"index"] integerValue]];
  NSRect b = self.pane.bounds;
  NSRect frame = NSMakeRect([m[@"x"] doubleValue], b.size.height - [m[@"y"] doubleValue] - [m[@"h"] doubleValue],
                            [m[@"w"] doubleValue], [m[@"h"] doubleValue]);
  [cell attachPopUpWithFrame:b inView:self.pane];
  [cell performClickWithFrame:frame inView:self.pane];  // tracks until dismissed
  [cell dismissPopUp];
  [self.conn send:@{@"t" : @"menuResult", @"id" : m[@"id"], @"index" : @(self.menuChoice)}];
}
- (void)menuPick:(NSMenuItem *)item { self.menuChoice = item.tag; }

#pragma mark Script

- (NSPoint)windowPoint:(NSPoint)paneTopLeft {
  NSPoint p = NSMakePoint(paneTopLeft.x, self.pane.bounds.size.height - paneTopLeft.y);
  return [self.pane convertPoint:p toView:nil];
}

- (void)sentMark:(NSString *)what {
  printf("SENT:{\"what\":\"%s\",\"t\":%.3f}\n", what.UTF8String, nowMs());
  fflush(stdout);
}

- (void)click:(NSPoint)tl {
  NSPoint w = [self windowPoint:tl];
  NSEvent *d = [NSEvent mouseEventWithType:NSEventTypeLeftMouseDown location:w modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime
                              windowNumber:self.window.windowNumber context:nil eventNumber:1 clickCount:1 pressure:1];
  NSEvent *u = [NSEvent mouseEventWithType:NSEventTypeLeftMouseUp location:w modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime
                              windowNumber:self.window.windowNumber context:nil eventNumber:2 clickCount:1 pressure:0];
  [self sentMark:@"click"];
  [self.pane mouseDown:d];
  [self.pane mouseUp:u];
}

- (void)key:(unsigned short)code chars:(NSString *)c raw:(NSString *)raw mods:(NSEventModifierFlags)mods {
  NSTimeInterval ts = NSProcessInfo.processInfo.systemUptime;
  NSEvent *d = [NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint modifierFlags:mods timestamp:ts
                            windowNumber:self.window.windowNumber context:nil characters:c charactersIgnoringModifiers:raw
                               isARepeat:NO keyCode:code];
  NSEvent *u = [NSEvent keyEventWithType:NSEventTypeKeyUp location:NSZeroPoint modifierFlags:mods timestamp:ts
                            windowNumber:self.window.windowNumber context:nil characters:c charactersIgnoringModifiers:raw
                               isARepeat:NO keyCode:code];
  [self sentMark:[@"key:" stringByAppendingString:raw]];
  if (mods & NSEventModifierFlagCommand) {
    [self.pane performKeyEquivalent:d];
  } else {
    [self.pane keyDown:d];
  }
  [self.pane keyUp:u];
}

- (void)scrollPhase:(int)phase momentum:(int)momentum dy:(int)dy {
  CGEventRef cg = CGEventCreateScrollWheelEvent2(NULL, kCGScrollEventUnitPixel, 1, dy, 0, 0);
  CGEventSetIntegerValueField(cg, kCGScrollWheelEventIsContinuous, 1);
  CGEventSetIntegerValueField(cg, kCGScrollWheelEventPointDeltaAxis1, dy);
  CGEventSetDoubleValueField(cg, kCGScrollWheelEventFixedPtDeltaAxis1, dy);
  CGEventSetIntegerValueField(cg, kCGScrollWheelEventScrollPhase, phase);
  CGEventSetIntegerValueField(cg, kCGScrollWheelEventMomentumPhase, momentum);
  NSEvent *e = [NSEvent eventWithCGEvent:cg];
  CFRelease(cg);
  [self.pane forward:e at:NSMakePoint(400, self.pane.bounds.size.height - 400)];
}

// A trackpad pinch, built the way the HID system reports one: a gesture
// CGEvent (type 29) carrying a zoom (HID type 8) value and phase.
- (void)pinchPhase:(int)phase by:(double)m {
  CGEventRef cg = CGEventCreate(NULL);
  CGEventSetType(cg, (CGEventType)29);
  CGEventSetIntegerValueField(cg, (CGEventField)110, 8);
  CGEventSetDoubleValueField(cg, (CGEventField)113, m);
  CGEventSetIntegerValueField(cg, (CGEventField)132, phase);
  NSEvent *e = [NSEvent eventWithCGEvent:cg];
  CFRelease(cg);
  printf("PINCH:cgphase=%d type=%lu phase=%lu magnification=%g\n", phase, (unsigned long)e.type, (unsigned long)e.phase, e.type == NSEventTypeMagnify ? e.magnification : 0);
  fflush(stdout);
  [self.pane forward:e at:NSMakePoint(400, self.pane.bounds.size.height - 300)];
}

- (void)after:(double)s do:(dispatch_block_t)b {
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(s * NSEC_PER_SEC)), dispatch_get_main_queue(), b);
}

- (void)runScript {
  // Wait for the page and layer.
  if (!self.ctx) {
    [self after:0.2 do:^{ [self runScript]; }];
    return;
  }
  [self after:1.0 do:^{
    [self click:NSMakePoint(200, 33)];
  }];
  [self after:1.5 do:^{
    [self key:4 chars:@"h" raw:@"h" mods:0];
    [self key:34 chars:@"i" raw:@"i" mods:0];
    [self key:14 chars:@"" raw:@"e" mods:NSEventModifierFlagOption];  // dead key ´
    [self key:14 chars:@"e" raw:@"e" mods:0];
    [self key:51 chars:@"\x7f" raw:@"\x7f" mods:0];  // delete: deleteBackward:
    [self key:14 chars:@"" raw:@"e" mods:NSEventModifierFlagOption];
    [self key:14 chars:@"e" raw:@"e" mods:0];
  }];
  // What the text input system sends for ⌥e e and for a Japanese IME
  // conversion when this app is active (it can't be with the screen locked).
  [self after:1.8 do:^{
    NSRange none = NSMakeRange(NSNotFound, 0);
    [self sentMark:@"ime"];
    [self.pane setMarkedText:@"´" selectedRange:NSMakeRange(1, 0) replacementRange:none];
    [self.pane insertText:@"é" replacementRange:none];
    [self.pane setMarkedText:@"か" selectedRange:NSMakeRange(1, 0) replacementRange:none];
    [self.pane setMarkedText:@"漢字" selectedRange:NSMakeRange(2, 0) replacementRange:none];
    [self.pane insertText:@"漢字" replacementRange:none];
  }];
  [self after:2.0 do:^{
    [self key:0 chars:@"a" raw:@"a" mods:NSEventModifierFlagCommand];
    [self key:8 chars:@"c" raw:@"c" mods:NSEventModifierFlagCommand];
  }];
  // Trackpad-style scroll: a gesture, then OS momentum.
  __block double t0 = 2.5;
  [self after:t0 do:^{ [self sentMark:@"scroll"]; [self scrollPhase:1 momentum:0 dy:0]; }];
  for (int i = 0; i < 10; i++) [self after:t0 + 0.016 * (i + 1) do:^{ [self scrollPhase:2 momentum:0 dy:-24]; }];
  [self after:t0 + 0.2 do:^{ [self scrollPhase:4 momentum:0 dy:0]; }];
  for (int i = 0; i < 20; i++) {
    int dy = -(int)(30 * pow(0.85, i)) - 1;
    [self after:t0 + 0.22 + 0.016 * i do:^{ [self scrollPhase:0 momentum:(i == 0 ? 1 : 2) dy:dy]; }];
  }
  [self after:t0 + 0.6 do:^{ [self scrollPhase:0 momentum:3 dy:0]; }];
  [self after:3.2 do:^{
    [self sentMark:@"pinch"];
    [self pinchPhase:1 by:0];
    for (int i = 0; i < 6; i++) [self pinchPhase:2 by:0.08];
    [self pinchPhase:4 by:0];
  }];
  [self after:3.5 do:^{
    [self sentMark:@"resize"];
    NSRect f = self.window.frame;
    f.size.width += 200;
    f.size.height += 100;
    [self.window setFrame:f display:YES];
  }];
  [self after:4.5 do:^{
    printf("VIEWER:script done\n");
    fflush(stdout);
    [NSApp terminate:nil];
  }];
}
@end

int main(int argc, const char **argv) {
  @autoreleasepool {
    NSApplication *app = NSApplication.sharedApplication;
    app.activationPolicy = NSApplicationActivationPolicyRegular;
    Delegate *d = [Delegate new];
    app.delegate = d;
    [app run];
  }
}
