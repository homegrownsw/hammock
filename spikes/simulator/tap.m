// Spike: tap a booted simulator's screen through SimulatorKit's HID client.
// Usage: tap X_RATIO Y_RATIO   (0..1 of the screen)
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <objc/runtime.h>
#import <objc/message.h>
#include <dlfcn.h>

// Xcode 27: (point, second point, target, event type, edge, screen width, height); the
// point is divided by the size, so any units work as long as they match.
typedef void *(*MouseFn)(CGPoint *, CGPoint *, uint32_t target, NSEventType type, uint32_t edge, double w, double h);

int main(int argc, char **argv) {
  @autoreleasepool {
    dlopen("/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator", RTLD_NOW);
    void *sk = dlopen("/Applications/Xcode.app/Contents/SharedFrameworks/SimulatorKit.framework/SimulatorKit", RTLD_NOW);
    MouseFn mouse = (MouseFn)dlsym(sk, "IndigoHIDMessageForMouseNSEvent");
    id ctx = ((id(*)(id, SEL, id, id *))objc_msgSend)(objc_getClass("SimServiceContext"), NSSelectorFromString(@"sharedServiceContextForDeveloperDir:error:"), @"/Applications/Xcode.app/Contents/Developer", nil);
    id set = ((id(*)(id, SEL, id *))objc_msgSend)(ctx, NSSelectorFromString(@"defaultDeviceSetWithError:"), nil);
    id dev = nil;
    for (id d in [set valueForKey:@"devices"]) if ([[d valueForKey:@"state"] integerValue] == 3) dev = d;
    NSError *err = nil;
    id hid = ((id(*)(id, SEL, id, NSError **))objc_msgSend)([objc_getClass("_TtC12SimulatorKit24SimDeviceLegacyHIDClient") alloc], NSSelectorFromString(@"initWithDevice:error:"), dev, &err);
    if (!hid) { printf("hid: %s\n", err.description.UTF8String); return 1; }
    CGPoint p = CGPointMake(atof(argv[1]), atof(argv[2]));
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    for (NSEventType t : (NSEventType[]){NSEventTypeLeftMouseDown, NSEventTypeLeftMouseUp}) {
      void *msg = mouse(&p, NULL, 0x32, t, 0, 1.0, 1.0);
      ((void(*)(id, SEL, void *, BOOL, id, id))objc_msgSend)(hid, NSSelectorFromString(@"sendWithMessage:freeWhenDone:completionQueue:completion:"), msg, YES, dispatch_get_global_queue(0, 0), ^(NSError *e) {
        if (e) printf("send: %s\n", e.description.UTF8String);
        dispatch_semaphore_signal(done);
      });
      dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
      usleep(50000);
    }
    printf("tapped %.3f,%.3f\n", p.x, p.y);
  }
}
