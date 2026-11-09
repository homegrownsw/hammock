// The simulator runner's one "test": it connects to the host's Unix socket
// (HMK_SIM_RUNNER_SOCKET, passed as TEST_RUNNER_HMK_SIM_RUNNER_SOCKET to
// xcodebuild) and serves commands until the host closes it. One JSON object
// per line each way:
//
//   {"cmd":"orientation","value":"portrait|upside-down|landscape-left|landscape-right|left|right"}
//       left and right: a quarter turn of the device from where it is
//   {"cmd":"gesture","paths":[[{"x":0.4,"y":0.5,"t":0},{"x":0.2,"y":0.5,"t":300}],[…]]}
//       fingers' paths; x and y are 0–1 of the device's native portrait (the
//       framebuffer, however the device is turned), t in ms
//
// replies {"ok":true,"device":N} or {"ok":false,"error":"…","device":N}, N being
// the device's UIDeviceOrientation after the command.
//
// XCUIDevice's orientation is public API. Gestures use XCTest's private
// XCSynthesizedEventRecord and XCPointerEventPath, which drive any number of
// fingers (the host's Indigo touches carry one).
#import <XCTest/XCTest.h>
#import <objc/message.h>
#import <sys/socket.h>
#import <sys/un.h>

@interface HammockSimRunner : XCTestCase
@end

@implementation HammockSimRunner {
  XCUIApplication *_springboard;
}

- (void)testServe {
  self.continueAfterFailure = YES;
  NSString *path = NSProcessInfo.processInfo.environment[@"HMK_SIM_RUNNER_SOCKET"];
  if (!path.length) {
    NSLog(@"hmk-sim-runner: no HMK_SIM_RUNNER_SOCKET");
    return;
  }
  int fd = socket(AF_UNIX, SOCK_STREAM, 0);
  struct sockaddr_un addr = {.sun_family = AF_UNIX};
  strlcpy(addr.sun_path, path.fileSystemRepresentation, sizeof(addr.sun_path));
  if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) != 0) {
    NSLog(@"hmk-sim-runner: can't connect to %@: %s", path, strerror(errno));
    close(fd);
    return;
  }
  _springboard = [[XCUIApplication alloc] initWithBundleIdentifier:@"com.apple.springboard"];
  [self send:@{@"ready" : @YES, @"device" : @(XCUIDevice.sharedDevice.orientation)} to:fd];
  FILE *in = fdopen(dup(fd), "r");
  char *line = NULL;
  size_t cap = 0;
  while (getline(&line, &cap, in) > 0) {
    @autoreleasepool {
      NSData *data = [NSData dataWithBytesNoCopy:line length:strlen(line) freeWhenDone:NO];
      NSDictionary *msg = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
      NSString *error = nil;
      @try {
        error = [msg isKindOfClass:NSDictionary.class] ? [self handle:msg] : @"not JSON";
      } @catch (NSException *e) {
        error = e.reason ?: e.name;
      }
      NSMutableDictionary *reply = [@{@"ok" : @(error == nil)} mutableCopy];
      if (error) reply[@"error"] = error;
      // The device's orientation, for the host to turn its picture by.
      reply[@"device"] = @(XCUIDevice.sharedDevice.orientation);
      [self send:reply to:fd];
    }
  }
  free(line);
  fclose(in);
  close(fd);
}

- (void)send:(NSDictionary *)reply to:(int)fd {
  NSMutableData *d = [[NSJSONSerialization dataWithJSONObject:reply options:0 error:nil] mutableCopy];
  [d appendBytes:"\n" length:1];
  (void)write(fd, d.bytes, d.length);
}

- (NSString *)handle:(NSDictionary *)msg {
  NSString *cmd = msg[@"cmd"];
  if ([cmd isEqual:@"orientation"]) {
    NSDictionary *names = @{
      @"portrait" : @(UIDeviceOrientationPortrait),
      @"upside-down" : @(UIDeviceOrientationPortraitUpsideDown),
      @"landscape-left" : @(UIDeviceOrientationLandscapeLeft),
      @"landscape-right" : @(UIDeviceOrientationLandscapeRight),
    };
    // left and right turn the device a quarter turn from where it is.
    NSArray *clockwise = @[ @(UIDeviceOrientationPortrait), @(UIDeviceOrientationLandscapeRight),
                            @(UIDeviceOrientationPortraitUpsideDown), @(UIDeviceOrientationLandscapeLeft) ];
    NSNumber *o = names[msg[@"value"]];
    if (!o && ([msg[@"value"] isEqual:@"left"] || [msg[@"value"] isEqual:@"right"])) {
      NSUInteger i = [clockwise indexOfObject:@(XCUIDevice.sharedDevice.orientation)];
      if (i == NSNotFound) i = 0;  // face up or down: from portrait
      o = clockwise[(i + ([msg[@"value"] isEqual:@"right"] ? 1 : 3)) % 4];
    }
    if (!o) return @"orientation: portrait, upside-down, landscape-left, landscape-right, left or right";
    XCUIDevice.sharedDevice.orientation = o.integerValue;
    return nil;
  }
  if ([cmd isEqual:@"gesture"]) return [self gesture:msg[@"paths"]];
  return [NSString stringWithFormat:@"unknown command %@", cmd];
}

// Fingers on the screen, as ratios of the device's native portrait.
- (NSString *)gesture:(NSArray *)paths {
  if (![paths isKindOfClass:NSArray.class] || paths.count == 0) return @"gesture: no paths";
  Class recordClass = NSClassFromString(@"XCSynthesizedEventRecord");
  Class pathClass = NSClassFromString(@"XCPointerEventPath");
  if (!recordClass || !pathClass) return @"gesture: this XCTest has no event synthesis";
  // In the device's own portrait (the framebuffer's), whichever way it's
  // turned: the host maps what you see onto it, as it does touches.
  CGRect frame = _springboard.frame;
  CGSize screen = CGSizeMake(MIN(frame.size.width, frame.size.height), MAX(frame.size.width, frame.size.height));
  NSInteger orientation = UIInterfaceOrientationPortrait;
  id record = ((id(*)(id, SEL, NSString *, NSUInteger, NSInteger))objc_msgSend)(
      [recordClass alloc], NSSelectorFromString(@"initWithName:displayID:interfaceOrientation:"), @"Hammock gesture", 0,
      orientation);
  for (NSArray *samples in paths) {
    if (![samples isKindOfClass:NSArray.class] || samples.count == 0) return @"gesture: empty path";
    id path = nil;
    double last = 0;
    for (NSDictionary *s in samples) {
      CGPoint p = CGPointMake([s[@"x"] doubleValue] * screen.width, [s[@"y"] doubleValue] * screen.height);
      double t = [s[@"t"] doubleValue] / 1000.0;
      if (!path) {
        path = ((id(*)(id, SEL, CGPoint, double))objc_msgSend)([pathClass alloc],
                                                                NSSelectorFromString(@"initForTouchAtPoint:offset:"), p, t);
      } else {
        ((void (*)(id, SEL, CGPoint, double))objc_msgSend)(path, NSSelectorFromString(@"moveToPoint:atOffset:"), p, t);
      }
      last = t;
    }
    ((void (*)(id, SEL, double))objc_msgSend)(path, NSSelectorFromString(@"liftUpAtOffset:"), last + 0.01);
    ((void (*)(id, SEL, id))objc_msgSend)(record, NSSelectorFromString(@"addPointerEventPath:"), path);
  }
  NSError *error = nil;
  BOOL ok = ((BOOL(*)(id, SEL, NSError **))objc_msgSend)(record, NSSelectorFromString(@"synthesizeWithError:"), &error);
  return ok ? nil : (error.localizedDescription ?: @"gesture: synthesis failed");
}

@end
