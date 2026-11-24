// The simulator runner's one "test": it connects to the host's Unix socket
// (HMK_SIM_RUNNER_SOCKET, passed as TEST_RUNNER_HMK_SIM_RUNNER_SOCKET to
// xcodebuild) and serves commands until the host closes it. One JSON object
// per line each way:
//
//   {"cmd":"orientation","value":"portrait|upside-down|landscape-left|landscape-right|left|right"}
//       left and right: a quarter turn of the device from where it is
//   {"cmd":"gesture","paths":[[{"x":0.4,"y":0.5,"t":0},{"x":0.2,"y":0.5,"t":300}],[…]]}
//       fingers' paths; x and y are 0–1 of the device's native portrait (the
//       framebuffer, however the device is turned), t in ms; on a foldable's
//       inner screen also "display" (its screenID) and "w", "h" (its
//       framebuffer, in points)
//   {"cmd":"describe","x":0.5,"y":0.5}
//       the accessibility element at a point of the screen as it shows (x
//       and y 0–1 of the frontmost app's frame, turned as it is), for a
//       design note's pin: the reply has "element", {app, type, label,
//       identifier, value, title, placeholder, enabled, selected, frame {x,
//       y, w, h}, screen {w, h} (points), path (the types of the elements
//       it's in)}, or null for none
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

static NSString *typeName(XCUIElementType t);

@interface HammockSimRunner : XCTestCase
@end

@implementation HammockSimRunner {
  XCUIApplication *_springboard;
  NSDictionary *_extra;  // the command's answer, besides ok and error (describe's element)
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
      _extra = nil;
      @try {
        error = [msg isKindOfClass:NSDictionary.class] ? [self handle:msg] : @"not JSON";
      } @catch (NSException *e) {
        error = e.reason ?: e.name;
      }
      NSMutableDictionary *reply = [@{@"ok" : @(error == nil)} mutableCopy];
      if (_extra) [reply addEntriesFromDictionary:_extra];
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
  if ([cmd isEqual:@"gesture"]) return [self gesture:msg];
  if ([cmd isEqual:@"describe"]) return [self describe:msg];
  return [NSString stringWithFormat:@"unknown command %@", cmd];
}

// Fingers on the screen, as ratios of the device's native portrait.
- (NSString *)gesture:(NSDictionary *)msg {
  NSArray *paths = msg[@"paths"];
  if (![paths isKindOfClass:NSArray.class] || paths.count == 0) return @"gesture: no paths";
  Class recordClass = NSClassFromString(@"XCSynthesizedEventRecord");
  Class pathClass = NSClassFromString(@"XCPointerEventPath");
  if (!recordClass || !pathClass) return @"gesture: this XCTest has no event synthesis";
  // In the device's own portrait (the framebuffer's), whichever way it's
  // turned: the host maps what you see onto it, as it does touches.
  CGRect frame = _springboard.frame;
  CGSize screen = CGSizeMake(MIN(frame.size.width, frame.size.height), MAX(frame.size.width, frame.size.height));
  // A foldable's inner screen: its display (XCTest's displayID is the
  // screen's screenID) and its framebuffer's size, from the host. Display 0
  // is the main screen.
  NSUInteger display = [msg[@"display"] unsignedIntegerValue];
  if ([msg[@"w"] doubleValue] > 0 && [msg[@"h"] doubleValue] > 0) screen = CGSizeMake([msg[@"w"] doubleValue], [msg[@"h"] doubleValue]);
  NSInteger orientation = UIInterfaceOrientationPortrait;
  id record = ((id(*)(id, SEL, NSString *, NSUInteger, NSInteger))objc_msgSend)(
      [recordClass alloc], NSSelectorFromString(@"initWithName:displayID:interfaceOrientation:"), @"Hammock gesture", display,
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

// The app in front: XCTest's own accessibility client names it (private,
// as WebDriverAgent uses it), else the home screen.
- (XCUIApplication *)frontmostApp {
  Class client = NSClassFromString(@"XCAXClient_iOS");
  SEL shared = NSSelectorFromString(@"sharedClient"), active = NSSelectorFromString(@"activeApplications");
  SEL withPID = NSSelectorFromString(@"applicationWithPID:");
  if (client && [client respondsToSelector:shared] && [XCUIApplication respondsToSelector:withPID]) {
    id c = ((id(*)(id, SEL))objc_msgSend)(client, shared);
    NSArray *apps = [c respondsToSelector:active] ? ((id(*)(id, SEL))objc_msgSend)(c, active) : nil;
    for (id app in apps) {
      pid_t pid = 0;
      @try {
        pid = [[app valueForKey:@"processIdentifier"] intValue];
      } @catch (NSException *e) {
        continue;
      }
      if (pid <= 0) continue;
      XCUIApplication *a = ((id(*)(id, SEL, pid_t))objc_msgSend)(XCUIApplication.class, withPID, pid);
      // The home screen is listed too, behind the app: the app first.
      NSString *bundle = nil;
      @try {
        bundle = [a valueForKey:@"bundleID"];
      } @catch (NSException *e) {
      }
      if (a && ![bundle isEqual:@"com.apple.springboard"]) return a;
    }
  }
  return _springboard;
}

// What's at a point, for a design note's pin: the smallest element whose
// frame holds it, in the frontmost app's accessibility tree.
- (NSString *)describe:(NSDictionary *)msg {
  XCUIApplication *app = [self frontmostApp];
  NSError *error = nil;
  id<XCUIElementSnapshot> root = [app snapshotWithError:&error];
  if (!root) return error.localizedDescription ?: @"describe: no snapshot of the app";
  CGRect screen = root.frame;
  CGPoint p = CGPointMake(screen.origin.x + [msg[@"x"] doubleValue] * screen.size.width,
                          screen.origin.y + [msg[@"y"] doubleValue] * screen.size.height);
  id<XCUIElementSnapshot> best = nil;
  NSArray<NSString *> *bestPath = @[];
  double bestArea = INFINITY;
  // Depth first, children in order: of equal frames, the deeper and later
  // wins (a label over its cell).
  NSMutableArray *stack = [NSMutableArray arrayWithObject:@[ root, @[] ]];
  while (stack.count) {
    NSArray *top = stack.lastObject;
    [stack removeLastObject];
    id<XCUIElementSnapshot> e = top[0];
    NSArray<NSString *> *path = top[1];
    CGRect f = e.frame;
    if (!CGRectContainsPoint(f, p) || f.size.width <= 0 || f.size.height <= 0) continue;
    double area = f.size.width * f.size.height;
    if (area <= bestArea && e.elementType != XCUIElementTypeApplication) {
      best = e;
      bestArea = area;
      bestPath = path;
    }
    NSArray *inner = [path arrayByAddingObject:typeName(e.elementType)];
    for (id<XCUIElementSnapshot> c in e.children.reverseObjectEnumerator) [stack addObject:@[ c, inner ]];
  }
  NSMutableDictionary *el = nil;
  if (best) {
    CGRect f = best.frame;
    el = [@{
      @"type" : typeName(best.elementType),
      @"label" : best.label ?: @"",
      @"identifier" : best.identifier ?: @"",
      @"title" : best.title ?: @"",
      @"placeholder" : best.placeholderValue ?: @"",
      @"enabled" : @(best.isEnabled),
      @"selected" : @(best.isSelected),
      @"frame" : @{@"x" : @(f.origin.x - screen.origin.x), @"y" : @(f.origin.y - screen.origin.y), @"w" : @(f.size.width), @"h" : @(f.size.height)},
      @"screen" : @{@"w" : @(screen.size.width), @"h" : @(screen.size.height)},
      // The last few it's in, past the window.
      @"path" : bestPath.count > 4 ? [bestPath subarrayWithRange:NSMakeRange(bestPath.count - 4, 4)] : bestPath,
    } mutableCopy];
    id value = best.value;
    if ([value isKindOfClass:NSString.class] || [value isKindOfClass:NSNumber.class]) el[@"value"] = [value description];
    NSString *bundle = nil;
    @try {
      bundle = [app valueForKey:@"bundleID"];
    } @catch (NSException *e) {
    }
    if (bundle) el[@"app"] = bundle;
  }
  _extra = @{@"element" : el ?: NSNull.null};
  return nil;
}

@end

// XCUIElementType's names, for people and agents.
static NSString *typeName(XCUIElementType t) {
  static NSArray *names;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    names = @[
      @"Any", @"Other", @"Application", @"Group", @"Window", @"Sheet", @"Drawer", @"Alert", @"Dialog", @"Button", @"RadioButton",
      @"RadioGroup", @"CheckBox", @"DisclosureTriangle", @"PopUpButton", @"ComboBox", @"MenuButton", @"ToolbarButton", @"Popover",
      @"Keyboard", @"Key", @"NavigationBar", @"TabBar", @"TabGroup", @"Toolbar", @"StatusBar", @"Table", @"TableRow", @"TableColumn",
      @"Outline", @"OutlineRow", @"Browser", @"CollectionView", @"Slider", @"PageIndicator", @"ProgressIndicator",
      @"ActivityIndicator", @"SegmentedControl", @"Picker", @"PickerWheel", @"Switch", @"Toggle", @"Link", @"Image", @"Icon",
      @"SearchField", @"ScrollView", @"ScrollBar", @"StaticText", @"TextField", @"SecureTextField", @"DatePicker", @"TextView",
      @"Menu", @"MenuItem", @"MenuBar", @"MenuBarItem", @"Map", @"WebView", @"IncrementArrow", @"DecrementArrow", @"Timeline",
      @"RatingIndicator", @"ValueIndicator", @"SplitGroup", @"Splitter", @"RelevanceIndicator", @"ColorWell", @"HelpTag", @"Matte",
      @"DockItem", @"Ruler", @"RulerMarker", @"Grid", @"LevelIndicator", @"Cell", @"LayoutArea", @"LayoutItem", @"Handle",
      @"Stepper", @"Tab", @"TouchBar", @"StatusItem"
    ];
  });
  return (NSUInteger)t < names.count ? names[t] : [NSString stringWithFormat:@"Type%ld", (long)t];
}
