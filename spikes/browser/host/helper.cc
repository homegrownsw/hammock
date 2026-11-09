// Chromium sub-process entry point (renderer, GPU, utility). Copied into the
// five "BrowserHost Helper*.app" bundles macOS CEF expects.
#include "include/cef_app.h"
#include "include/wrapper/cef_library_loader.h"

int main(int argc, char* argv[]) {
  CefScopedLibraryLoader loader;
  if (!loader.LoadInHelper()) return 1;
  CefMainArgs args(argc, argv);
  return CefExecuteProcess(args, nullptr, nullptr);
}
