// Spike: read a booted simulator's framebuffer IOSurface and save it as PNG.
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <CoreImage/CoreImage.h>
#import <IOSurface/IOSurface.h>
#import <objc/runtime.h>
#import <objc/message.h>
#include <dlfcn.h>

int main(int argc, char **argv) {
  @autoreleasepool {
    dlopen("/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator", RTLD_NOW);
    id ctx = ((id(*)(id, SEL, id, id *))objc_msgSend)(objc_getClass("SimServiceContext"), NSSelectorFromString(@"sharedServiceContextForDeveloperDir:error:"), @"/Applications/Xcode.app/Contents/Developer", nil);
    id set = ((id(*)(id, SEL, id *))objc_msgSend)(ctx, NSSelectorFromString(@"defaultDeviceSetWithError:"), nil);
    for (id dev in [set valueForKey:@"devices"]) {
      if ([[dev valueForKey:@"state"] integerValue] != 3) continue;
      id io = [dev valueForKey:@"io"];
      int i = 0;
      for (id port in ((id(*)(id, SEL))objc_msgSend)(io, NSSelectorFromString(@"ioPorts"))) {
        id desc = ((id(*)(id, SEL))objc_msgSend)(port, NSSelectorFromString(@"descriptor"));
        if (![desc conformsToProtocol:objc_getProtocol("SimDisplayIOSurfaceRenderable")]) continue;
        id props = [desc respondsToSelector:NSSelectorFromString(@"screenProperties")] ? ((id(*)(id, SEL))objc_msgSend)(desc, NSSelectorFromString(@"screenProperties")) : nil;
        IOSurfaceRef s = (__bridge IOSurfaceRef)((id(*)(id, SEL))objc_msgSend)(desc, NSSelectorFromString(@"framebufferSurface"));
        printf("display %d: surface %p %zux%zu props %s\n", i, s, s ? IOSurfaceGetWidth(s) : 0, s ? IOSurfaceGetHeight(s) : 0, [[props description] UTF8String] ?: "-");
        if (s) {
          CIImage *img = [CIImage imageWithIOSurface:s];
          NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCIImage:img];
          NSString *path = [NSString stringWithFormat:@"%s-%d.png", argv[1], i];
          [[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:path atomically:YES];
        }
        i++;
      }
      break;
    }
  }
}
