// Spike: browser-host, design B ("hidden window").
//
// Chromium (CEF, windowed/Alloy) runs in a real NSWindow owned by this
// process. The window is fully transparent and kept exactly behind the
// viewer's pane, so Chromium-owned popups (<select>, context menus, IME
// candidates, tooltips) land in the right place on screen. What the user sees
// is Chromium's own GPU-process CAContext, hosted by the viewer through
// CALayerHost: the same remote-layer path Chrome uses to display web content
// in its own windows, with no copies.
//
// Input arrives as raw CGEvent bytes (CGEventCreateData) from the viewer and
// is replayed into Chromium's NSView, so trackpad phases, momentum, precise
// deltas and magnify gestures survive the hop.
//
// Protocol: newline-delimited JSON over a Unix socket (one viewer at a time).
//   viewer -> host: geom, ev, focus, nav, reload, edit, eval
//   host -> viewer: layer, cursor, console, title, unhandled, hello
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>
#include <objc/runtime.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#include <string>

#include "include/cef_app.h"
#include "include/cef_application_mac.h"
#include "include/cef_browser.h"
#include "include/cef_client.h"
#include "include/cef_command_line.h"
#include "include/wrapper/cef_helpers.h"
#include "include/wrapper/cef_library_loader.h"

@interface CALayerHost : CALayer
@property uint32_t contextId;
@end

static double nowMs() { return [NSDate date].timeIntervalSince1970 * 1000.0; }

#pragma mark - Shadow window

// Never shown (alpha 0, ignores the mouse); reports itself key while the
// viewer's pane is focused, so Chromium draws a caret and treats the page as
// active.
@interface ShadowWindow : NSWindow
@property BOOL spoofKey;
@end
@implementation ShadowWindow
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)isKeyWindow { return self.spoofKey; }
@end

// Wraps an NSEvent rebuilt from CGEvent bytes. AppKit has no public
// constructor for scroll or gesture events, and one rebuilt from CGEvent data
// has no window, so its locationInWindow is in screen space. The proxy
// supplies the shadow window and a window-relative location; everything else
// (phases, momentum, precise deltas, magnification, the CGEvent itself) is
// forwarded untouched.
@interface EventProxy : NSProxy
@property(strong) NSEvent *target;
@property(weak) NSWindow *win;
@property NSPoint loc;
@end
@implementation EventProxy
- (NSMethodSignature *)methodSignatureForSelector:(SEL)sel { return [_target methodSignatureForSelector:sel]; }
- (void)forwardInvocation:(NSInvocation *)inv { [inv invokeWithTarget:_target]; }
- (NSPoint)locationInWindow { return _loc; }
- (NSWindow *)window { return _win; }
- (NSInteger)windowNumber { return _win.windowNumber; }
- (BOOL)isKindOfClass:(Class)c { return [_target isKindOfClass:c]; }
@end

#pragma mark - Text input

// The host is never the active app, so the real text input system (dead keys,
// IME, Press-and-Hold) can't run here: it stalls 10-50 ms per key and loses
// dead-key state. The viewer runs interpretKeyEvents itself and sends the
// resulting NSTextInputClient calls with each key; this context replays them
// into Chromium's view from inside its own keyDown, exactly where the real
// context would have made them.
static NSArray<NSDictionary *> *gPendingOps;
static void pushTextState(NSView<NSTextInputClient> *web);

static NSRange rangeFrom(NSArray *a) {
  if (![a isKindOfClass:NSArray.class] || a.count != 2) return NSMakeRange(NSNotFound, 0);
  long long loc = [a[0] longLongValue];
  return NSMakeRange(loc < 0 ? NSNotFound : (NSUInteger)loc, [a[1] unsignedIntegerValue]);
}

static void applyOps(NSView<NSTextInputClient> *web, NSArray<NSDictionary *> *ops) {
  for (NSDictionary *op in ops) {
    NSString *k = op[@"op"];
    if ([k isEqualToString:@"insert"]) {
      [web insertText:op[@"s"] replacementRange:rangeFrom(op[@"r"])];
    } else if ([k isEqualToString:@"mark"]) {
      [web setMarkedText:op[@"s"] selectedRange:rangeFrom(op[@"sel"]) replacementRange:rangeFrom(op[@"r"])];
    } else if ([k isEqualToString:@"unmark"]) {
      [web unmarkText];
    } else if ([k isEqualToString:@"cmd"]) {
      [web doCommandBySelector:NSSelectorFromString(op[@"sel"])];
    }
  }
}

@interface ReplayInputContext : NSTextInputContext
@end
@implementation ReplayInputContext
- (BOOL)handleEvent:(NSEvent *)event {
  NSArray *ops = gPendingOps;
  gPendingOps = nil;
  if (!ops.count) return NO;
  applyOps((NSView<NSTextInputClient> *)self.client, ops);
  return YES;
}
- (void)activate {}
- (void)deactivate {}
- (void)discardMarkedText {}
- (void)invalidateCharacterCoordinates { pushTextState((NSView<NSTextInputClient> *)self.client); }
@end

static id replayInputContext(id self, SEL _cmd) {
  static char key;
  ReplayInputContext *c = objc_getAssociatedObject(self, &key);
  if (!c) {
    c = [[ReplayInputContext alloc] initWithClient:self];
    objc_setAssociatedObject(self, &key, c, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  }
  return c;
}

static void installReplayInputContext(NSView *web) {
  static BOOL done;
  if (done || !web) return;
  done = YES;
  Method m = class_getInstanceMethod(web.class, @selector(inputContext));
  class_replaceMethod(web.class, @selector(inputContext), (IMP)replayInputContext, method_getTypeEncoding(m));
}

#pragma mark - <select> popups

// Chromium shows <select> as a native menu from this process, but a menu
// can't stay open in an app that isn't active: it flashes and closes. So the
// menu is shown by the viewer instead: Chromium's WebMenuRunner is hooked to
// send the items over and wait (pumping the run loop, as menu tracking
// would) for the viewer's choice, which is then applied through the item's
// own action, exactly as if it had been clicked here.
static int gMenuSeq;
static NSNumber *gMenuResult;  // index, or -1 for cancelled
static BOOL gViewerConnected();
static void sendMenu(NSDictionary *msg);

static NSMenu *menuOf(id runner) {
  unsigned n = 0;
  Ivar *ivars = class_copyIvarList([runner class], &n);
  NSMenu *menu = nil;
  for (unsigned i = 0; i < n && !menu; i++) {
    const char *type = ivar_getTypeEncoding(ivars[i]);
    if (type && strstr(type, "NSMenu")) {
      id v = object_getIvar(runner, ivars[i]);
      if ([v isKindOfClass:NSMenu.class]) menu = v;
    }
  }
  free(ivars);
  return menu;
}

static void runMenuInViewer(id self, SEL _cmd, NSView *view, NSRect bounds, int index) {
  NSMenu *menu = menuOf(self);
  if (!menu || !gViewerConnected()) return;  // nothing chosen
  NSMutableArray *items = [NSMutableArray array];
  for (NSMenuItem *it in menu.itemArray) {
    [items addObject:@{@"title" : it.title ?: @"", @"enabled" : @(it.enabled), @"sep" : @(it.separatorItem),
                       @"indent" : @(it.indentationLevel), @"hidden" : @(it.hidden)}];
  }
  // Pane-local, top-left origin (the web view fills the shadow window).
  NSRect r = [view convertRect:bounds toView:nil];
  CGFloat h = view.window.contentView.bounds.size.height;
  int seq = ++gMenuSeq;
  gMenuResult = nil;
  sendMenu(@{@"t" : @"menu", @"id" : @(seq), @"items" : items, @"index" : @(index),
             @"font" : @(menu.font.pointSize ?: 13), @"x" : @(r.origin.x), @"y" : @(h - NSMaxY(r)),
             @"w" : @(r.size.width), @"h" : @(r.size.height)});
  while (!gMenuResult && gViewerConnected()) {
    @autoreleasepool {
      CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, true);
    }
  }
  int chosen = gMenuResult ? gMenuResult.intValue : -1;
  gMenuResult = nil;
  if (chosen >= 0 && chosen < (int)menu.numberOfItems) {
    NSMenuItem *it = [menu itemAtIndex:chosen];
    if (it.action) [NSApp sendAction:it.action to:it.target from:it];
  }
}

static void installMenuHook(void) {
  Class cls = NSClassFromString(@"WebMenuRunner");
  SEL sel = NSSelectorFromString(@"runMenuInView:withBounds:initialIndex:");
  Method m = cls ? class_getInstanceMethod(cls, sel) : NULL;
  if (!m) {
    fprintf(stderr, "browser-host: WebMenuRunner not found; <select> popups will misbehave\n");
    return;
  }
  method_setImplementation(m, (IMP)runMenuInViewer);
}

#pragma mark - Host state

class Client;

@interface Host : NSObject
@property(strong) ShadowWindow *window;
@property(strong) NSString *socketPath;
@property(strong) NSString *url;
@property int listenFd;
@property int clientFd;
@property(strong) dispatch_source_t acceptSrc;
@property(strong) dispatch_source_t readSrc;
@property(strong) NSMutableData *inbuf;
@property uint32_t lastCtx;
@property double lastScale;
@property(weak) NSView *mouseTarget;
@property int64_t eventsIn;
- (void)send:(NSDictionary *)msg;
- (void)browserCreated;
@end

static Host *gHost;
static CefRefPtr<CefBrowser> gBrowser;

#pragma mark - CEF client

class Client : public CefClient,
               public CefLifeSpanHandler,
               public CefDisplayHandler,
               public CefKeyboardHandler {
 public:
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  CefRefPtr<CefDisplayHandler> GetDisplayHandler() override { return this; }
  CefRefPtr<CefKeyboardHandler> GetKeyboardHandler() override { return this; }

  void OnAfterCreated(CefRefPtr<CefBrowser> browser) override {
    gBrowser = browser;
    [gHost browserCreated];
  }
  // Never open a window from the host: it would belong to this background
  // process. The viewer decides (Hammock: a new browser tab).
  bool OnBeforePopup(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>, int, const CefString& target_url, const CefString&,
                     WindowOpenDisposition, bool user_gesture, const CefPopupFeatures&, CefWindowInfo&,
                     CefRefPtr<CefClient>&, CefBrowserSettings&, CefRefPtr<CefDictionaryValue>&, bool*) override {
    [gHost send:@{@"t" : @"popup", @"url" : @(target_url.ToString().c_str()), @"gesture" : @(user_gesture)}];
    return true;
  }
  bool DoClose(CefRefPtr<CefBrowser>) override { return false; }
  void OnBeforeClose(CefRefPtr<CefBrowser>) override {
    gBrowser = nullptr;
    CefQuitMessageLoop();
  }

  bool OnCursorChange(CefRefPtr<CefBrowser>, CefCursorHandle, cef_cursor_type_t type,
                      const CefCursorInfo&) override {
    [gHost send:@{@"t" : @"cursor", @"type" : @(type)}];
    return true;  // the viewer owns the real cursor
  }
  bool OnConsoleMessage(CefRefPtr<CefBrowser>, cef_log_severity_t, const CefString& message,
                        const CefString&, int) override {
    NSString* m = [NSString stringWithUTF8String:message.ToString().c_str()];
    if ([m hasPrefix:@"HAMMOCK:"]) {
      double t = nowMs();
      printf("%s\n", m.UTF8String);
      fflush(stdout);
      [gHost send:@{@"t" : @"console", @"msg" : [m substringFromIndex:5], @"at" : @(t)}];
    }
    return false;
  }
  void OnTitleChange(CefRefPtr<CefBrowser>, const CefString& title) override {
    [gHost send:@{@"t" : @"title", @"title" : [NSString stringWithUTF8String:title.ToString().c_str()]}];
  }

  // Keys the page didn't consume. Editing commands run here (the helper has no
  // key window for a menu to target); anything else goes back to the viewer so
  // its own menus (new tab, close, ...) still work while a page is focused.
  bool OnKeyEvent(CefRefPtr<CefBrowser> browser, const CefKeyEvent& event, CefEventHandle os) override {
    if (event.type != KEYEVENT_RAWKEYDOWN || !(event.modifiers & EVENTFLAG_COMMAND_DOWN)) return false;
    auto frame = browser->GetFocusedFrame();
    bool shift = event.modifiers & EVENTFLAG_SHIFT_DOWN;
    switch (event.unmodified_character) {
      case 'c': frame->Copy(); return true;
      case 'v': frame->Paste(); return true;
      case 'x': frame->Cut(); return true;
      case 'a': frame->SelectAll(); return true;
      case 'z': shift ? frame->Redo() : frame->Undo(); return true;
    }
    NSEvent* e = (__bridge NSEvent*)os;
    if (e.CGEvent) {
      NSData* d = CFBridgingRelease(CGEventCreateData(kCFAllocatorDefault, e.CGEvent));
      [gHost send:@{@"t" : @"unhandled", @"cg" : [d base64EncodedStringWithOptions:0]}];
    }
    return true;
  }

 private:
  IMPLEMENT_REFCOUNTING(Client);
};

class App : public CefApp, public CefBrowserProcessHandler {
 public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override { return this; }

  void OnBeforeCommandLineProcessing(const CefString& process_type,
                                     CefRefPtr<CefCommandLine> cl) override {
    // The shadow window is transparent and usually covered; never let Chromium
    // treat it as hidden.
    cl->AppendSwitch("disable-backgrounding-occluded-windows");
    cl->AppendSwitch("disable-renderer-backgrounding");
    cl->AppendSwitch("disable-background-timer-throttling");
    cl->AppendSwitch("use-mock-keychain");
    // No web notifications (and no macOS notification permission prompt).
    cl->AppendSwitch("disable-notifications");
  }

  void OnContextInitialized() override {
    installMenuHook();
    NSRect r = NSMakeRect(0, 0, 900, 700);
    ShadowWindow* w = [[ShadowWindow alloc] initWithContentRect:r
                                                      styleMask:NSWindowStyleMaskBorderless
                                                        backing:NSBackingStoreBuffered
                                                          defer:NO];
    w.releasedWhenClosed = NO;
    w.alphaValue = 0;
    w.ignoresMouseEvents = YES;
    w.hasShadow = NO;
    w.opaque = NO;
    w.collectionBehavior = NSWindowCollectionBehaviorTransient | NSWindowCollectionBehaviorIgnoresCycle |
                           NSWindowCollectionBehaviorFullScreenAuxiliary;
    w.contentView.wantsLayer = YES;
    [w orderBack:nil];
    gHost.window = w;

    CefWindowInfo info;
    info.SetAsChild((__bridge CefWindowHandle)w.contentView, CefRect(0, 0, (int)r.size.width, (int)r.size.height));
    CefBrowserSettings bs;
    CefBrowserHost::CreateBrowser(info, new Client, gHost.url.UTF8String, bs, nullptr, nullptr);
  }

 private:
  IMPLEMENT_REFCOUNTING(App);
};

#pragma mark - Host implementation

static CALayerHost* findLayerHost(CALayer* l) {
  static Class cls = NSClassFromString(@"CALayerHost");
  if ([l isKindOfClass:cls]) return (CALayerHost*)l;
  for (CALayer* s in l.sublayers) {
    if (CALayerHost* h = findLayerHost(s)) return h;
  }
  return nil;
}

static NSView* findView(NSView* v, NSString* name) {
  if ([NSStringFromClass(v.class) containsString:name]) return v;
  for (NSView* s in v.subviews) {
    if (NSView* f = findView(s, name)) return f;
  }
  return nil;
}

@implementation Host

- (void)listen {
  [NSFileManager.defaultManager createDirectoryAtPath:self.socketPath.stringByDeletingLastPathComponent
                          withIntermediateDirectories:YES attributes:nil error:nil];
  unlink(self.socketPath.fileSystemRepresentation);
  int fd = socket(AF_UNIX, SOCK_STREAM, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  if (strlen(self.socketPath.fileSystemRepresentation) >= sizeof a.sun_path) {
    fprintf(stderr, "browser-host: socket path too long: %s\n", self.socketPath.UTF8String);
    exit(1);
  }
  strlcpy(a.sun_path, self.socketPath.fileSystemRepresentation, sizeof a.sun_path);
  if (bind(fd, (struct sockaddr*)&a, sizeof a) || ::listen(fd, 4)) {
    perror("browser-host: listen");
    exit(1);
  }
  self.listenFd = fd;
  self.clientFd = -1;
  self.acceptSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, fd, 0, dispatch_get_main_queue());
  dispatch_source_set_event_handler(self.acceptSrc, ^{
    int c = accept(fd, NULL, NULL);
    if (c < 0) return;
    [self dropClient];
    self.clientFd = c;
    self.inbuf = [NSMutableData data];
    self.readSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, c, 0, dispatch_get_main_queue());
    dispatch_source_set_event_handler(self.readSrc, ^{ [self readClient]; });
    dispatch_resume(self.readSrc);
    self.lastCtx = 0;  // resend the layer to the new viewer
    [self send:@{@"t" : @"hello", @"pid" : @(getpid())}];
    [self pollLayer];
  });
  dispatch_resume(self.acceptSrc);
}

- (void)dropClient {
  if (self.readSrc) dispatch_source_cancel(self.readSrc);
  self.readSrc = nil;
  if (self.clientFd >= 0) close(self.clientFd);
  self.clientFd = -1;
  // No viewer: stop claiming focus, but keep the page running.
  self.window.spoofKey = NO;
  if (gBrowser) gBrowser->GetHost()->SetFocus(false);
}

- (void)readClient {
  char buf[65536];
  ssize_t n = read(self.clientFd, buf, sizeof buf);
  if (n <= 0) {
    [self dropClient];
    return;
  }
  [self.inbuf appendBytes:buf length:n];
  const char* p = (const char*)self.inbuf.bytes;
  NSUInteger len = self.inbuf.length, start = 0;
  for (NSUInteger i = 0; i < len; i++) {
    if (p[i] != '\n') continue;
    NSData* line = [NSData dataWithBytes:p + start length:i - start];
    start = i + 1;
    NSDictionary* m = [NSJSONSerialization JSONObjectWithData:line options:0 error:nil];
    if ([m isKindOfClass:NSDictionary.class]) [self handle:m];
  }
  [self.inbuf replaceBytesInRange:NSMakeRange(0, start) withBytes:NULL length:0];
}

- (void)send:(NSDictionary*)msg {
  if (self.clientFd < 0) return;
  NSMutableData* d = [[NSJSONSerialization dataWithJSONObject:msg options:0 error:nil] mutableCopy];
  [d appendBytes:"\n" length:1];
  if (write(self.clientFd, d.bytes, d.length) < 0) [self dropClient];
}

- (void)browserCreated {
  // Poll for Chromium's remote layer: its context id changes when the GPU
  // process recreates the surface (resize to a new size class, GPU restart).
  [NSTimer scheduledTimerWithTimeInterval:1.0 / 30 repeats:YES block:^(NSTimer*) { [self pollLayer]; }];
}

- (void)pollLayer {
  CALayerHost* h = findLayerHost(self.window.contentView.layer);
  if (!h || !h.contextId) return;
  double scale = h.transform.m11 ? 1.0 / h.transform.m11 : self.window.backingScaleFactor;
  if (h.contextId == self.lastCtx && scale == self.lastScale) return;
  if (getenv("HMK_DUMP_LAYERS")) {
    for (CALayer* l = h; l; l = l.superlayer) {
      CATransform3D x = l.transform;
      printf("LAYER %s frame=%s anchor=%s pos=%s flipped=%d xform=(%g,%g,%g,%g) sub=(%g,%g) scale=%g\n", NSStringFromClass(l.class).UTF8String,
             NSStringFromRect(l.frame).UTF8String, NSStringFromPoint(l.anchorPoint).UTF8String, NSStringFromPoint(l.position).UTF8String,
             l.geometryFlipped, x.m11, x.m22, x.m41, x.m42, l.sublayerTransform.m11, l.sublayerTransform.m22, l.contentsScale);
    }
    fflush(stdout);
  }
  self.lastCtx = h.contextId;
  self.lastScale = scale;
  printf("LAYER:{\"ctx\":%u,\"t\":%.0f}\n", h.contextId, nowMs());
  fflush(stdout);
  [self send:@{@"t" : @"layer", @"ctx" : @(h.contextId), @"scale" : @(scale)}];
}

- (NSView*)webView {
  // Chromium's RenderWidgetHostViewCocoa: the view that takes input.
  NSView* v = findView(self.window.contentView, @"RenderWidgetHostViewCocoa");
  installReplayInputContext(v);
  return v;
}

- (void)handle:(NSDictionary*)m {
  NSString* t = m[@"t"];
  if ([t isEqualToString:@"ev"]) {
    [self replay:m];
  } else if ([t isEqualToString:@"menuResult"]) {
    if ([m[@"id"] intValue] == gMenuSeq) gMenuResult = m[@"index"];
  } else if ([t isEqualToString:@"ti"]) {
    // Text input outside a key press (IME candidate picked with the mouse,
    // Press-and-Hold accent menu, dictation).
    NSView<NSTextInputClient>* web = (NSView<NSTextInputClient>*)[self webView];
    if (web) {
      applyOps(web, m[@"ops"]);
      pushTextState(web);
    }
  } else if ([t isEqualToString:@"geom"]) {
    NSRect r = NSMakeRect([m[@"x"] doubleValue], [m[@"y"] doubleValue], [m[@"w"] doubleValue], [m[@"h"] doubleValue]);
    [self.window setFrame:r display:NO];
    if (gBrowser) {
      NSView* v = (__bridge NSView*)gBrowser->GetHost()->GetWindowHandle();
      v.frame = self.window.contentView.bounds;
    }
    NSInteger below = [m[@"wid"] integerValue];
    if (below) [self.window orderWindow:NSWindowBelow relativeTo:below];
    [self pollLayer];
  } else if ([t isEqualToString:@"focus"]) {
    BOOL on = [m[@"on"] boolValue];
    self.window.spoofKey = on;
    if (on) [self.window makeFirstResponder:[self webView]];
    [NSNotificationCenter.defaultCenter
        postNotificationName:on ? NSWindowDidBecomeKeyNotification : NSWindowDidResignKeyNotification
                      object:self.window];
    if (gBrowser) gBrowser->GetHost()->SetFocus(on);
  } else if ([t isEqualToString:@"nav"]) {
    if (gBrowser) gBrowser->GetMainFrame()->LoadURL([m[@"url"] UTF8String]);
  } else if ([t isEqualToString:@"reload"]) {
    if (gBrowser) gBrowser->Reload();
  } else if ([t isEqualToString:@"eval"]) {
    if (gBrowser) gBrowser->GetMainFrame()->ExecuteJavaScript([m[@"js"] UTF8String], "", 0);
  }
}

// Rebuilds an NSEvent from the viewer's CGEvent bytes and delivers it to
// Chromium's view the way NSWindow would, without going through NSWindow
// (which would try to activate this app on a click).
- (void)replay:(NSDictionary*)m {
  NSView* web = [self webView];
  if (!web) return;
  NSData* d = [[NSData alloc] initWithBase64EncodedString:m[@"cg"] options:0];
  CGEventRef cg = CGEventCreateFromData(kCFAllocatorDefault, (__bridge CFDataRef)d);
  if (!cg) return;
  NSEvent* src = [NSEvent eventWithCGEvent:cg];
  CFRelease(cg);
  if (!src) return;
  self.eventsIn++;

  // Viewer sends pane-local points with a top-left origin; the web view fills
  // the shadow window's content view.
  NSRect b = self.window.contentView.bounds;
  NSPoint loc = NSMakePoint([m[@"x"] doubleValue], b.size.height - [m[@"y"] doubleValue]);
  NSTimeInterval ts = NSProcessInfo.processInfo.systemUptime;
  NSInteger wn = self.window.windowNumber;

  switch (src.type) {
    case NSEventTypeKeyDown:
    case NSEventTypeKeyUp:
    case NSEventTypeFlagsChanged: {
      NSEvent* e;
      if (src.type == NSEventTypeFlagsChanged) {
        e = [NSEvent keyEventWithType:src.type location:NSZeroPoint modifierFlags:src.modifierFlags timestamp:ts
                         windowNumber:wn context:nil characters:@"" charactersIgnoringModifiers:@"" isARepeat:NO
                              keyCode:src.keyCode];
        [web flagsChanged:e];
        break;
      }
      e = [NSEvent keyEventWithType:src.type location:NSZeroPoint modifierFlags:src.modifierFlags timestamp:ts
                       windowNumber:wn context:nil characters:src.characters
        charactersIgnoringModifiers:src.charactersIgnoringModifiers isARepeat:src.ARepeat keyCode:src.keyCode];
      if (self.window.firstResponder != web) [self.window makeFirstResponder:web];
      if (src.type == NSEventTypeKeyUp) {
        [web keyUp:e];
      } else if (src.modifierFlags & NSEventModifierFlagCommand) {
        if (![web performKeyEquivalent:e]) [web keyDown:e];
      } else {
        gPendingOps = m[@"ops"];
        [web keyDown:e];
        gPendingOps = nil;
        pushTextState((NSView<NSTextInputClient>*)web);
      }
      break;
    }
    case NSEventTypeLeftMouseDown:
    case NSEventTypeLeftMouseUp:
    case NSEventTypeLeftMouseDragged:
    case NSEventTypeRightMouseDown:
    case NSEventTypeRightMouseUp:
    case NSEventTypeRightMouseDragged:
    case NSEventTypeOtherMouseDown:
    case NSEventTypeOtherMouseUp:
    case NSEventTypeOtherMouseDragged:
    case NSEventTypeMouseMoved:
    case NSEventTypeMouseEntered:
    case NSEventTypeMouseExited: {
      NSEventType type = src.type;
      // clickCount, pressure and eventNumber throw on tracking (entered/exited)
      // events; only button events carry them.
      BOOL tracking = type == NSEventTypeMouseEntered || type == NSEventTypeMouseExited;
      BOOL button = !tracking && type != NSEventTypeMouseMoved;
      if (tracking) type = NSEventTypeMouseMoved;
      NSEvent* e = [NSEvent mouseEventWithType:type location:loc modifierFlags:src.modifierFlags timestamp:ts
                                  windowNumber:wn context:nil eventNumber:button ? src.eventNumber : 0
                                    clickCount:button ? src.clickCount : 0 pressure:button ? src.pressure : 0];
      NSView* target = web;
      BOOL down = type == NSEventTypeLeftMouseDown || type == NSEventTypeRightMouseDown ||
                  type == NSEventTypeOtherMouseDown;
      if (down) {
        NSView* hit = [self.window.contentView hitTest:loc];
        self.mouseTarget = hit ?: web;
        if (self.window.firstResponder != web) [self.window makeFirstResponder:web];
      }
      if (type != NSEventTypeMouseMoved && self.mouseTarget) target = self.mouseTarget;
      switch (type) {
        case NSEventTypeLeftMouseDown: [target mouseDown:e]; break;
        case NSEventTypeLeftMouseUp: [target mouseUp:e]; break;
        case NSEventTypeLeftMouseDragged: [target mouseDragged:e]; break;
        case NSEventTypeRightMouseDown: [target rightMouseDown:e]; break;
        case NSEventTypeRightMouseUp: [target rightMouseUp:e]; break;
        case NSEventTypeRightMouseDragged: [target rightMouseDragged:e]; break;
        case NSEventTypeOtherMouseDown: [target otherMouseDown:e]; break;
        case NSEventTypeOtherMouseUp: [target otherMouseUp:e]; break;
        case NSEventTypeOtherMouseDragged: [target otherMouseDragged:e]; break;
        default: [web mouseMoved:e]; break;
      }
      break;
    }
    case NSEventTypeScrollWheel:
    case NSEventTypeMagnify:
    case NSEventTypeSmartMagnify:
    case NSEventTypeRotate: {
      EventProxy* p = [EventProxy alloc];
      p.target = src;
      p.win = self.window;
      p.loc = loc;
      NSEvent* e = (NSEvent*)p;
      if (src.type == NSEventTypeScrollWheel) [web scrollWheel:e];
      else if (src.type == NSEventTypeMagnify) [web magnifyWithEvent:e];
      else if (src.type == NSEventTypeSmartMagnify) [web smartMagnifyWithEvent:e];
      else [web rotateWithEvent:e];
      break;
    }
    default:
      break;
  }
  if (m[@"sent"]) {
    // Hop latency (viewer send -> replayed here), for the probe log.
    printf("HOP:{\"kind\":%lu,\"ms\":%.3f}\n", (unsigned long)src.type, nowMs() - [m[@"sent"] doubleValue]);
    fflush(stdout);
  }
}

@end

// Tells the viewer where the caret and composition are, so its text input
// system can answer IME queries (candidate window position, marked range)
// without a round trip. Screen coordinates line up because the shadow window
// sits exactly behind the pane.
static void pushTextState(NSView<NSTextInputClient>* web) {
  if (!web) return;
  NSRange sel = web.selectedRange, marked = web.markedRange;
  NSRect r = [web firstRectForCharacterRange:sel actualRange:NULL];
  auto rng = ^(NSRange x) { return @[ @(x.location == NSNotFound ? -1 : (long long)x.location), @(x.length) ]; };
  [gHost send:@{@"t" : @"ti", @"sel" : rng(sel), @"marked" : rng(marked),
                 @"rect" : @[ @(r.origin.x), @(r.origin.y), @(r.size.width), @(r.size.height) ]}];
}

static BOOL gViewerConnected() { return gHost.clientFd >= 0; }
static void sendMenu(NSDictionary* msg) { [gHost send:msg]; }

#pragma mark - NSApplication

@interface HostApplication : NSApplication <CefAppProtocol> {
  BOOL handlingSendEvent_;
}
@end
@implementation HostApplication
- (BOOL)isHandlingSendEvent { return handlingSendEvent_; }
- (void)setHandlingSendEvent:(BOOL)v { handlingSendEvent_ = v; }
- (void)sendEvent:(NSEvent*)event {
  CefScopedSendingEvent scoper;
  [super sendEvent:event];
}
@end

int main(int argc, char* argv[]) {
  CefScopedLibraryLoader loader;
  if (!loader.LoadInMain()) return 1;
  CefMainArgs args(argc, argv);
  @autoreleasepool {
    [HostApplication sharedApplication];
    NSApp.activationPolicy = NSApplicationActivationPolicyAccessory;

    CefRefPtr<CefCommandLine> cl = CefCommandLine::CreateCommandLine();
    cl->InitFromArgv(argc, argv);
    NSString* support = [NSHomeDirectory() stringByAppendingPathComponent:@"Library/Application Support/Hammock/browser-spike"];
    gHost = [Host new];
    gHost.socketPath = cl->HasSwitch("socket") ? @(cl->GetSwitchValue("socket").ToString().c_str())
                                               : [support stringByAppendingPathComponent:@"host.sock"];
    gHost.url = cl->HasSwitch("url") ? @(cl->GetSwitchValue("url").ToString().c_str()) : @"https://example.com";

    CefSettings settings;
    settings.no_sandbox = true;
    settings.log_severity = LOGSEVERITY_WARNING;
    CefString(&settings.root_cache_path) = [support stringByAppendingPathComponent:@"profile"].UTF8String;
    if (!CefInitialize(args, settings, new App, nullptr)) return CefGetExitCode();
    [gHost listen];
    CefRunMessageLoop();
    CefShutdown();
  }
  return 0;
}
