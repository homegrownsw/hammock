// Lists methods of CoreSimulator/SimulatorKit classes used for display and input.
#import <Foundation/Foundation.h>
#import <objc/runtime.h>
#include <dlfcn.h>

static void dump(const char *name) {
  Class c = objc_getClass(name);
  if (!c) { printf("%s: missing\n", name); return; }
  printf("== %s : %s\n", name, class_getName(class_getSuperclass(c)));
  unsigned n; Method *ms = class_copyMethodList(c, &n);
  for (unsigned i = 0; i < n; i++) printf("  - %s  %s\n", sel_getName(method_getName(ms[i])), method_getTypeEncoding(ms[i]));
  free(ms);
  ms = class_copyMethodList(object_getClass(c), &n);
  for (unsigned i = 0; i < n; i++) printf("  + %s\n", sel_getName(method_getName(ms[i])));
  free(ms);
}

int main(int argc, char **argv) {
  dlopen("/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator", RTLD_NOW);
  dlopen("/Applications/Xcode.app/Contents/SharedFrameworks/SimulatorKit.framework/SimulatorKit", RTLD_NOW);
  for (int i = 1; i < argc; i++) dump(argv[i]);
  return 0;
}
