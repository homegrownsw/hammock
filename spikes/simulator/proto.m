#import <Foundation/Foundation.h>
#import <objc/runtime.h>
#include <dlfcn.h>
int main(int argc, char **argv) {
  dlopen("/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator", RTLD_NOW);
  dlopen("/Applications/Xcode.app/Contents/SharedFrameworks/SimulatorKit.framework/SimulatorKit", RTLD_NOW);
  for (int i = 1; i < argc; i++) {
    Protocol *p = objc_getProtocol(argv[i]);
    printf("== %s %s\n", argv[i], p ? "" : "missing");
    if (!p) continue;
    for (int req = 0; req < 2; req++) {
      unsigned n; struct objc_method_description *ms = protocol_copyMethodDescriptionList(p, req, YES, &n);
      for (unsigned j = 0; j < n; j++) printf("  %s%s  %s\n", req ? "" : "(opt) ", sel_getName(ms[j].name), ms[j].types);
      free(ms);
    }
  }
}
