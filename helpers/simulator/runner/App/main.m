// The UI-test bundle's target app. Nothing runs it: the runner only needs
// XCTest's session, so this is an empty app to satisfy the build.
#import <UIKit/UIKit.h>

int main(int argc, char *argv[]) {
  @autoreleasepool {
    return UIApplicationMain(argc, argv, nil, nil);
  }
}
