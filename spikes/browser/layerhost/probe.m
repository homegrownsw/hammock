// Spike: can one process display a layer tree owned by another process?
//   probe serve          # prints a CAContext id, animates a layer in it
//   probe host <id>      # opens a window showing that context via CALayerHost
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>

@interface CAContext : NSObject
+ (instancetype)contextWithCGSConnection:(uint32_t)cid options:(NSDictionary *)opts;
@property(readonly) uint32_t contextId;
@property(retain) CALayer *layer;
@end
@interface CALayerHost : CALayer
@property uint32_t contextId;
@end
extern uint32_t CGSMainConnectionID(void);

static CAContext *ctx;

static void serve(void) {
  ctx = [CAContext contextWithCGSConnection:CGSMainConnectionID() options:@{}];
  CALayer *root = [CALayer layer];
  root.frame = CGRectMake(0, 0, 400, 300);
  root.backgroundColor = NSColor.systemTealColor.CGColor;
  CALayer *box = [CALayer layer];
  box.frame = CGRectMake(150, 100, 100, 100);
  box.backgroundColor = NSColor.systemOrangeColor.CGColor;
  [root addSublayer:box];
  CABasicAnimation *spin = [CABasicAnimation animationWithKeyPath:@"transform.rotation.z"];
  spin.toValue = @(M_PI * 2);
  spin.duration = 2;
  spin.repeatCount = HUGE_VALF;
  [box addAnimation:spin forKey:@"spin"];
  ctx.layer = root;
  printf("%u\n", ctx.contextId);
  fflush(stdout);
}

static void host(uint32_t cid) {
  NSWindow *w = [[NSWindow alloc] initWithContentRect:NSMakeRect(200, 200, 400, 300)
                                            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
                                              backing:NSBackingStoreBuffered defer:NO];
  w.title = @"CALayerHost probe";
  NSView *v = w.contentView;
  v.wantsLayer = YES;
  CALayerHost *h = [CALayerHost layer];
  h.contextId = cid;
  h.frame = v.bounds;
  // The hosted layer tree uses bottom-left origin like the serving side.
  [v.layer addSublayer:h];
  [w makeKeyAndOrderFront:nil];
  printf("window %ld\n", (long)w.windowNumber);
  fflush(stdout);
}

int main(int argc, char **argv) {
  @autoreleasepool {
    NSApplication *app = NSApplication.sharedApplication;
    if (argc >= 2 && !strcmp(argv[1], "serve")) {
      app.activationPolicy = NSApplicationActivationPolicyAccessory;
      serve();
    } else if (argc >= 3 && !strcmp(argv[1], "host")) {
      app.activationPolicy = NSApplicationActivationPolicyRegular;
      host((uint32_t)strtoul(argv[2], NULL, 10));
      [app activateIgnoringOtherApps:YES];
    } else {
      fprintf(stderr, "usage: probe serve | probe host <context-id>\n");
      return 2;
    }
    [app run];
  }
}
