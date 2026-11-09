// hmk-sim-guest: runs inside a foldable simulator (the iPhone Duo), started
// by the simulator host with `xcrun simctl spawn UDID <path>`, for the
// device's fold, which only its motion relay understands.
//
// It registers a virtual HID service with the guest's HID system, as
// DeviceHub's controls do, and sends vendor-defined events (usage page
// 0xff61, usage 0x5b) carrying a small OSSerialized dictionary:
// {provider, source, type, value}. locationd's device state relay
// (CMDeviceStateRelayManager) reads "hinge-slider-control" (a range: the
// hinge angle in degrees, 0 closed to 180 flat) and
// "orientation-picker-control" (an enum: portrait, pud, landscape-left,
// landscape-right). Regular iPhones have no such relay: Hammock rotates those
// through its XCTest runner. (The same technique as EvanBacon/serve-sim's
// Duo helper, Apache-2.0; this code is Hammock's own.)
//
// One command per line on stdin, one reply ("OK" or "ERROR …") per line;
// "READY" first:
//   hinge DEGREES [MS]   to that angle, eased over MS (default: at once)
//   orientation portrait|upside-down|landscape-left|landscape-right
#import <Foundation/Foundation.h>
#import <dlfcn.h>
#import <math.h>
#import <mach/mach_time.h>
#import <objc/message.h>
#import <objc/runtime.h>

// The service's properties, as the HID system asks for them.
@interface ServiceDelegate : NSObject
@property(copy) NSDictionary *properties;
@end
@implementation ServiceDelegate
- (id)propertyForKey:(NSString *)key forService:(id)service { return self.properties[key]; }
- (BOOL)setProperty:(id)value forKey:(NSString *)key forService:(id)service { return YES; }
- (id)copyEventMatching:(id)matching forService:(id)service { return nil; }
- (BOOL)setOutputEvent:(id)event forService:(id)service { return YES; }
- (void)notification:(uint32_t)type withProperty:(id)property forService:(id)service {}
@end

static id makeService(uint32_t page, uint32_t usage) {
  Class cls = NSClassFromString(@"HIDVirtualEventService");
  if (!cls) return nil;
  id service = [[cls alloc] init];
  ServiceDelegate *delegate = [ServiceDelegate new];
  delegate.properties = @{
    @"PrimaryUsagePage" : @(page), @"PrimaryUsage" : @(usage),
    @"DeviceUsagePairs" : @[ @{@"DeviceUsagePage" : @(page), @"DeviceUsage" : @(usage)} ],
    @"Transport" : @"CoreDevice", @"Product" : @"Hammock", @"Built-In" : @NO,
    @"VendorID" : @0, @"ProductID" : @0, @"VersionNumber" : @0, @"ReportInterval" : @8000,
  };
  objc_setAssociatedObject(service, "hammock.delegate", delegate, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  ((void (*)(id, SEL, id))objc_msgSend)(service, sel_registerName("setDelegate:"), delegate);
  ((void (*)(id, SEL, id))objc_msgSend)(service, sel_registerName("setDispatchQueue:"), dispatch_queue_create("hammock.guest", DISPATCH_QUEUE_SERIAL));
  ((void (*)(id, SEL))objc_msgSend)(service, sel_registerName("activate"));
  uint64_t sid = ((uint64_t (*)(id, SEL))objc_msgSend)(service, sel_registerName("serviceID"));
  return sid ? service : nil;
}

// The OSSerialize binary format: a 4-byte header per object (length or
// count, then a type byte; 0x80 marks a collection's last object).
static void put(NSMutableData *d, uint32_t n, uint8_t type) {
  uint8_t b[] = {n & 0xff, (n >> 8) & 0xff, (n >> 16) & 0xff, type};
  [d appendBytes:b length:4];
}
static void putString(NSMutableData *d, const char *s, BOOL key, BOOL last) {
  size_t n = strlen(s) + (key ? 1 : 0);  // symbols (keys) carry their NUL
  put(d, (uint32_t)n, (key ? 0x08 : 0x09) | (last ? 0x80 : 0));
  [d appendBytes:s length:n];
  while (d.length % 4) [d appendBytes:"\0" length:1];
}
// {provider, source, type, value}, with the value left for the caller.
static NSMutableData *control(const char *source, const char *type) {
  NSMutableData *d = [NSMutableData data];
  put(d, 0xd3, 0x00);  // the format's signature
  put(d, 4, 0x81);     // a dictionary of 4: the top-level (last) object
  putString(d, "provider", YES, NO);
  putString(d, "com.apple.Virtualization.VirtualMachines", NO, NO);
  putString(d, "source", YES, NO);
  putString(d, source, NO, NO);
  putString(d, "type", YES, NO);
  putString(d, type, NO, NO);
  putString(d, "value", YES, NO);
  return d;
}
static NSData *hingePayload(double degrees) {
  NSMutableData *d = control("hinge-slider-control", "range");
  // A number, last; DeviceHub's own header (0x3f) with the angle as a double.
  put(d, 0x3f, 0x84);
  [d appendBytes:&degrees length:sizeof degrees];
  return d;
}
static NSData *orientationPayload(const char *value) {
  NSMutableData *d = control("orientation-picker-control", "enum");
  putString(d, value, NO, YES);
  return d;
}

typedef CFTypeRef (*VendorEventFn)(CFAllocatorRef, uint64_t, uint32_t, uint32_t, uint32_t, const void *, CFIndex, uint32_t);
static VendorEventFn vendorEvent;
static id motion;

static BOOL send(NSData *payload) {
  CFTypeRef ev = vendorEvent(NULL, mach_absolute_time(), 0xff61, 0x5b, 0, payload.bytes, (CFIndex)payload.length, 0);
  BOOL ok = ev && ((BOOL (*)(id, SEL, id))objc_msgSend)(motion, sel_registerName("dispatchEvent:"), (__bridge id)ev);
  if (ev) CFRelease(ev);
  return ok;
}

static double angle = -1;  // where the hinge was last put

static const char *hinge(double to, double ms) {
  if (!isfinite(to) || to < 0 || to > 180) return "ERROR hinge: 0 (closed) to 180 (flat)";
  double from = angle < 0 ? to : angle;
  int frames = ms > 0 && from != to ? (int)fmax(2, ms / 16) : 1;
  for (int i = 1; i <= frames; i++) {
    double t = (double)i / frames, eased = 1 - pow(1 - t, 3);
    if (!send(hingePayload(from + (to - from) * eased))) return "ERROR not dispatched";
    if (i < frames) usleep((useconds_t)(ms * 1000 / frames));
  }
  angle = to;
  return "OK";
}

int main(void) {
  @autoreleasepool {
    dlopen("/System/Library/PrivateFrameworks/HID.framework/HID", RTLD_NOW);
    vendorEvent = (VendorEventFn)dlsym(dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", RTLD_NOW), "IOHIDEventCreateVendorDefinedEvent");
    motion = makeService(0xff61, 0x5b);
    if (!vendorEvent || !motion) {
      printf("ERROR HID services unavailable\n");
      fflush(stdout);
      return 1;
    }
    usleep(300000);  // backboardd enumerates the new service
    printf("READY\n");
    fflush(stdout);
    char *line = NULL;
    size_t cap = 0;
    while (getline(&line, &cap, stdin) > 0) {
      @autoreleasepool {
        char word[32] = {0}, arg[64] = {0};
        double ms = 0;
        int n = sscanf(line, "%31s %63s %lf", word, arg, &ms);
        const char *reply = "ERROR unknown command";
        if (n >= 2 && !strcmp(word, "hinge")) {
          reply = hinge(atof(arg), ms);
        } else if (n >= 2 && !strcmp(word, "orientation")) {
          const char *v = !strcmp(arg, "portrait") ? "portrait" : !strcmp(arg, "upside-down") ? "pud"
                        : !strcmp(arg, "landscape-left") ? "landscape-left" : !strcmp(arg, "landscape-right") ? "landscape-right" : NULL;
          reply = !v ? "ERROR orientation: portrait, upside-down, landscape-left or landscape-right"
                     : send(orientationPayload(v)) ? "OK" : "ERROR not dispatched";
        }
        printf("%s\n", reply);
        fflush(stdout);
      }
    }
    free(line);
  }
  return 0;
}
