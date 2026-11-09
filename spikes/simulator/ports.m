// Spike: find a booted simulator's display surface through CoreSimulator.
#import <Foundation/Foundation.h>
#import <IOSurface/IOSurface.h>
#import <objc/runtime.h>
#include <dlfcn.h>

static void methods(id o) {
  unsigned n; Method *ms = class_copyMethodList([o class], &n);
  for (unsigned i = 0; i < n; i++) printf("      - %s\n", sel_getName(method_getName(ms[i])));
  free(ms);
  unsigned pn; Protocol * __unsafe_unretained *ps = class_copyProtocolList([o class], &pn);
  for (unsigned i = 0; i < pn; i++) printf("      <%s>\n", protocol_getName(ps[i]));
}

int main(int argc, char **argv) {
  @autoreleasepool {
    dlopen("/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator", RTLD_NOW);
    dlopen("/Applications/Xcode.app/Contents/SharedFrameworks/SimulatorKit.framework/SimulatorKit", RTLD_NOW);
    NSError *err;
    id ctx = [objc_getClass("SimServiceContext") performSelector:NSSelectorFromString(@"sharedServiceContextForDeveloperDir:error:")
                                                      withObject:@"/Applications/Xcode.app/Contents/Developer" withObject:nil];
    id set = [ctx performSelector:NSSelectorFromString(@"defaultDeviceSetWithError:") withObject:nil];
    NSString *want = argc > 1 ? @(argv[1]) : nil;
    for (id dev in [set valueForKey:@"devices"]) {
      NSString *udid = [[dev valueForKey:@"UDID"] UUIDString];
      if (want && ![udid isEqualToString:want]) continue;
      if ([[dev valueForKey:@"state"] integerValue] != 3) continue;  // booted
      printf("%s %s\n", [[dev valueForKey:@"name"] UTF8String], udid.UTF8String);
      id io = [dev valueForKey:@"io"];
      for (id port in [io valueForKey:@"ioPorts"]) {
        id desc = [port performSelector:NSSelectorFromString(@"descriptor")];
        printf("  port %s desc %s\n", class_getName([port class]), class_getName([desc class]));
        methods(desc);
      }
    }
    (void)err;
  }
  return 0;
}
