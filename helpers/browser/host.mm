// Hammock Browser: the process that runs every browser pane.
//
// One host per daemon (Chromium can't share a profile directory between
// processes). Each browser pane is a windowed CEF browser in its own
// transparent "shadow" window, kept exactly behind the pane it's shown in, so
// anything Chromium positions on screen (IME candidates, drag images) lands in
// the right place. What the user sees is Chromium's own GPU-process CAContext,
// hosted by the app through CALayerHost: the remote-layer path Chrome uses
// for its own windows, with no copies (docs/adr/0011).
//
// Input arrives as raw CGEvent bytes (CGEventCreateData) from the app and is
// replayed into Chromium's NSView, so trackpad phases, momentum, precise
// deltas and magnify gestures survive the hop. Native UI that a background
// app can't keep open (<select> popups, context menus, JS dialogs, file
// pickers) is shown by the app instead.
//
// The host outlives the app and the daemon: pages keep running, and a new
// client reattaches. It quits after a while with no clients at all.
//
// Protocol: newline-delimited JSON over a Unix socket, any number of clients.
// Every per-browser message carries the browser id ("id"; the pane id).
//   client -> host: create, close, nav, back, forward, reload, stop, zoom,
//                   attach, detach, geom, focus, ev, ti, menuResult,
//                   contextResult, dialogResult, fileResult, dragEnd, eval, quit,
//                   cdp (a DevTools protocol call), cdpEvents (subscribe)
//   host -> client: hello, state, popup (page: a new-tab link the person's
//                   own click opened, which may go to their browser), download (to everyone);
//                   layer, cursor, ti, unhandled, menu, context, dialog, file, drag,
//                   external (to the client the browser is attached to);
//                   cdpResult (to the caller), cdpEvent (to subscribers);
//                   menu, context and clipboard go to a remote viewer instead
//                   (through the daemon) when its input came last
//
// Remote viewers (ADR-0012): the daemon opens a second connection and sends
// {"t":"media"}; the host answers {"t":"mediaReady"} and from then on takes
// encode {id, sid, bitrate, fps, maxPixels}, rate {sid, bitrate, fps},
// keyframe {sid}, refresh {sid}, ltr {sid, tokens}, hold {sid, on} and stop
// {sid}, and writes binary records (HammockMediaRecord, helpers/media/encoder.h),
// the same as Hammock Simulator's. Pixels come
// from the DevTools screencast (no screen recording permission), decoded and
// re-encoded by HammockEncoder. A remote viewer's input arrives on the main
// connection as rinput {id, op: mouse|wheel|key|text|compose|copy|cut, ...}, in fractions of
// the page (x, y in 0...1), and is sent to the page as DevTools Input events.
//
// DevTools calls go through CEF's in-process channel: no debugging port is
// opened, so only clients of this socket (the daemon) can drive pages.
#import <AppKit/AppKit.h>
#import <CoreVideo/CoreVideo.h>
#import <ImageIO/ImageIO.h>
#import <QuartzCore/QuartzCore.h>

#import "../media/encoder.h"
#include <objc/runtime.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>

#include "../common/peer.h"

#include <map>
#include <string>

#include "include/cef_app.h"
#include "include/cef_application_mac.h"
#include "include/cef_browser.h"
#include "include/cef_client.h"
#include "include/cef_command_line.h"
#include "include/cef_devtools_message_observer.h"
#include "include/cef_parser.h"
#include "include/wrapper/cef_helpers.h"
#include "include/wrapper/cef_library_loader.h"

@interface CALayerHost : CALayer
@property uint32_t contextId;
@end

static NSString *str(const CefString &s) { return [NSString stringWithUTF8String:s.ToString().c_str()] ?: @""; }

#pragma mark - Shadow window

// Never shown (alpha 0, ignores the mouse); reports itself key while its pane
// is focused, so Chromium draws a caret and treats the page as active.
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

#pragma mark - Host state

@class Conn;

@interface Browser : NSObject {
 @public
  CefRefPtr<CefBrowser> cef;
  CefRefPtr<CefRegistration> devtools;
}
@property(copy) NSString *bid;
@property(strong) ShadowWindow *window;
@property(weak) Conn *viewer;  // the client showing this browser
@property uint32_t lastCtx;
@property double lastScale;
@property(weak) NSView *mouseTarget;
@property(copy) NSString *url, *title, *icon, *pendingURL, *errorURL;
@property BOOL loading, back, fwd, closing;
// The next navigation was asked for by a client (address bar, hammock, an
// agent): a link for another app it leads to is offered without a click.
@property BOOL navAsked;
// A DevTools pane's frontend talking to this page, through the daemon
// (dtBridge/dtIn/dtOut): who to send its replies and events to.
@property(weak) Conn *dtConn;
// Remote video: the viewers' sessions, the latest screencast frame (kept
// for a keyframe asked for while the page is still).
// Who gave the latest input: menus the page opens go to them (a remote
// viewer, through the daemon, or the app showing it here).
@property(weak) Conn *remoteViewer;
@property CFTimeInterval remoteAt, localAt;
// The person's latest click or key here, from the app (not a remote viewer
// or an agent), and whether it asked for a new tab itself (⌘, middle button):
// a new-tab link it opens may go to their own browser (browser-new-tab).
@property CFTimeInterval pressAt;
@property BOOL pressAsksTab;
@property(strong) NSMutableArray *sessions;
@property BOOL casting, decoding;
@property int castPixels;     // the size cap the screencast runs at (0: none)
@property NSSize castSize;     // the window size (points) it was started for
@property CFTimeInterval lastAck;  // acks are paced to 60 a second: Chromium captures a frame per ack
@property(strong) NSData *pending;  // the next frame, while one is decoding
@property(nonatomic) CVPixelBufferRef frame;
@property CVPixelBufferPoolRef pool;
@property int poolW, poolH;
// Screencast stats, logged every few seconds while casting.
@property int statFrames;
@property double statDecode, statLag, statBytes;
@property CFTimeInterval statSince;
@end
@implementation Browser
- (void)setFrame:(CVPixelBufferRef)f {
  if (f) CVPixelBufferRetain(f);
  if (_frame) CVPixelBufferRelease(_frame);
  _frame = f;
}
- (void)dealloc {
  self.frame = NULL;
  if (_pool) CVPixelBufferPoolRelease(_pool);
}
@end

@interface Conn : NSObject
@property int fd;
@property(strong) dispatch_source_t src, writeSrc;
@property(strong) NSMutableData *buf, *out;
@property BOOL cdpEvents;  // wants pages' console and network events
@property BOOL media;      // a media connection (remote video)
@end
@implementation Conn
@end

// One remote viewer's video of a browser.
@interface Session : NSObject
@property(copy) NSString *sid;
@property(weak) Browser *browser;
@property(weak) Conn *conn;
@property(strong) HammockEncoder *encoder;
@property int maxPixels;
@property CFTimeInterval last;  // when a frame was last encoded
@property BOOL scheduled, held, dirty;
@property BOOL lost;  // frames dropped for a backlog: waiting for a keyframe (HammockAdmitFrame)
@end
@implementation Session
@end

@interface Host : NSObject
@property(copy) NSString *socketPath;
@property pid_t owner;  // the hmkd that started it (--owner)
@property(strong) NSMutableDictionary<NSString *, Browser *> *browsers;
@property(strong) NSMutableArray<Conn *> *conns;
@property(strong) dispatch_source_t acceptSrc;
@property NSTimeInterval lonelySince;  // when the last client left (0 while any are connected)
@property NSTimeInterval emptySince;   // when the last browser closed (0 while any are open)
@property BOOL quitting;
@property time_t built;                // the executable's mtime at launch
- (void)broadcast:(NSDictionary *)msg;
- (void)send:(NSDictionary *)msg to:(Conn *)c;
- (void)toViewer:(Browser *)b msg:(NSDictionary *)msg;
- (Browser *)browserForWindow:(NSWindow *)w;
- (void)sendState:(Browser *)b;
- (void)pollLayer:(Browser *)b;
- (NSView *)webView:(Browser *)b;
- (void)browserClosed:(Browser *)b;
- (void)screencastFrame:(NSData *)raw of:(Browser *)b;
- (void)devtoolsIn:(NSString *)raw to:(Browser *)b;
- (void)sendClipboard:(NSString *)text of:(Browser *)b;
- (void)startCasting:(Browser *)b;
@end

static Host *gHost;

#pragma mark - DevTools protocol

// Calls in flight, by DevTools message id: @[caller, its seq].
static NSMutableDictionary<NSNumber *, NSArray *> *gCDPCalls;
// The host's own DevTools calls waiting for a result, by message id.
static NSMutableDictionary<NSNumber *, void (^)(NSDictionary *)> *gCDPThen;
static int gCDPNext = 1;

static id jsonOf(const void *p, size_t n) {
  if (!p || !n) return @{};
  return [NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:p length:n] options:0 error:nil] ?: @{};
}

// The events worth keeping for agents: console output, errors, requests.
static void startDevTools(Browser *b);

static NSSet<NSString *> *forwardedEvents() {
  static NSSet *s = [NSSet setWithArray:@[
    @"Runtime.consoleAPICalled", @"Runtime.exceptionThrown", @"Log.entryAdded", @"Network.requestWillBeSent",
    @"Network.responseReceived", @"Network.loadingFailed", @"Network.loadingFinished", @"Page.frameNavigated"
  ]];
  return s;
}

// Async UI shown by the app, by request sequence number.
static int gSeq;
static std::map<int, CefRefPtr<CefJSDialogCallback>> gDialogs;
static std::map<int, CefRefPtr<CefFileDialogCallback>> gFiles;
static std::map<int, CefRefPtr<CefBeforeDownloadCallback>> gDownloads;  // dropped unanswered: canceled
static std::map<int, CefRefPtr<CefRunContextMenuCallback>> gContexts;
static NSMutableDictionary<NSNumber *, NSNumber *> *gMenuResults;  // seq -> index
static NSMutableDictionary<NSNumber *, NSArray *> *gContextCmds;  // seq -> command ids, for answers by index

// Where a menu the page opens should show: the remote viewer whose input
// opened it, else the app showing the page.
static Conn *menuTarget(Browser *b, BOOL *remote) {
  *remote = b.remoteViewer && b.remoteAt > b.localAt;
  return *remote ? b.remoteViewer : b.viewer;
}

#pragma mark - Text input

// The host is never the active app, so the real text input system (dead keys,
// IME, Press-and-Hold) can't run here: it stalls 10-50 ms per key and loses
// dead-key state. The app runs interpretKeyEvents itself and sends the
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

// Tells the app where the caret and composition are, so its text input
// system can answer IME queries (candidate window position, marked range)
// without a round trip. Screen coordinates line up because the shadow window
// sits exactly behind the pane.
static void pushTextState(NSView<NSTextInputClient> *web) {
  Browser *b = [gHost browserForWindow:web.window];
  if (!b || !b.viewer) return;
  NSRange sel = web.selectedRange, marked = web.markedRange;
  NSRect r = [web firstRectForCharacterRange:sel actualRange:NULL];
  auto rng = ^(NSRange x) { return @[ @(x.location == NSNotFound ? -1 : (long long)x.location), @(x.length) ]; };
  [gHost toViewer:b msg:@{@"t" : @"ti", @"id" : b.bid, @"sel" : rng(sel), @"marked" : rng(marked),
                          @"rect" : @[ @(r.origin.x), @(r.origin.y), @(r.size.width), @(r.size.height) ]}];
}

#pragma mark - <select> popups

// Chromium shows <select> as a native menu from this process, but a menu
// can't stay open in an app that isn't active: it flashes and closes. So the
// app shows it instead: Chromium's WebMenuRunner is hooked to send the items
// over and wait (pumping the run loop, as menu tracking would) for the
// choice, which is then applied through the item's own action, exactly as if
// it had been clicked here.
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
  Browser *b = [gHost browserForWindow:view.window];
  BOOL remote = NO;
  Conn *target = b ? menuTarget(b, &remote) : nil;
  if (!menu || !target) return;  // nothing chosen
  NSMutableArray *items = [NSMutableArray array];
  for (NSMenuItem *it in menu.itemArray) {
    [items addObject:@{@"title" : it.title ?: @"", @"enabled" : @(it.enabled), @"sep" : @(it.separatorItem),
                       @"indent" : @(it.indentationLevel), @"hidden" : @(it.hidden)}];
  }
  // Pane-local, top-left origin (the web view fills the shadow window).
  NSRect r = [view convertRect:bounds toView:nil];
  CGFloat h = view.window.contentView.bounds.size.height;
  int seq = ++gSeq;
  CGFloat cw = view.window.contentView.bounds.size.width ?: 1;
  [gHost send:@{@"t" : @"menu", @"id" : b.bid, @"seq" : @(seq), @"items" : items, @"index" : @(index),
                 @"font" : @(menu.font.pointSize ?: 13), @"x" : @(r.origin.x), @"y" : @(h - NSMaxY(r)),
                 @"w" : @(r.size.width), @"h" : @(r.size.height), @"kind" : @"select",
                 @"fx" : @(r.origin.x / cw), @"fy" : @((h - NSMaxY(r)) / (h ?: 1)), @"fw" : @(r.size.width / cw), @"fh" : @(r.size.height / (h ?: 1))}
            to:target];
  while (!gMenuResults[@(seq)] && [gHost.conns containsObject:target] && !b.closing) {
    @autoreleasepool {
      CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, true);
    }
  }
  int chosen = gMenuResults[@(seq)] ? gMenuResults[@(seq)].intValue : -1;
  [gMenuResults removeObjectForKey:@(seq)];
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

#pragma mark - Drags

// A drag the page starts (selected text, a link, an image) would be an AppKit
// drag session of this background process, begun from a replayed event. The
// real mouse-up goes to the app, so macOS never ends it, and every window is
// stuck in it until Esc (#71). Instead the drag's data and image go to the
// app, which runs the drag from the pane with the real mouse and says how it
// ended ("dragEnd"); that is passed to Chromium's drag source, exactly as
// AppKit would have. Drags from a remote viewer's input are refused.
//
// The source (WebContentsViewCocoa) sets one global "drag running" flag in
// draggingSession:willBeginAtPoint: and clears it first thing in
// draggingSession:endedAtPoint:operation:, with a CHECK that it changes (the
// host crashes otherwise). AppKit calls willBegin once a session starts;
// with no session here, beginDrag calls it, so every end has its begin.
// browser id -> @[Chromium's drag source, seq]: the drag waiting on the app.
static NSMutableDictionary<NSString *, NSArray *> *gDragSources;

static void beginDrag(id source) {
  if ([source respondsToSelector:@selector(draggingSession:willBeginAtPoint:)]) {
    [(id<NSDraggingSource>)source draggingSession:(NSDraggingSession *_Nonnull)nil willBeginAtPoint:NSEvent.mouseLocation];
  }
}

static void endDrag(id source, NSPoint screen, NSDragOperation op) {
  if ([source respondsToSelector:@selector(draggingSession:endedAtPoint:operation:)]) {
    [(id<NSDraggingSource>)source draggingSession:(NSDraggingSession *_Nonnull)nil endedAtPoint:screen operation:op];
  }
}

// Ends a browser's drag still waiting on the app (the app went away, the
// pane closed): Chromium otherwise keeps the page in its drag.
static void cancelDrag(NSString *bid) {
  NSArray *pending = gDragSources[bid];
  if (!pending) return;
  [gDragSources removeObjectForKey:bid];
  endDrag(pending[0], NSEvent.mouseLocation, NSDragOperationNone);
}

static NSData *dataOf(id<NSPasteboardWriting> w, NSPasteboardType type) {
  if ([(id)w isKindOfClass:NSPasteboardItem.class]) return [(NSPasteboardItem *)w dataForType:type];
  id v = [w pasteboardPropertyListForType:type];
  if ([v isKindOfClass:NSData.class]) return v;
  if ([v isKindOfClass:NSString.class]) return [v dataUsingEncoding:NSUTF8StringEncoding];
  return v ? [NSPropertyListSerialization dataWithPropertyList:v format:NSPropertyListBinaryFormat_v1_0 options:0 error:nil]
           : nil;
}

static NSString *pngOf(NSImage *img) {
  CGImageRef cg = [img CGImageForProposedRect:NULL context:nil hints:nil];
  if (!cg) return nil;
  NSData *d = [[[NSBitmapImageRep alloc] initWithCGImage:cg] representationUsingType:NSBitmapImageFileTypePNG
                                                                           properties:@{}];
  return [d base64EncodedStringWithOptions:0];
}

static NSDraggingSession *dragInViewer(id self, SEL _cmd, NSArray<NSDraggingItem *> *items, NSEvent *event,
                                       id<NSDraggingSource> source) {
  NSView *view = self;
  Browser *b = [gHost browserForWindow:view.window];
  BOOL remote = NO;
  Conn *target = b ? menuTarget(b, &remote) : nil;
  if (gDragSources.count) {
    // Another drag is still waiting on the app (one mouse: it shouldn't be),
    // and holds the flag. Ended once Chromium has finished starting it,
    // leaving the flag set for the other.
    dispatch_async(dispatch_get_main_queue(), ^{
      endDrag(source, NSEvent.mouseLocation, NSDragOperationNone);
      beginDrag(source);
    });
    return nil;
  }
  beginDrag(source);
  // Refused (ended once Chromium has finished starting it): no app to run
  // it, or a remote viewer's.
  if (!target || remote || !items.count) {
    dispatch_async(dispatch_get_main_queue(), ^{ endDrag(source, NSEvent.mouseLocation, NSDragOperationNone); });
    return nil;
  }
  if (!gDragSources) gDragSources = [NSMutableDictionary dictionary];
  int seq = ++gSeq;
  gDragSources[b.bid] = @[ source, @(seq) ];

  NSPasteboard *pb = [NSPasteboard pasteboardWithName:NSPasteboardNameDrag];
  NSMutableArray *out = [NSMutableArray array];
  for (NSDraggingItem *it in items) {
    id<NSPasteboardWriting> w = it.item;
    NSMutableDictionary *types = [NSMutableDictionary dictionary];
    for (NSPasteboardType type in [w writableTypesForPasteboard:pb]) {
      // File promises can't be kept by the app: left out.
      if ([type.lowercaseString containsString:@"promise"]) continue;
      NSData *d = dataOf(w, type);
      if (d) types[type] = [d base64EncodedStringWithOptions:0];
    }
    if (types.count) [out addObject:types];
  }
  NSMutableDictionary *msg = [@{@"t" : @"drag", @"id" : b.bid, @"seq" : @(seq), @"items" : out} mutableCopy];
  if ([(id)source respondsToSelector:@selector(draggingSession:sourceOperationMaskForDraggingContext:)]) {
    msg[@"mask"] = @([source draggingSession:(NSDraggingSession *_Nonnull)nil
        sourceOperationMaskForDraggingContext:NSDraggingContextOutsideApplication]);
  }
  // The drag image, pane-local with a top-left origin (the web view fills the
  // shadow window).
  NSDraggingItem *first = items.firstObject;
  for (NSDraggingImageComponent *c in first.imageComponents) {
    if (![c.contents isKindOfClass:NSImage.class]) continue;
    NSString *png = pngOf(c.contents);
    if (!png) break;
    NSRect r = [view convertRect:NSOffsetRect(c.frame, first.draggingFrame.origin.x, first.draggingFrame.origin.y)
                          toView:nil];
    CGFloat h = view.window.contentView.bounds.size.height;
    [msg addEntriesFromDictionary:@{@"png" : png, @"x" : @(r.origin.x), @"y" : @(h - NSMaxY(r)),
                                    @"w" : @(r.size.width), @"h" : @(r.size.height)}];
    break;
  }
  [gHost send:msg to:target];
  return nil;
}

static void installDragHook(void) {
  // Overridden on Chromium's view only: NSView's own stays as it is.
  Class cls = NSClassFromString(@"WebContentsViewCocoa");
  SEL sel = @selector(beginDraggingSessionWithItems:event:source:);
  Method m = class_getInstanceMethod(NSView.class, sel);
  if (!cls || !m) {
    fprintf(stderr, "browser-host: WebContentsViewCocoa not found; dragging from pages will get stuck\n");
    return;
  }
  class_replaceMethod(cls, sel, (IMP)dragInViewer, method_getTypeEncoding(m));
}

#pragma mark - CEF client

enum { kOpenLinkInNewTab = MENU_ID_USER_FIRST, kCopyLinkAddress };

// Context menu items a remote viewer may use: none that reads or writes this
// Mac's pasteboard (their Copy, Cut and Copy Link Address go to them instead).
static bool remoteMenuCommand(int cmd) {
  switch (cmd) {
    case MENU_ID_BACK: case MENU_ID_FORWARD: case MENU_ID_RELOAD: case MENU_ID_RELOAD_NOCACHE: case MENU_ID_STOPLOAD:
    case MENU_ID_UNDO: case MENU_ID_REDO: case MENU_ID_CUT: case MENU_ID_COPY: case MENU_ID_DELETE: case MENU_ID_SELECT_ALL:
    case MENU_ID_NO_SPELLING_SUGGESTIONS: case MENU_ID_ADD_TO_DICTIONARY:
    case kOpenLinkInNewTab: case kCopyLinkAddress:
      return true;
  }
  return cmd >= MENU_ID_SPELLCHECK_SUGGESTION_0 && cmd <= MENU_ID_SPELLCHECK_SUGGESTION_LAST;
}

// Where a download is first offered: ~/Downloads, under a name not taken.
static NSString *downloadPath(NSString *name) {
  NSString *dir = [NSHomeDirectory() stringByAppendingPathComponent:@"Downloads"];
  if (!name.length) name = @"download";
  name = [name stringByReplacingOccurrencesOfString:@"/" withString:@"-"];
  NSString *path = [dir stringByAppendingPathComponent:name];
  NSString *base = name.stringByDeletingPathExtension, *ext = name.pathExtension;
  for (int i = 1; [NSFileManager.defaultManager fileExistsAtPath:path]; i++) {
    NSString *n = [NSString stringWithFormat:@"%@ (%d)", base, i];
    path = [dir stringByAppendingPathComponent:ext.length ? [n stringByAppendingPathExtension:ext] : n];
  }
  return path;
}

// A link for another app (obsidian:, zoommtg:, mailto:, …): the app macOS
// would open it with. nil for the web's own schemes, and for a scheme no app
// handles (that one fails as before, on the error page).
static NSURL *externalApp(const CefString &url) {
  NSURL *u = [NSURL URLWithString:str(url)];
  NSString *scheme = u.scheme.lowercaseString;
  if (!scheme.length) return nil;
  static NSSet *web = [NSSet setWithArray:@[ @"http", @"https", @"file", @"data", @"about", @"blob", @"javascript", @"chrome",
                                              @"chrome-error", @"chrome-extension", @"chrome-untrusted", @"devtools",
                                              @"view-source", @"filesystem", @"ws", @"wss" ]];
  if ([web containsObject:scheme]) return nil;
  return [NSWorkspace.sharedWorkspace URLForApplicationToOpenURL:u];
}

class Client : public CefClient,
               public CefLifeSpanHandler,
               public CefDisplayHandler,
               public CefLoadHandler,
               public CefKeyboardHandler,
               public CefJSDialogHandler,
               public CefDialogHandler,
               public CefDownloadHandler,
               public CefContextMenuHandler,
               public CefFindHandler,
               public CefRequestHandler {
 public:
  explicit Client(Browser *b) : b_(b) {}

  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  CefRefPtr<CefDisplayHandler> GetDisplayHandler() override { return this; }
  CefRefPtr<CefLoadHandler> GetLoadHandler() override { return this; }
  CefRefPtr<CefKeyboardHandler> GetKeyboardHandler() override { return this; }
  CefRefPtr<CefJSDialogHandler> GetJSDialogHandler() override { return this; }
  CefRefPtr<CefDialogHandler> GetDialogHandler() override { return this; }
  CefRefPtr<CefDownloadHandler> GetDownloadHandler() override { return this; }
  CefRefPtr<CefContextMenuHandler> GetContextMenuHandler() override { return this; }
  CefRefPtr<CefFindHandler> GetFindHandler() override { return this; }
  CefRefPtr<CefRequestHandler> GetRequestHandler() override { return this; }

  // Another app's link: Chromium can't load it, and opening it straight from
  // here would let any page launch apps. The app showing the page asks first
  // (and opens it), for a link clicked or an address asked for; one a page
  // follows by itself is dropped, as Chrome would without a click.
  bool OnBeforeBrowse(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>, CefRefPtr<CefRequest> request, bool user_gesture,
                      bool) override {
    Browser *b = b_;
    if (!b) return false;
    BOOL asked = b.navAsked;
    b.navAsked = NO;
    return offerExternal(b, request->GetURL(), user_gesture || asked);
  }
  bool offerExternal(Browser *b, const CefString &url, bool wanted) {
    NSURL *app = externalApp(url);
    if (!app || !b.viewer) return false;  // the error page says why
    if (wanted) {
      [gHost toViewer:b msg:@{@"t" : @"external", @"id" : b.bid, @"url" : str(url), @"app" : app.path,
                              @"from" : [NSURL URLWithString:b.url ?: @""].host ?: @""}];
    }
    return true;
  }

  // Find in page (⌘F): how many matches, and which is selected, to the Mac
  // showing the page.
  void OnFindResult(CefRefPtr<CefBrowser>, int, int count, const CefRect &, int active, bool final) override {
    Browser *b = b_;
    if (!b) return;
    [gHost toViewer:b msg:@{@"t" : @"found", @"id" : b.bid, @"count" : @(count), @"active" : @(active), @"final" : @(final)}];
  }

  // Life span

  void OnAfterCreated(CefRefPtr<CefBrowser> browser) override {
    Browser *b = b_;
    if (!b) return;
    b->cef = browser;
    NSView *v = (__bridge NSView *)browser->GetHost()->GetWindowHandle();
    v.frame = b.window.contentView.bounds;
    v.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    startDevTools(b);
    if (b.pendingURL) {
      browser->GetMainFrame()->LoadURL(b.pendingURL.UTF8String);
      b.pendingURL = nil;
    }
    if (b.closing) browser->GetHost()->CloseBrowser(true);
  }
  // Never open a window from the host: it would belong to this background
  // process. Hammock opens it in a browser tab, but only for a popup the
  // person asked for (a click, a key): one a page opens by itself is dropped,
  // as a browser's popup blocker would. A link the page opens in a new tab
  // ("page": target=_blank, window.open without a size) from the person's own
  // click here may go to their browser instead (the daemon decides:
  // browser-new-tab). Not a ⌘- or middle-click (they asked for a tab), a
  // sized popup (a sign-in window: it needs this profile's cookies), or a
  // click from a remote viewer or an agent (it would open on this Mac, where
  // they can't see it).
  bool OnBeforePopup(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>, int, const CefString &target_url, const CefString &,
                     WindowOpenDisposition disposition, bool user_gesture, const CefPopupFeatures &, CefWindowInfo &,
                     CefRefPtr<CefClient> &, CefBrowserSettings &, CefRefPtr<CefDictionaryValue> &, bool *) override {
    if (!user_gesture) return true;
    Browser *b = b_;
    if (!b) return true;
    if (offerExternal(b, target_url, true)) return true;
    CFTimeInterval now = CACurrentMediaTime();
    BOOL page = disposition == CEF_WOD_NEW_FOREGROUND_TAB && !b.pressAsksTab && now - b.pressAt < 1 &&
                b.pressAt >= b.remoteAt;
    [gHost broadcast:@{@"t" : @"popup", @"id" : b.bid, @"url" : str(target_url), @"gesture" : @YES, @"page" : @(page)}];
    return true;
  }
  // The default (performClose: on the browser's window) does nothing for a
  // borderless window with no close button: tear the view down ourselves,
  // which finishes the close (OnBeforeClose).
  bool DoClose(CefRefPtr<CefBrowser> browser) override {
    NSView *v = (__bridge NSView *)browser->GetHost()->GetWindowHandle();
    dispatch_async(dispatch_get_main_queue(), ^{ [v removeFromSuperview]; });
    return true;
  }
  void OnBeforeClose(CefRefPtr<CefBrowser>) override {
    if (Browser *b = b_) [gHost browserClosed:b];
  }

  // Display

  void OnAddressChange(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame> frame, const CefString &url) override {
    Browser *b = b_;
    if (!b || !frame->IsMain()) return;
    NSString *u = str(url);
    // Our error page is a data: URL; keep showing the address that failed.
    if ([u hasPrefix:@"data:text/html"] && b.errorURL) u = b.errorURL;
    else b.errorURL = nil;
    b.url = u;
    [gHost sendState:b];
  }
  void OnTitleChange(CefRefPtr<CefBrowser>, const CefString &title) override {
    Browser *b = b_;
    if (!b) return;
    b.title = str(title);
    [gHost sendState:b];
  }
  void OnFaviconURLChange(CefRefPtr<CefBrowser>, const std::vector<CefString> &urls) override {
    Browser *b = b_;
    if (!b) return;
    b.icon = urls.empty() ? @"" : str(urls[0]);
    [gHost sendState:b];
  }
  bool OnCursorChange(CefRefPtr<CefBrowser>, CefCursorHandle, cef_cursor_type_t type, const CefCursorInfo &) override {
    if (Browser *b = b_) [gHost toViewer:b msg:@{@"t" : @"cursor", @"id" : b.bid, @"type" : @(type)}];
    return true;  // the app owns the real cursor
  }

  // Load

  void OnLoadingStateChange(CefRefPtr<CefBrowser>, bool isLoading, bool canGoBack, bool canGoForward) override {
    Browser *b = b_;
    if (!b) return;
    b.loading = isLoading;
    b.back = canGoBack;
    b.fwd = canGoForward;
    [gHost sendState:b];
  }
  void OnLoadError(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame> frame, ErrorCode code, const CefString &text,
                   const CefString &failed) override {
    Browser *b = b_;
    if (!b || !frame->IsMain() || code == ERR_ABORTED) return;
    b.errorURL = str(failed);
    NSString *esc = [[str(failed) stringByReplacingOccurrencesOfString:@"&" withString:@"&amp;"]
        stringByReplacingOccurrencesOfString:@"<" withString:@"&lt;"];
    NSString *html = [NSString stringWithFormat:
        @"<!doctype html><meta charset=utf-8><title>Can't open this page</title>"
         "<style>:root{color-scheme:light dark}body{font:15px -apple-system,sans-serif;display:grid;place-items:center;"
         "height:90vh;margin:0;color:#888}h1{font-size:22px;font-weight:600;color:CanvasText}div{max-width:520px;padding:24px}"
         "code{word-break:break-all}</style><div><h1>Can't open this page</h1><p><code>%@</code></p><p>%@</p></div>",
        esc, str(text)];
    std::string uri = "data:text/html;base64," + CefURIEncode(CefBase64Encode(html.UTF8String, strlen(html.UTF8String)), false).ToString();
    frame->LoadURL(uri);
  }

  // Keyboard: keys the page didn't consume. Editing and zoom run here (the
  // host has no key window for a menu to target); anything else goes back to
  // the app so its own menus (new tab, close, ...) still work while a page is
  // focused.
  bool OnKeyEvent(CefRefPtr<CefBrowser> browser, const CefKeyEvent &event, CefEventHandle os) override {
    if (event.type != KEYEVENT_RAWKEYDOWN || !(event.modifiers & EVENTFLAG_COMMAND_DOWN)) return false;
    auto frame = browser->GetFocusedFrame();
    auto host = browser->GetHost();
    bool shift = event.modifiers & EVENTFLAG_SHIFT_DOWN;
    switch (event.unmodified_character) {
      case 'c': frame->Copy(); return true;
      case 'v': frame->Paste(); return true;
      case 'x': frame->Cut(); return true;
      case 'a': frame->SelectAll(); return true;
      case 'z': shift ? frame->Redo() : frame->Undo(); return true;
      case '=': case '+': host->SetZoomLevel(host->GetZoomLevel() + 0.5); return true;
      case '-': host->SetZoomLevel(host->GetZoomLevel() - 0.5); return true;
      case '0': host->SetZoomLevel(0); return true;
    }
    NSEvent *e = (__bridge NSEvent *)os;
    Browser *b = b_;
    if (b && e.CGEvent) {
      NSData *d = CFBridgingRelease(CGEventCreateData(kCFAllocatorDefault, e.CGEvent));
      [gHost toViewer:b msg:@{@"t" : @"unhandled", @"id" : b.bid, @"cg" : [d base64EncodedStringWithOptions:0]}];
    }
    return true;
  }

  // JS dialogs (alert, confirm, prompt), shown by the app as a sheet over the
  // pane. With nothing showing the pane they're dismissed.
  bool OnJSDialog(CefRefPtr<CefBrowser>, const CefString &origin, JSDialogType type, const CefString &message,
                  const CefString &prompt, CefRefPtr<CefJSDialogCallback> callback, bool &suppress) override {
    Browser *b = b_;
    if (!b.viewer) {
      suppress = true;
      return false;
    }
    int seq = ++gSeq;
    gDialogs[seq] = callback;
    NSString *kind = type == JSDIALOGTYPE_CONFIRM ? @"confirm" : type == JSDIALOGTYPE_PROMPT ? @"prompt" : @"alert";
    [gHost toViewer:b msg:@{@"t" : @"dialog", @"id" : b.bid, @"seq" : @(seq), @"kind" : kind, @"message" : str(message),
                            @"default" : str(prompt), @"origin" : str(origin)}];
    return true;
  }
  // "Leave site?" never blocks: closing a pane or navigating away just goes.
  bool OnBeforeUnloadDialog(CefRefPtr<CefBrowser>, const CefString &, bool, CefRefPtr<CefJSDialogCallback> cb) override {
    cb->Continue(true, "");
    return true;
  }

  // <input type=file>: an open or save panel shown by the app.
  bool OnFileDialog(CefRefPtr<CefBrowser>, FileDialogMode mode, const CefString &title, const CefString &path,
                    const std::vector<CefString> &filters, const std::vector<CefString> &extensions,
                    const std::vector<CefString> &, CefRefPtr<CefFileDialogCallback> callback) override {
    Browser *b = b_;
    if (!b.viewer) {
      callback->Cancel();
      return true;
    }
    int seq = ++gSeq;
    gFiles[seq] = callback;
    NSMutableArray *exts = [NSMutableArray array];
    for (auto &e : extensions) {
      for (NSString *x in [str(e) componentsSeparatedByString:@";"]) {
        if (x.length) [exts addObject:[x stringByTrimmingCharactersInSet:[NSCharacterSet characterSetWithCharactersInString:@". "]]];
      }
    }
    for (auto &f : filters) {
      NSString *x = str(f);
      if ([x hasPrefix:@"."]) [exts addObject:[x substringFromIndex:1]];
    }
    NSString *m = mode == FILE_DIALOG_OPEN_MULTIPLE ? @"multiple" : mode == FILE_DIALOG_OPEN_FOLDER ? @"folder"
                : mode == FILE_DIALOG_SAVE ? @"save" : @"open";
    [gHost toViewer:b msg:@{@"t" : @"file", @"id" : b.bid, @"seq" : @(seq), @"mode" : m, @"title" : str(title),
                            @"default" : str(path), @"exts" : exts}];
    return true;
  }

  // Downloads

  // Each one asks where to save it (a save panel shown by the app, in
  // ~/Downloads to start with); cancelled, or with no app showing the page
  // to ask, the download doesn't happen.
  bool CanDownload(CefRefPtr<CefBrowser>, const CefString &, const CefString &) override { return true; }
  bool OnBeforeDownload(CefRefPtr<CefBrowser>, CefRefPtr<CefDownloadItem> item, const CefString &name,
                        CefRefPtr<CefBeforeDownloadCallback> callback) override {
    Browser *b = b_;
    if (!b.viewer) return true;  // the callback goes unanswered: canceled
    int seq = ++gSeq;
    gDownloads[seq] = callback;
    NSString *from = [NSURL URLWithString:str(item->GetURL())].host ?: @"";
    [gHost toViewer:b msg:@{@"t" : @"file", @"id" : b.bid, @"seq" : @(seq), @"mode" : @"download", @"title" : @"",
                            @"default" : downloadPath(str(name)), @"from" : from, @"exts" : @[]}];
    return true;
  }
  void OnDownloadUpdated(CefRefPtr<CefBrowser>, CefRefPtr<CefDownloadItem> item, CefRefPtr<CefDownloadItemCallback>) override {
    Browser *b = b_;
    NSString *path = str(item->GetFullPath());
    if (!b || !path.length) return;
    if (item->IsComplete()) {
      // Mark it as downloaded from the web, as browsers do, so Gatekeeper
      // checks it when opened; and bounce the Dock's Downloads stack.
      NSURL *u = [NSURL fileURLWithPath:path];
      NSMutableDictionary *q = [@{(NSString *)kLSQuarantineTypeKey : (NSString *)kLSQuarantineTypeWebDownload,
                                  (NSString *)kLSQuarantineAgentNameKey : @"Hammock"} mutableCopy];
      if (NSURL *src = [NSURL URLWithString:str(item->GetURL())]) q[(NSString *)kLSQuarantineDataURLKey] = src;
      [u setResourceValue:q forKey:NSURLQuarantinePropertiesKey error:nil];
      [NSDistributedNotificationCenter.defaultCenter postNotificationName:@"com.apple.DownloadFileFinished" object:path];
      [gHost broadcast:@{@"t" : @"download", @"id" : b.bid, @"path" : path, @"done" : @YES}];
    } else if (item->IsCanceled() || item->IsInterrupted()) {
      [gHost broadcast:@{@"t" : @"download", @"id" : b.bid, @"path" : path, @"failed" : @YES}];
    }
  }

  // Context menus: Chromium's own items, shown by the app (a menu can't stay
  // open from a background app), plus the link items Chrome has.
  void OnBeforeContextMenu(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>, CefRefPtr<CefContextMenuParams> params,
                           CefRefPtr<CefMenuModel> model) override {
    model->Remove(MENU_ID_PRINT);
    model->Remove(MENU_ID_VIEW_SOURCE);
    while (model->GetCount() && model->GetTypeAt(model->GetCount() - 1) == MENUITEMTYPE_SEPARATOR) {
      model->RemoveAt(model->GetCount() - 1);
    }
    if (!params->GetLinkUrl().empty()) {
      model->InsertItemAt(0, kOpenLinkInNewTab, "Open Link in New Tab");
      model->InsertItemAt(1, kCopyLinkAddress, "Copy Link Address");
      if (model->GetCount() > 2) model->InsertSeparatorAt(2);
    }
  }
  bool RunContextMenu(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>, CefRefPtr<CefContextMenuParams> params,
                      CefRefPtr<CefMenuModel> model, CefRefPtr<CefRunContextMenuCallback> callback) override {
    Browser *b = b_;
    BOOL remote = NO;
    Conn *target = b ? menuTarget(b, &remote) : nil;
    if (!target || !model->GetCount()) {
      callback->Cancel();
      return true;
    }
    NSMutableArray *items = [NSMutableArray array];
    NSMutableArray *cmds = [NSMutableArray array];
    for (size_t i = 0; i < model->GetCount(); i++) {
      auto type = model->GetTypeAt(i);
      if (type == MENUITEMTYPE_SUBMENU || !model->IsVisibleAt(i)) continue;
      if (remote && type != MENUITEMTYPE_SEPARATOR && !remoteMenuCommand(model->GetCommandIdAt(i))) continue;
      // Labels carry Windows-style "&" mnemonics.
      NSString *title = [[str(model->GetLabelAt(i)) stringByReplacingOccurrencesOfString:@"&&" withString:@"\x01"]
          stringByReplacingOccurrencesOfString:@"&" withString:@""];
      [cmds addObject:@(model->GetCommandIdAt(i))];
      [items addObject:@{@"cmd" : @(model->GetCommandIdAt(i)), @"sep" : @(type == MENUITEMTYPE_SEPARATOR),
                         @"title" : [title stringByReplacingOccurrencesOfString:@"\x01" withString:@"&"],
                         @"enabled" : @(model->IsEnabledAt(i)), @"checked" : @(model->IsCheckedAt(i))}];
    }
    int seq = ++gSeq;
    gContexts[seq] = callback;
    gContextCmds[@(seq)] = cmds;
    NSSize size = b.window.contentView.bounds.size;
    [gHost send:@{@"t" : @"context", @"id" : b.bid, @"seq" : @(seq), @"items" : items, @"kind" : @"context",
                   @"x" : @(params->GetXCoord()), @"y" : @(params->GetYCoord()),
                   @"fx" : @(params->GetXCoord() / (size.width ?: 1)), @"fy" : @(params->GetYCoord() / (size.height ?: 1))}
              to:target];
    return true;
  }
  bool OnContextMenuCommand(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame> frame, CefRefPtr<CefContextMenuParams> params,
                            int command, EventFlags) override {
    Browser *b = b_;
    // Opened by a remote viewer: what it copies goes to their clipboard, not
    // this Mac's, and nothing reads this Mac's (Paste isn't offered to them).
    if (b && b.remoteViewer && b.remoteAt > b.localAt) {
      if (!remoteMenuCommand(command)) return true;
      if (command == kCopyLinkAddress) {
        [gHost sendClipboard:str(params->GetLinkUrl()) of:b];
        return true;
      }
      if (command == MENU_ID_COPY || command == MENU_ID_CUT) {
        [gHost sendClipboard:str(params->GetSelectionText()) of:b];
        if (command == MENU_ID_CUT) frame->Delete();
        return true;
      }
    }
    if (command == kOpenLinkInNewTab && b) {
      [gHost broadcast:@{@"t" : @"popup", @"id" : b.bid, @"url" : str(params->GetLinkUrl()), @"gesture" : @YES}];
      return true;
    }
    if (command == kCopyLinkAddress) {
      [NSPasteboard.generalPasteboard clearContents];
      [NSPasteboard.generalPasteboard setString:str(params->GetLinkUrl()) forType:NSPasteboardTypeString];
      return true;
    }
    return false;
  }

 private:
  __weak Browser *b_;
  IMPLEMENT_REFCOUNTING(Client);
};

class DevTools : public CefDevToolsMessageObserver {
 public:
  explicit DevTools(Browser *b) : b_(b) {}

  void OnDevToolsMethodResult(CefRefPtr<CefBrowser>, int mid, bool success, const void *result, size_t size) override;
  void OnDevToolsEvent(CefRefPtr<CefBrowser>, const CefString &method, const void *params, size_t size) override;

 private:
  __weak Browser *b_;
  IMPLEMENT_REFCOUNTING(DevTools);
};

// A DevTools pane's requests, by the id they were sent with: the page and
// the frontend's own id, for its reply.
static NSMutableDictionary<NSNumber *, NSArray *> *gDTCalls;

// Sends a DevTools message; returns its id (0 if it couldn't be sent).
static int sendCDP(Browser *b, NSString *method, NSDictionary *params) {
  if (!b->cef) return 0;
  int mid = gCDPNext++;
  NSData *d = [NSJSONSerialization dataWithJSONObject:@{@"id" : @(mid), @"method" : method ?: @"", @"params" : params ?: @{}}
                                              options:0 error:nil];
  if (!d || !b->cef->GetHost()->SendDevToolsMessage(d.bytes, d.length)) return 0;
  return mid;
}

// From the moment a page exists, keep its console and network activity
// flowing, so an agent that looks later sees what already happened.
static void startDevTools(Browser *b) {
  if (b->devtools || !b->cef) return;
  b->devtools = b->cef->GetHost()->AddDevToolsMessageObserver(new DevTools(b));
  for (NSString *m in @[ @"Runtime.enable", @"Log.enable", @"Network.enable", @"Page.enable" ]) sendCDP(b, m, nil);
  [gHost startCasting:b];  // for viewers that asked before the page existed
}

class App : public CefApp, public CefBrowserProcessHandler {
 public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override { return this; }

  void OnBeforeCommandLineProcessing(const CefString &process_type, CefRefPtr<CefCommandLine> cl) override {
    // Shadow windows are transparent and usually covered; never let Chromium
    // treat them as hidden.
    cl->AppendSwitch("disable-backgrounding-occluded-windows");
    cl->AppendSwitch("disable-renderer-backgrounding");
    cl->AppendSwitch("disable-background-timer-throttling");
    // No keychain prompt for Chromium's cookie encryption key.
    cl->AppendSwitch("use-mock-keychain");
    // No web notifications (and no macOS notification permission prompt).
    cl->AppendSwitch("disable-notifications");
  }

  void OnContextInitialized() override {
    installMenuHook();
    installDragHook();
  }

 private:
  IMPLEMENT_REFCOUNTING(App);
};

#pragma mark - Host implementation

static CALayerHost *findLayerHost(CALayer *l) {
  static Class cls = NSClassFromString(@"CALayerHost");
  if ([l isKindOfClass:cls]) return (CALayerHost *)l;
  for (CALayer *s in l.sublayers) {
    if (CALayerHost *h = findLayerHost(s)) return h;
  }
  return nil;
}

static NSView *findView(NSView *v, NSString *name) {
  if ([NSStringFromClass(v.class) containsString:name]) return v;
  for (NSView *s in v.subviews) {
    if (NSView *f = findView(s, name)) return f;
  }
  return nil;
}

static time_t mtimeOf(const char *path) {
  struct stat st;
  return stat(path, &st) == 0 ? st.st_mtime : 0;
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
  if (bind(fd, (struct sockaddr *)&a, sizeof a) || ::listen(fd, 8)) {
    perror("browser-host: listen");
    exit(1);
  }
  chmod(a.sun_path, 0600);
  self.acceptSrc = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, fd, 0, dispatch_get_main_queue());
  dispatch_source_set_event_handler(self.acceptSrc, ^{
    int c = accept(fd, NULL, NULL);
    if (c < 0) return;
    // Only hmkd and the Hammock app (helpers/common/peer.h): not anything
    // else of yours, an agent in a pane, past hmkd's checks.
    pid_t peer = 0;
    const char *why = "";
    if (!HammockPeerAllowed(c, self.owner, &peer, &why)) {
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
    [self send:@{@"t" : @"hello", @"pid" : @(getpid()), @"ids" : self.browsers.allKeys, @"built" : @(self.built)} to:conn];
    for (Browser *b in self.browsers.allValues) [self sendState:b to:conn];
  });
  dispatch_resume(self.acceptSrc);

  // Layers: Chromium's context id changes when the GPU process recreates the
  // surface (resize to a new size class, GPU restart).
  [NSTimer scheduledTimerWithTimeInterval:1.0 / 30 repeats:YES block:^(NSTimer *) {
    for (Browser *b in self.browsers.allValues) [self pollLayer:b];
  }];
  // With no clients for a while (the daemon is gone for good), or no
  // browsers (the daemon starts another when it needs one), quit.
  self.lonelySince = self.emptySince = NSDate.date.timeIntervalSince1970;
  [NSTimer scheduledTimerWithTimeInterval:30 repeats:YES block:^(NSTimer *) {
    NSTimeInterval now = NSDate.date.timeIntervalSince1970;
    if (self.browsers.count) self.emptySince = 0;
    else if (!self.emptySince) self.emptySince = now;
    if ((self.lonelySince && now - self.lonelySince > 600) || (self.emptySince && now - self.emptySince > 300)) [self quit];
  }];
}

- (void)drop:(Conn *)c {
  if (!c || ![self.conns containsObject:c]) return;
  dispatch_source_cancel(c.src);
  if (c.writeSrc) dispatch_source_cancel(c.writeSrc);
  close(c.fd);
  [self.conns removeObject:c];
  for (Browser *b in self.browsers.allValues) {
    for (Session *s in [b.sessions copy]) {
      if (s.conn == c) [self stopSession:s];
    }
    if (b.viewer == c || !b.viewer) {
      [self unfocus:b];
      // Later: a send inside dragInViewer can drop the connection, and
      // Chromium is still starting that drag.
      NSString *bid = b.bid;
      dispatch_async(dispatch_get_main_queue(), ^{ cancelDrag(bid); });
    }
  }
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
  // Never on a media connection: its stream is binary frame records.
  if (!c || c.media || ![self.conns containsObject:c]) return;
  [c.out appendData:[NSJSONSerialization dataWithJSONObject:msg options:0 error:nil]];
  [c.out appendBytes:"\n" length:1];
  if (c.out.length > 64 << 20) {
    [self drop:c];
    return;
  }
  [self flush:c];
}

- (void)broadcast:(NSDictionary *)msg {
  for (Conn *c in [self.conns copy]) [self send:msg to:c];
}

- (void)toViewer:(Browser *)b msg:(NSDictionary *)msg {
  [self send:msg to:b.viewer];
}

- (Browser *)browserForWindow:(NSWindow *)w {
  for (Browser *b in self.browsers.allValues) {
    if (b.window == w) return b;
  }
  return nil;
}

- (NSDictionary *)stateOf:(Browser *)b {
  return @{@"t" : @"state", @"id" : b.bid, @"url" : b.url ?: @"", @"title" : b.title ?: @"", @"icon" : b.icon ?: @"", @"loading" : @(b.loading),
           @"back" : @(b.back), @"fwd" : @(b.fwd)};
}
- (void)sendState:(Browser *)b { [self broadcast:[self stateOf:b]]; }
- (void)sendState:(Browser *)b to:(Conn *)c { [self send:[self stateOf:b] to:c]; }

- (void)pollLayer:(Browser *)b {
  if (!b.viewer) return;
  CALayerHost *h = findLayerHost(b.window.contentView.layer);
  if (!h || !h.contextId) return;
  double scale = h.transform.m11 ? 1.0 / h.transform.m11 : b.window.backingScaleFactor;
  if (h.contextId == b.lastCtx && scale == b.lastScale) return;
  b.lastCtx = h.contextId;
  b.lastScale = scale;
  [self toViewer:b msg:@{@"t" : @"layer", @"id" : b.bid, @"ctx" : @(h.contextId), @"scale" : @(scale)}];
}

- (NSView *)webView:(Browser *)b {
  // Chromium's RenderWidgetHostViewCocoa: the view that takes input.
  NSView *v = findView(b.window.contentView, @"RenderWidgetHostViewCocoa");
  installReplayInputContext(v);
  return v;
}

- (void)create:(NSString *)bid url:(NSString *)url {
  if (self.browsers[bid] || self.quitting) return;
  Browser *b = [Browser new];
  b.bid = bid;
  b.url = url;
  NSRect r = NSMakeRect(0, 0, 1000, 700);
  ShadowWindow *w = [[ShadowWindow alloc] initWithContentRect:r styleMask:NSWindowStyleMaskBorderless
                                                      backing:NSBackingStoreBuffered defer:NO];
  w.releasedWhenClosed = NO;
  w.alphaValue = 0;
  w.ignoresMouseEvents = YES;
  w.hasShadow = NO;
  w.opaque = NO;
  w.collectionBehavior = NSWindowCollectionBehaviorTransient | NSWindowCollectionBehaviorIgnoresCycle |
                         NSWindowCollectionBehaviorFullScreenAuxiliary | NSWindowCollectionBehaviorCanJoinAllSpaces;
  w.contentView.wantsLayer = YES;
  [w orderBack:nil];
  b.window = w;
  self.browsers[bid] = b;

  CefWindowInfo info;
  info.SetAsChild((__bridge CefWindowHandle)w.contentView, CefRect(0, 0, (int)r.size.width, (int)r.size.height));
  CefBrowserSettings bs;
  CefBrowserHost::CreateBrowser(info, new Client(b), url.length ? url.UTF8String : "about:blank", bs, nullptr, nullptr);
}

- (void)closeBrowser:(Browser *)b {
  if (b.closing) return;
  b.closing = YES;
  if (b->cef) b->cef->GetHost()->CloseBrowser(true);
  // else: closed as soon as it's created (OnAfterCreated)
}

- (void)browserClosed:(Browser *)b {
  for (Session *s in [b.sessions copy]) [self stopSession:s];
  [gDragSources removeObjectForKey:b.bid];
  b->devtools = nullptr;
  b->cef = nullptr;
  [b.window orderOut:nil];
  [b.window close];
  [self.browsers removeObjectForKey:b.bid];
  if (self.quitting && !self.browsers.count) CefQuitMessageLoop();
}

- (void)quit {
  if (self.quitting) return;
  self.quitting = YES;
  if (!self.browsers.count) {
    CefQuitMessageLoop();
    return;
  }
  for (Browser *b in self.browsers.allValues) [self closeBrowser:b];
}

- (void)unfocus:(Browser *)b {
  b.window.spoofKey = NO;
  if (b->cef) b->cef->GetHost()->SetFocus(false);
}

- (void)handle:(NSDictionary *)m from:(Conn *)c {
  NSString *t = m[@"t"];
  if ([t isEqualToString:@"create"]) {
    [self create:m[@"id"] url:m[@"url"]];
    return;
  }
  if ([t isEqualToString:@"quit"]) {
    [self quit];
    return;
  }
  if ([t isEqualToString:@"media"]) {
    [self send:@{@"t" : @"mediaReady"} to:c];
    c.media = YES;
    return;
  }
  if (c.media) {
    [self handleMedia:m from:c];
    return;
  }
  if ([t isEqualToString:@"cdpEvents"]) {
    c.cdpEvents = YES;
    return;
  }
  if ([t isEqualToString:@"cdp"]) {
    Browser *b = self.browsers[m[@"id"] ?: @""];
    int mid = b && !b.closing ? sendCDP(b, m[@"method"], m[@"params"]) : 0;
    if (mid) gCDPCalls[@(mid)] = @[ c, m[@"seq"] ?: @0 ];
    else [self send:@{@"t" : @"cdpResult", @"seq" : m[@"seq"] ?: @0, @"ok" : @NO, @"result" : @{@"message" : @"the page isn't running"}} to:c];
    return;
  }
  if ([t isEqualToString:@"menuResult"]) {
    gMenuResults[m[@"seq"]] = m[@"index"] ?: @(-1);
    return;
  }
  if ([t isEqualToString:@"dialogResult"]) {
    auto it = gDialogs.find([m[@"seq"] intValue]);
    if (it == gDialogs.end()) return;
    it->second->Continue([m[@"ok"] boolValue], [m[@"text"] ?: @"" UTF8String]);
    gDialogs.erase(it);
    return;
  }
  if ([t isEqualToString:@"fileResult"]) {
    auto dl = gDownloads.find([m[@"seq"] intValue]);
    if (dl != gDownloads.end()) {
      NSArray *paths = m[@"paths"];
      if (paths.count) dl->second->Continue([paths[0] fileSystemRepresentation], false);
      gDownloads.erase(dl);  // unanswered: canceled
      return;
    }
    auto it = gFiles.find([m[@"seq"] intValue]);
    if (it == gFiles.end()) return;
    NSArray *paths = m[@"paths"];
    if (paths.count) {
      std::vector<CefString> v;
      for (NSString *p in paths) v.push_back(p.UTF8String);
      it->second->Continue(v);
    } else {
      it->second->Cancel();
    }
    gFiles.erase(it);
    return;
  }
  if ([t isEqualToString:@"contextResult"]) {
    auto it = gContexts.find([m[@"seq"] intValue]);
    if (it == gContexts.end()) return;
    NSArray *cmds = gContextCmds[m[@"seq"]];
    [gContextCmds removeObjectForKey:m[@"seq"]];
    int cmd = [m[@"cmd"] intValue];
    if (m[@"index"]) {  // a remote viewer answers by position
      int i = [m[@"index"] intValue];
      cmd = i >= 0 && i < (int)cmds.count ? [cmds[i] intValue] : -1;
    }
    if (cmd >= 0) it->second->Continue(cmd, EVENTFLAG_NONE);
    else it->second->Cancel();
    gContexts.erase(it);
    return;
  }

  Browser *b = self.browsers[m[@"id"] ?: @""];
  if (!b || b.closing) return;
  CefRefPtr<CefBrowser> cef = b->cef;

  if ([t isEqualToString:@"ev"]) {
    b.localAt = CACurrentMediaTime();
    [self replay:m into:b];
  } else if ([t isEqualToString:@"dragEnd"]) {
    // The app's drag of the page's data ended: at x, y (screen), with op.
    NSArray *pending = gDragSources[b.bid];
    if (pending && [pending[1] isEqual:m[@"seq"]]) {
      [gDragSources removeObjectForKey:b.bid];
      endDrag(pending[0], NSMakePoint([m[@"x"] doubleValue], [m[@"y"] doubleValue]), [m[@"op"] unsignedIntegerValue]);
    }
  } else if ([t isEqualToString:@"ti"]) {
    // Text input outside a key press (IME candidate picked with the mouse,
    // Press-and-Hold accent menu, dictation).
    NSView<NSTextInputClient> *web = (NSView<NSTextInputClient> *)[self webView:b];
    if (web) {
      applyOps(web, m[@"ops"]);
      pushTextState(web);
    }
  } else if ([t isEqualToString:@"attach"]) {
    b.viewer = c;
    b.lastCtx = 0;  // resend the layer to this viewer
    [self pollLayer:b];
  } else if ([t isEqualToString:@"detach"]) {
    if (b.viewer == c) {
      b.viewer = nil;
      [self unfocus:b];
      cancelDrag(b.bid);  // the view that would have run it is gone
    }
  } else if ([t isEqualToString:@"geom"]) {
    if (b.viewer != c) {
      b.viewer = c;
      b.lastCtx = 0;
    }
    NSRect r = NSMakeRect([m[@"x"] doubleValue], [m[@"y"] doubleValue], [m[@"w"] doubleValue], [m[@"h"] doubleValue]);
    [b.window setFrame:r display:NO];
    NSInteger below = [m[@"wid"] integerValue];
    if (below) [b.window orderWindow:NSWindowBelow relativeTo:below];
    if (b.casting && !NSEqualSizes(b.window.contentView.bounds.size, b.castSize)) {
      // Resized while watched: cast at the new size, once the resizing settles.
      [NSObject cancelPreviousPerformRequestsWithTarget:self selector:@selector(recast:) object:b];
      [self performSelector:@selector(recast:) withObject:b afterDelay:0.15];
    }
    [self pollLayer:b];
  } else if ([t isEqualToString:@"focus"]) {
    BOOL on = [m[@"on"] boolValue];
    if (on) b.viewer = c;
    b.window.spoofKey = on;
    if (on) [b.window makeFirstResponder:[self webView:b]];
    [NSNotificationCenter.defaultCenter
        postNotificationName:on ? NSWindowDidBecomeKeyNotification : NSWindowDidResignKeyNotification
                      object:b.window];
    if (cef) cef->GetHost()->SetFocus(on);
  } else if ([t isEqualToString:@"close"]) {
    [self closeBrowser:b];
  } else if ([t isEqualToString:@"nav"]) {
    NSString *url = m[@"url"];
    if (!url.length) return;
    b.navAsked = YES;
    if (cef) cef->GetMainFrame()->LoadURL(url.UTF8String);
    else b.pendingURL = url;
  } else if ([t isEqualToString:@"back"]) {
    if (cef) cef->GoBack();
  } else if ([t isEqualToString:@"forward"]) {
    if (cef) cef->GoForward();
  } else if ([t isEqualToString:@"reload"]) {
    if (cef) cef->Reload();
  } else if ([t isEqualToString:@"stop"]) {
    if (cef) cef->StopLoad();
  } else if ([t isEqualToString:@"find"]) {
    // {text, forward, next}: the first search for text, or the next match.
    NSString *text = m[@"text"];
    if (!cef || ![text isKindOfClass:NSString.class] || !text.length) return;
    cef->GetHost()->Find(text.UTF8String, ![m[@"backward"] boolValue], [m[@"matchCase"] boolValue], [m[@"next"] boolValue]);
  } else if ([t isEqualToString:@"dtBridge"]) {
    b.dtConn = [m[@"on"] boolValue] ? c : nil;
  } else if ([t isEqualToString:@"dtIn"]) {
    [self devtoolsIn:m[@"msg"] to:b];
  } else if ([t isEqualToString:@"stopFind"]) {
    if (cef) cef->GetHost()->StopFinding([m[@"clear"] boolValue]);
  } else if ([t isEqualToString:@"zoom"]) {
    if (!cef) return;
    double d = [m[@"delta"] doubleValue];
    cef->GetHost()->SetZoomLevel(d == 0 ? 0 : cef->GetHost()->GetZoomLevel() + d);
  } else if ([t isEqualToString:@"eval"]) {
    if (cef) cef->GetMainFrame()->ExecuteJavaScript([m[@"js"] UTF8String], "", 0);
  } else if ([t isEqualToString:@"rinput"]) {
    b.remoteViewer = c;
    b.remoteAt = CACurrentMediaTime();
    [self remoteInput:m into:b];
  }
}

#pragma mark Remote video

- (Session *)session:(NSString *)sid {
  for (Browser *b in self.browsers.allValues) {
    for (Session *s in b.sessions) {
      if ([s.sid isEqualToString:sid]) return s;
    }
  }
  return nil;
}

- (void)stopSession:(Session *)s {
  Browser *b = s.browser;
  [s.encoder invalidate];
  [b.sessions removeObject:s];
  if (b && !b.sessions.count && b.casting) {
    b.casting = NO;
    sendCDP(b, @"Page.stopScreencast", nil);
    b.frame = NULL;
  } else if (b.sessions.count) {
    [self startCasting:b];  // resized for who is left
  }
}

- (void)recast:(Browser *)b {
  [self startCasting:b];
}

// Starts the page's screencast, if it isn't running. Chromium sends a frame
// whenever the page changes (and one to start), as JPEG.
- (void)startCasting:(Browser *)b {
  if (!b->cef || !b.sessions.count) return;
  // As big as the biggest viewer wants (0: full size).
  int want = -1;
  for (Session *s in b.sessions) want = s.maxPixels <= 0 ? 0 : (want == 0 ? 0 : MAX(want, s.maxPixels));
  NSSize pt = b.window.contentView.bounds.size;
  if (b.casting && want == b.castPixels && NSEqualSizes(pt, b.castSize)) return;
  if (b.casting) sendCDP(b, @"Page.stopScreencast", nil);
  b.casting = NO;
  b.castPixels = want;
  b.castSize = pt;
  b.statSince = CACurrentMediaTime();
  double scale = b.window.backingScaleFactor ?: 2, w = pt.width * scale, h = pt.height * scale, k = 1;
  if (want > 0 && w * h > want) k = sqrt(want / (w * h));
  b.casting = sendCDP(b, @"Page.startScreencast", @{@"format" : @"jpeg", @"quality" : @85, @"maxWidth" : @(MIN(4096, (int)ceil(w * k))),
                                                    @"maxHeight" : @(MIN(4096, (int)ceil(h * k))), @"everyNthFrame" : @1}) != 0;
}

// The JPEG as a BGRA pixel buffer (IOSurface-backed, for VideoToolbox).
- (CVPixelBufferRef)decode:(NSData *)jpeg for:(Browser *)b CF_RETURNS_RETAINED {
  CGImageSourceRef src = CGImageSourceCreateWithData((__bridge CFDataRef)jpeg, NULL);
  if (!src) return NULL;
  CGImageRef img = CGImageSourceCreateImageAtIndex(src, 0, (__bridge CFDictionaryRef) @{(__bridge NSString *)kCGImageSourceShouldCacheImmediately : @YES});
  CFRelease(src);
  if (!img) return NULL;
  int w = (int)CGImageGetWidth(img), h = (int)CGImageGetHeight(img);
  if (!b.pool || b.poolW != w || b.poolH != h) {
    if (b.pool) CVPixelBufferPoolRelease(b.pool);
    CVPixelBufferPoolRef pool = NULL;
    NSDictionary *attrs = @{(__bridge NSString *)kCVPixelBufferPixelFormatTypeKey : @(kCVPixelFormatType_32BGRA),
                            (__bridge NSString *)kCVPixelBufferWidthKey : @(w),
                            (__bridge NSString *)kCVPixelBufferHeightKey : @(h),
                            (__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey : @{}};
    CVPixelBufferPoolCreate(NULL, NULL, (__bridge CFDictionaryRef)attrs, &pool);
    b.pool = pool;
    b.poolW = w;
    b.poolH = h;
  }
  CVPixelBufferRef pb = NULL;
  if (!b.pool || CVPixelBufferPoolCreatePixelBuffer(NULL, b.pool, &pb) != kCVReturnSuccess) {
    CGImageRelease(img);
    return NULL;
  }
  CVPixelBufferLockBaseAddress(pb, 0);
  static CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
  CGContextRef ctx = CGBitmapContextCreate(CVPixelBufferGetBaseAddress(pb), w, h, 8, CVPixelBufferGetBytesPerRow(pb), cs,
                                           (CGBitmapInfo)kCGImageAlphaNoneSkipFirst | (CGBitmapInfo)kCGBitmapByteOrder32Little);
  if (ctx) {
    CGContextSetBlendMode(ctx, kCGBlendModeCopy);
    CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), img);
    CGContextRelease(ctx);
  }
  CVPixelBufferUnlockBaseAddress(pb, 0);
  CGImageRelease(img);
  return pb;
}

// The screencast's session id, found without parsing the whole message
// (which carries the frame): Chromium writes it last.
static NSNumber *screencastSessionID(NSData *raw) {
  const char *p = (const char *)raw.bytes;
  size_t n = raw.length, from = n > 256 ? n - 256 : 0;
  static const char key[] = "\"sessionId\":";
  const char *hit = NULL;
  for (size_t i = from; i + sizeof key - 1 <= n; i++) {
    if (memcmp(p + i, key, sizeof key - 1) == 0) hit = p + i + sizeof key - 1;
  }
  if (!hit) {
    NSDictionary *m = [NSJSONSerialization JSONObjectWithData:raw options:0 error:nil];
    return [m isKindOfClass:NSDictionary.class] ? m[@"sessionId"] : nil;
  }
  return @(strtol(hit, NULL, 10));
}

static dispatch_queue_t decodeQueue(void) {
  static dispatch_queue_t q = dispatch_queue_create("hammock.screencast", DISPATCH_QUEUE_SERIAL);
  return q;
}

// Acks a screencast frame, at most 60 a second: Chromium captures (and
// JPEG-encodes) the next frame only once acked, so this caps the work on a
// 120 Hz display.
- (void)ack:(NSNumber *)sid of:(Browser *)b {
  // Chromium keeps more than one frame in flight: each ack gets its own slot.
  CFTimeInterval now = CACurrentMediaTime(), at = MAX(now, b.lastAck + 1.0 / 60);
  b.lastAck = at;
  if (at <= now) {
    sendCDP(b, @"Page.screencastFrameAck", @{@"sessionId" : sid});
    return;
  }
  __weak Browser *wb = b;
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)((at - now) * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
    Browser *b = wb;
    // Even if the screencast stopped since: Chromium counts unacked frames
    // across a stop, and a restarted screencast would wait on them.
    if (b && !b.closing) sendCDP(b, @"Page.screencastFrameAck", @{@"sessionId" : sid});
  });
}

// A screencast frame (Page.screencastFrame's params, raw). Acked at once,
// since Chromium sends the next only after an ack; decoded off the main
// thread (every page runs on it), the latest frame winning while one is in
// the decoder.
- (void)screencastFrame:(NSData *)raw of:(Browser *)b {
  if (NSNumber *sid = screencastSessionID(raw)) [self ack:sid of:b];
  if (!b.casting) return;
  if (b.decoding) {
    b.pending = raw;
    return;
  }
  b.decoding = YES;
  __weak Browser *wb = b;
  dispatch_async(decodeQueue(), ^{
    Browser *b = wb;
    if (!b) return;
    NSDictionary *params = [NSJSONSerialization JSONObjectWithData:raw options:0 error:nil];
    NSData *jpeg = [params isKindOfClass:NSDictionary.class]
                       ? [[NSData alloc] initWithBase64EncodedString:params[@"data"] ?: @"" options:0] : nil;
    CFTimeInterval t0 = CACurrentMediaTime();
    CVPixelBufferRef pb = jpeg.length ? [self decode:jpeg for:b] : NULL;
    CFTimeInterval took = CACurrentMediaTime() - t0;
    double ts = [params[@"metadata"][@"timestamp"] doubleValue];
    dispatch_async(dispatch_get_main_queue(), ^{
      b.decoding = NO;
      if (pb && b.casting) {
        b.frame = pb;
        [self countFrame:b decode:took bytes:jpeg.length stamp:ts];
        for (Session *s in b.sessions) [self frameFor:s];
      }
      if (pb) CVPixelBufferRelease(pb);
      NSData *next = b.pending;
      b.pending = nil;
      if (next) [self screencastFrame:next of:b];
    });
  });
}

- (void)countFrame:(Browser *)b decode:(double)took bytes:(NSUInteger)bytes stamp:(double)ts {
  b.statFrames++;
  b.statDecode += took;
  b.statBytes += bytes;
  if (ts > 0) b.statLag += NSDate.date.timeIntervalSince1970 - ts;
  CFTimeInterval now = CACurrentMediaTime();
  if (now - b.statSince < 5) return;
  int n = b.statFrames;
  if (n) {
    fprintf(stderr, "browser-host: screencast %s %dx%d %.1f fps, jpeg %.0f KB, decode %.1f ms, lag %.1f ms\n", b.bid.UTF8String,
            (int)CVPixelBufferGetWidth(b.frame), (int)CVPixelBufferGetHeight(b.frame), n / (now - b.statSince),
            b.statBytes / n / 1024, b.statDecode / n * 1000, b.statLag / n * 1000);
  }
  b.statFrames = 0;
  b.statDecode = b.statLag = b.statBytes = 0;
  b.statSince = now;
}

// A new frame of the page: encode it for this viewer, at most fps times a
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
  CVPixelBufferRef f = s.browser.frame;
  if (!f) return;
  s.dirty = NO;
  s.last = CACurrentMediaTime();
  [s.encoder encodePixelBuffer:f];
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
    Browser *b = self.browsers[m[@"id"] ?: @""];
    if (!b || b.closing) return;
    Session *old = [self session:sid];
    if (old) [self stopSession:old];
    Session *s = [Session new];
    s.sid = sid;
    s.browser = b;
    s.conn = c;
    __weak Session *ws = s;
    s.encoder = [[HammockEncoder alloc] initWithBitrate:[m[@"bitrate"] intValue] ?: 4000000 fps:[m[@"fps"] intValue] ?: 60
                                           maxPixels:[m[@"maxPixels"] intValue]
                                              output:^(NSData *avcc, HammockFrameInfo info) {
      if (ws) [gHost sendFrame:avcc info:info session:ws];
    }];
    if (!b.sessions) b.sessions = [NSMutableArray array];
    s.maxPixels = [m[@"maxPixels"] intValue];
    [b.sessions addObject:s];
    if (b.frame) [self encode:s];  // the first frame (a keyframe) straight away
    [self startCasting:b];
    return;
  }
  Session *s = [self session:sid];
  if (!s) return;
  if ([t isEqualToString:@"rate"]) {
    if (m[@"bitrate"]) s.encoder.bitrate = [m[@"bitrate"] intValue];
    if (m[@"fps"]) s.encoder.fps = [m[@"fps"] intValue];
  } else if ([t isEqualToString:@"keyframe"]) {
    // Encoded now even if the page is still: the viewer has nothing.
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


#pragma mark Remote clipboard

// Text a remote viewer copied, to its own clipboard (through the daemon).
// devtoolsIn sends a DevTools pane's request to its page, under an id of
// ours (the page's channel is shared with the screencast and agents'
// tools), remembering the frontend's for the reply.
- (void)devtoolsIn:(NSString *)raw to:(Browser *)b {
  if (!b->cef || ![raw isKindOfClass:NSString.class]) return;
  NSMutableDictionary *msg = [[NSJSONSerialization JSONObjectWithData:[raw dataUsingEncoding:NSUTF8StringEncoding] options:NSJSONReadingMutableContainers
                                                                 error:nil] mutableCopy];
  if (![msg isKindOfClass:NSMutableDictionary.class] || ![msg[@"method"] isKindOfClass:NSString.class]) return;
  int mid = gCDPNext++;
  if (!gDTCalls) gDTCalls = [NSMutableDictionary dictionary];
  gDTCalls[@(mid)] = @[ b.bid, msg[@"id"] ?: @0 ];
  msg[@"id"] = @(mid);
  NSData *d = [NSJSONSerialization dataWithJSONObject:msg options:0 error:nil];
  if (!d || !b->cef->GetHost()->SendDevToolsMessage(d.bytes, d.length)) [gDTCalls removeObjectForKey:@(mid)];
}

- (void)sendClipboard:(NSString *)text of:(Browser *)b {
  if (text.length && b.remoteViewer) [self send:@{@"t" : @"clipboard", @"id" : b.bid, @"text" : text} to:b.remoteViewer];
}

// ⌘C / ⌘X from a remote viewer: the page's selection (in a text field or
// the document), deleted for a cut.
- (void)copySelection:(Browser *)b cut:(BOOL)cut {
  NSString *js = [NSString stringWithFormat:
      @"(() => { const e = document.activeElement; let s;"
       " if (e && (e.tagName === 'INPUT' || e.tagName === 'TEXTAREA') && e.selectionStart != null)"
       "   s = e.value.slice(e.selectionStart, e.selectionEnd);"
       " else s = String(getSelection());"
       " if (%@ && s) document.execCommand('delete');"
       " return s; })()", cut ? @"true" : @"false"];
  int mid = sendCDP(b, @"Runtime.evaluate", @{@"expression" : js, @"returnByValue" : @YES, @"userGesture" : @YES});
  if (!mid) return;
  __weak Browser *wb = b;
  gCDPThen[@(mid)] = ^(NSDictionary *r) {
    id v = r[@"result"][@"value"];
    if (wb && [v isKindOfClass:NSString.class]) [gHost sendClipboard:v of:wb];
  };
}

// DevTools modifier bits from names.
static int cdpModifiers(id mods) {
  int bits = 0;
  if (![mods isKindOfClass:NSArray.class]) return 0;
  for (id k in mods) {
    if (![k isKindOfClass:NSString.class]) continue;
    if ([k isEqualToString:@"alt"]) bits |= 1;
    else if ([k isEqualToString:@"ctrl"]) bits |= 2;
    else if ([k isEqualToString:@"meta"]) bits |= 4;
    else if ([k isEqualToString:@"shift"]) bits |= 8;
  }
  return bits;
}

// A remote viewer's input, as DevTools Input events. Positions come as
// fractions of the page and become CSS pixels of its viewport.
- (void)remoteInput:(NSDictionary *)m into:(Browser *)b {
  NSString *op = m[@"op"];
  NSSize size = b.window.contentView.bounds.size;
  double zoom = b->cef ? pow(1.2, b->cef->GetHost()->GetZoomLevel()) : 1;
  double x = [m[@"x"] doubleValue] * size.width / zoom, y = [m[@"y"] doubleValue] * size.height / zoom;
  int mods = cdpModifiers(m[@"mods"]);
  if ([op isEqualToString:@"mouse"]) {
    NSString *type = m[@"type"];  // down, move, up
    NSString *cdpType = [type isEqualToString:@"down"] ? @"mousePressed" : [type isEqualToString:@"up"] ? @"mouseReleased" : @"mouseMoved";
    NSString *button = m[@"button"] ?: @"left";
    NSMutableDictionary *p = [@{@"type" : cdpType, @"x" : @(x), @"y" : @(y), @"modifiers" : @(mods)} mutableCopy];
    if (![type isEqualToString:@"move"]) {
      p[@"button"] = button;
      p[@"clickCount"] = m[@"clicks"] ?: @1;
    } else if ([m[@"pressed"] boolValue]) {
      p[@"button"] = button;  // a drag
      p[@"buttons"] = [button isEqualToString:@"right"] ? @2 : @1;
    }
    sendCDP(b, @"Input.dispatchMouseEvent", p);
  } else if ([op isEqualToString:@"wheel"]) {
    sendCDP(b, @"Input.dispatchMouseEvent", @{@"type" : @"mouseWheel", @"x" : @(x), @"y" : @(y), @"deltaX" : @([m[@"dx"] doubleValue] * size.width / zoom),
                                               @"deltaY" : @([m[@"dy"] doubleValue] * size.height / zoom), @"modifiers" : @(mods)});
  } else if ([op isEqualToString:@"key"]) {
    // {type: down|up, key, code, text, vk, mods, commands}
    NSString *text = m[@"text"];
    BOOL down = ![m[@"type"] isEqualToString:@"up"];
    NSMutableDictionary *p = [@{@"type" : down ? (text.length ? @"keyDown" : @"rawKeyDown") : @"keyUp", @"modifiers" : @(mods)} mutableCopy];
    for (NSString *k in @[ @"key", @"code" ]) {
      if (m[k]) p[k] = m[k];
    }
    if (m[@"vk"]) p[@"windowsVirtualKeyCode"] = p[@"nativeVirtualKeyCode"] = m[@"vk"];
    if (down && text.length) p[@"text"] = p[@"unmodifiedText"] = text;
    if (down && [m[@"commands"] isKindOfClass:NSArray.class]) {
      // Never clipboard commands: they'd use this Mac's pasteboard (the daemon checks too).
      NSSet *ok = [NSSet setWithArray:@[ @"selectAll", @"undo", @"redo" ]];
      NSArray *cmds = [m[@"commands"] filteredArrayUsingPredicate:[NSPredicate predicateWithBlock:^BOOL(id c, NSDictionary *_) {
                                        return [c isKindOfClass:NSString.class] && [ok containsObject:c];
                                      }]];
      if (cmds.count) p[@"commands"] = cmds;
    }
    sendCDP(b, @"Input.dispatchKeyEvent", p);
  } else if ([op isEqualToString:@"text"]) {
    NSString *text = m[@"text"];
    if (text.length) sendCDP(b, @"Input.insertText", @{@"text" : text});
  } else if ([op isEqualToString:@"compose"]) {
    // IME marked text, shown in the page until committed (a text op).
    sendCDP(b, @"Input.imeSetComposition", @{@"text" : m[@"text"] ?: @"", @"selectionStart" : m[@"start"] ?: @0,
                                               @"selectionEnd" : m[@"end"] ?: @0});
  } else if ([op isEqualToString:@"copy"] || [op isEqualToString:@"cut"]) {
    [self copySelection:b cut:[op isEqualToString:@"cut"]];
  }
}

// Writes never block the main thread (every page runs on it): what a slow
// client can't take yet is queued, and one that falls far behind is dropped.
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
// start again from a keyframe now, not when the page next changes.
- (void)resumeLost:(Conn *)c {
  for (Browser *b in self.browsers.allValues) {
    for (Session *s in b.sessions) {
      if (s.conn != c || !s.lost || s.held) continue;
      [s.encoder requestKeyframe];
      [self encode:s];
    }
  }
}

// NSEvent modifier flags as CEF's.
static uint32_t cefModifiers(NSEventModifierFlags f) {
  uint32_t m = EVENTFLAG_NONE;
  if (f & NSEventModifierFlagShift) m |= EVENTFLAG_SHIFT_DOWN;
  if (f & NSEventModifierFlagControl) m |= EVENTFLAG_CONTROL_DOWN;
  if (f & NSEventModifierFlagOption) m |= EVENTFLAG_ALT_DOWN;
  if (f & NSEventModifierFlagCommand) m |= EVENTFLAG_COMMAND_DOWN;
  if (f & NSEventModifierFlagCapsLock) m |= EVENTFLAG_CAPS_LOCK_ON;
  return m;
}

// Rebuilds an NSEvent from the app's CGEvent bytes and delivers it to
// Chromium's view the way NSWindow would, without going through NSWindow
// (which would try to activate this app on a click).
- (void)replay:(NSDictionary *)m into:(Browser *)b {
  NSView *web = [self webView:b];
  if (!web) return;
  NSData *d = [[NSData alloc] initWithBase64EncodedString:m[@"cg"] options:0];
  CGEventRef cg = d ? CGEventCreateFromData(kCFAllocatorDefault, (__bridge CFDataRef)d) : NULL;
  if (!cg) return;
  NSEvent *src = [NSEvent eventWithCGEvent:cg];
  CFRelease(cg);
  if (!src) return;

  // The app sends pane-local points with a top-left origin; the web view
  // fills the shadow window's content view.
  ShadowWindow *win = b.window;
  NSRect bounds = win.contentView.bounds;
  NSPoint loc = NSMakePoint([m[@"x"] doubleValue], bounds.size.height - [m[@"y"] doubleValue]);
  NSTimeInterval ts = NSProcessInfo.processInfo.systemUptime;
  NSInteger wn = win.windowNumber;

  if (src.type == NSEventTypeKeyDown || src.type == NSEventTypeLeftMouseDown || src.type == NSEventTypeOtherMouseDown) {
    b.pressAt = CACurrentMediaTime();
    b.pressAsksTab = src.type == NSEventTypeOtherMouseDown || (src.modifierFlags & NSEventModifierFlagCommand);
  }

  switch (src.type) {
    case NSEventTypeKeyDown:
    case NSEventTypeKeyUp:
    case NSEventTypeFlagsChanged: {
      NSEvent *e;
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
      if (win.firstResponder != web) [win makeFirstResponder:web];
      if (src.type == NSEventTypeKeyUp) {
        [web keyUp:e];
      } else if (src.modifierFlags & NSEventModifierFlagCommand) {
        if (![web performKeyEquivalent:e]) [web keyDown:e];
      } else {
        gPendingOps = m[@"ops"];
        [web keyDown:e];
        gPendingOps = nil;
        pushTextState((NSView<NSTextInputClient> *)web);
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
      // Moves go in through CEF: handed to the web view (as clicks are),
      // Chromium dropped them, so pages never hovered and the cursor only
      // changed on a click (#120). Exited is a mouse leave.
      if (!button && b->cef) {
        CefMouseEvent me;
        me.x = (int)[m[@"x"] doubleValue];
        me.y = (int)[m[@"y"] doubleValue];
        me.modifiers = cefModifiers(src.modifierFlags);
        b->cef->GetHost()->SendMouseMoveEvent(me, src.type == NSEventTypeMouseExited);
        break;
      }
      if (tracking) type = NSEventTypeMouseMoved;
      NSEvent *e = [NSEvent mouseEventWithType:type location:loc modifierFlags:src.modifierFlags timestamp:ts
                                  windowNumber:wn context:nil eventNumber:button ? src.eventNumber : 0
                                    clickCount:button ? src.clickCount : 0 pressure:button ? src.pressure : 0];
      NSView *target = web;
      BOOL down = type == NSEventTypeLeftMouseDown || type == NSEventTypeRightMouseDown ||
                  type == NSEventTypeOtherMouseDown;
      if (down) {
        NSView *hit = [win.contentView hitTest:loc];
        b.mouseTarget = hit ?: web;
        if (win.firstResponder != web) [win makeFirstResponder:web];
      }
      if (type != NSEventTypeMouseMoved && b.mouseTarget) target = b.mouseTarget;
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
      EventProxy *p = [EventProxy alloc];
      p.target = src;
      p.win = win;
      p.loc = loc;
      NSEvent *e = (NSEvent *)p;
      if (src.type == NSEventTypeScrollWheel) [web scrollWheel:e];
      else if (src.type == NSEventTypeMagnify) [web magnifyWithEvent:e];
      else if (src.type == NSEventTypeSmartMagnify) [web smartMagnifyWithEvent:e];
      else [web rotateWithEvent:e];
      break;
    }
    default:
      break;
  }
}

@end

#pragma mark - NSApplication

@interface HostApplication : NSApplication <CefAppProtocol> {
  BOOL handlingSendEvent_;
}
@end
@implementation HostApplication
- (BOOL)isHandlingSendEvent { return handlingSendEvent_; }
- (void)setHandlingSendEvent:(BOOL)v { handlingSendEvent_ = v; }
- (void)sendEvent:(NSEvent *)event {
  CefScopedSendingEvent scoper;
  [super sendEvent:event];
}
@end

int main(int argc, char *argv[]) {
  CefScopedLibraryLoader loader;
  if (!loader.LoadInMain()) return 1;
  CefMainArgs args(argc, argv);
  @autoreleasepool {
    [HostApplication sharedApplication];
    NSApp.activationPolicy = NSApplicationActivationPolicyAccessory;

    CefRefPtr<CefCommandLine> cl = CefCommandLine::CreateCommandLine();
    cl->InitFromArgv(argc, argv);
    if (!cl->HasSwitch("socket") || !cl->HasSwitch("profile")) {
      fprintf(stderr, "usage: %s --socket=PATH --profile=DIR\n", argv[0]);
      return 2;
    }
    gHost = [Host new];
    gHost.socketPath = @(cl->GetSwitchValue("socket").ToString().c_str());
    if (cl->HasSwitch("owner")) gHost.owner = (pid_t)atoi(cl->GetSwitchValue("owner").ToString().c_str());
    gHost.browsers = [NSMutableDictionary dictionary];
    gHost.conns = [NSMutableArray array];
    gHost.built = mtimeOf(NSBundle.mainBundle.executablePath.fileSystemRepresentation);
    gMenuResults = [NSMutableDictionary dictionary];
    gCDPCalls = [NSMutableDictionary dictionary];
    gContextCmds = [NSMutableDictionary dictionary];
    gCDPThen = [NSMutableDictionary dictionary];
    NSString *profile = @(cl->GetSwitchValue("profile").ToString().c_str());

    CefSettings settings;  // sandboxed sub-processes (helper.cc)
    // Bundled inside Hammock.app (Contents/Helpers), Chromium would take
    // the outer app for its own bundle and fail to start: name ours.
    NSString *bundle = NSBundle.mainBundle.bundlePath;
    CefString(&settings.main_bundle_path) = bundle.UTF8String;
    CefString(&settings.framework_dir_path) =
        [bundle stringByAppendingPathComponent:@"Contents/Frameworks/Chromium Embedded Framework.framework"].UTF8String;
    settings.log_severity = LOGSEVERITY_WARNING;
    // A persistent profile (cookies, logins, local storage) shared by every
    // browser pane, kept across restarts with session cookies included.
    CefString(&settings.root_cache_path) = profile.UTF8String;
    CefString(&settings.cache_path) = [profile stringByAppendingPathComponent:@"Default"].UTF8String;
    settings.persist_session_cookies = true;
    if (!CefInitialize(args, settings, new App, nullptr)) return CefGetExitCode();
    [gHost listen];
    CefRunMessageLoop();
    CefShutdown();
  }
  return 0;
}

#pragma mark - DevTools observer

// rawJSON is a DevTools payload as it came ({} if empty).
static NSString *rawJSON(const void *p, size_t n) {
  if (!p || !n) return @"{}";
  return [[NSString alloc] initWithBytes:p length:n encoding:NSUTF8StringEncoding] ?: @"{}";
}

void DevTools::OnDevToolsMethodResult(CefRefPtr<CefBrowser>, int mid, bool success, const void *result, size_t size) {
  if (NSArray *dt = gDTCalls[@(mid)]) {
    // A DevTools pane's request: its reply, under its own id.
    [gDTCalls removeObjectForKey:@(mid)];
    Browser *b = gHost.browsers[dt[0]];
    if (b.dtConn) {
      NSString *msg = [NSString stringWithFormat:@"{\"id\":%@,\"%@\":%@}", dt[1], success ? @"result" : @"error", rawJSON(result, size)];
      [gHost send:@{@"t" : @"dtOut", @"id" : b.bid, @"msg" : msg} to:b.dtConn];
    }
    return;
  }
  if (void (^then)(NSDictionary *) = gCDPThen[@(mid)]) {
    [gCDPThen removeObjectForKey:@(mid)];
    then(success ? jsonOf(result, size) : nil);
    return;
  }
  NSArray *call = gCDPCalls[@(mid)];
  if (!call) return;  // one of startDevTools' enables
  [gCDPCalls removeObjectForKey:@(mid)];
  [gHost send:@{@"t" : @"cdpResult", @"seq" : call[1], @"ok" : @(success), @"result" : jsonOf(result, size)} to:call[0]];
}

void DevTools::OnDevToolsEvent(CefRefPtr<CefBrowser>, const CefString &method, const void *params, size_t size) {
  Browser *b = b_;
  NSString *m = str(method);
  if (b && [m isEqualToString:@"Page.screencastFrame"]) {
    [gHost screencastFrame:[NSData dataWithBytes:params length:size] of:b];
    return;  // ours (remote video), not the DevTools pane's
  }
  if (b.dtConn) {
    NSString *msg = [NSString stringWithFormat:@"{\"method\":\"%@\",\"params\":%@}", m, rawJSON(params, size)];
    [gHost send:@{@"t" : @"dtOut", @"id" : b.bid, @"msg" : msg} to:b.dtConn];
  }
  if (!b || ![forwardedEvents() containsObject:m]) return;
  NSDictionary *msg = nil;
  for (Conn *c in [gHost.conns copy]) {
    if (!c.cdpEvents) continue;
    if (!msg) msg = @{@"t" : @"cdpEvent", @"id" : b.bid, @"method" : m, @"params" : jsonOf(params, size)};
    [gHost send:msg to:c];
  }
}
