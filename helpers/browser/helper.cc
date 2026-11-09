// Chromium sub-process entry point (renderer, GPU, utility). Copied into the
// four "Hammock Browser Helper*.app" bundles macOS CEF expects.
#include "include/cef_app.h"
#include "include/cef_sandbox_mac.h"
#include "include/wrapper/cef_library_loader.h"

int main(int argc, char* argv[]) {
  // The macOS sandbox (Seatbelt) goes on before the CEF framework is loaded,
  // as CEF requires; each process type then gets Chromium's own profile.
  // Loaded from the framework's Libraries/libcef_sandbox.dylib.
  CefScopedSandboxContext sandbox;
  if (!sandbox.Initialize(argc, argv)) return 1;
  CefScopedLibraryLoader loader;
  if (!loader.LoadInHelper()) return 1;
  CefMainArgs args(argc, argv);
  return CefExecuteProcess(args, nullptr, nullptr);
}
