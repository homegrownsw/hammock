// Who may talk to a helper. A helper's socket sits in the state dir
// (0700), so only you reach it; but anything of yours would then drive
// its simulators or pages, or borrow its grants, past hmkd's checks (an
// agent in a pane running nc). So a helper takes a connection only from:
//   - the hmkd that started it (--owner=PID), and
//   - a process signed as Hammock is, by this helper's own team with the
//     hardened runtime (nothing injected): hmkd (a restarted daemon) or
//     the Hammock app (it connects for live layers and input).
// A helper with no team (an ad-hoc dev build) checks the peer's program is
// its own app's (the app or hmkd in the .app this helper is inside).
//
// Header only: include it in one file of a helper, and link Security.
#pragma once

#import <Foundation/Foundation.h>
#import <Security/Security.h>
#include <libproc.h>
#include <sys/socket.h>
#include <sys/un.h>

// The connection's other end: its pid and audit token (the token names the
// process itself, so a pid reused meanwhile can't pass for it).
static BOOL HammockPeerToken(int fd, pid_t *pid, audit_token_t *token) {
  socklen_t n = sizeof *pid;
  if (getsockopt(fd, SOL_LOCAL, LOCAL_PEERPID, pid, &n) != 0) return NO;
  n = sizeof *token;
  return getsockopt(fd, SOL_LOCAL, LOCAL_PEERTOKEN, token, &n) == 0;
}

// This process's team, or nil (ad hoc).
static NSString *HammockOwnTeam(void) {
  static NSString *team;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    SecCodeRef me = NULL;
    SecStaticCodeRef st = NULL;
    CFDictionaryRef info = NULL;
    if (SecCodeCopySelf(kSecCSDefaultFlags, &me) == errSecSuccess &&
        SecCodeCopyStaticCode(me, kSecCSDefaultFlags, &st) == errSecSuccess &&
        SecCodeCopySigningInformation(st, kSecCSSigningInformation, &info) == errSecSuccess) {
      NSString *t = ((__bridge NSDictionary *)info)[(__bridge NSString *)kSecCodeInfoTeamIdentifier];
      if (t.length) team = [t copy];
    }
    if (info) CFRelease(info);
    if (st) CFRelease(st);
    if (me) CFRelease(me);
  });
  return team;
}

// The .app this helper's bundle is inside (Hammock.app/Contents/Helpers/X.app), or nil.
static NSString *HammockEnclosingApp(void) {
  NSString *p = NSBundle.mainBundle.bundlePath.stringByDeletingLastPathComponent;  // …/Contents/Helpers
  if (![p.lastPathComponent isEqualToString:@"Helpers"]) return nil;
  p = p.stringByDeletingLastPathComponent.stringByDeletingLastPathComponent;  // …/Hammock.app
  return [p.pathExtension isEqualToString:@"app"] ? p : nil;
}

static BOOL HammockSignedAsHammock(audit_token_t token, NSString *team) {
  NSData *audit = [NSData dataWithBytes:&token length:sizeof token];
  SecCodeRef code = NULL;
  if (SecCodeCopyGuestWithAttributes(NULL, (__bridge CFDictionaryRef) @{(__bridge NSString *)kSecGuestAttributeAudit : audit},
                                     kSecCSDefaultFlags, &code) != errSecSuccess)
    return NO;
  NSString *text = [NSString stringWithFormat:@"anchor apple generic and certificate leaf[subject.OU] = \"%@\" and "
                                              @"(identifier \"hmkd\" or identifier \"com.homegrownsw.hammock\" or "
                                              @"identifier \"com.homegrownsw.hammock.beta\")",
                                              team];
  SecRequirementRef req = NULL;
  BOOL ok = SecRequirementCreateWithString((__bridge CFStringRef)text, kSecCSDefaultFlags, &req) == errSecSuccess &&
            SecCodeCheckValidity(code, kSecCSDefaultFlags, req) == errSecSuccess;
  if (ok) {
    // The hardened runtime: no library injected into it, no debugger attached.
    SecStaticCodeRef st = NULL;
    CFDictionaryRef info = NULL;
    ok = SecCodeCopyStaticCode(code, kSecCSDefaultFlags, &st) == errSecSuccess &&
         SecCodeCopySigningInformation(st, kSecCSDynamicInformation, &info) == errSecSuccess &&
         ([((__bridge NSDictionary *)info)[(__bridge NSString *)kSecCodeInfoFlags] unsignedIntValue] & kSecCodeSignatureRuntime);
    if (info) CFRelease(info);
    if (st) CFRelease(st);
  }
  if (req) CFRelease(req);
  CFRelease(code);
  return ok;
}

// Whether the process at the other end of fd may talk to this helper; why
// (for the log) in *why.
static BOOL HammockPeerAllowed(int fd, pid_t owner, pid_t *peerOut, const char **why) {
  pid_t pid = 0;
  audit_token_t token;
  if (why) *why = "no peer";
  if (!HammockPeerToken(fd, &pid, &token)) return NO;
  if (peerOut) *peerOut = pid;
  if (why) *why = "the daemon that started it";
  if (owner > 0 && pid == owner) return YES;
  NSString *team = HammockOwnTeam();
  if (why) *why = team ? "signed as Hammock" : "this app's own program";
  if (team) return HammockSignedAsHammock(token, team);
  // Ad hoc: the peer's program is this app's own.
  NSString *app = HammockEnclosingApp();
  char path[PROC_PIDPATHINFO_MAXSIZE];
  if (!app || proc_pidpath(pid, path, sizeof path) <= 0) return NO;
  NSString *exe = @(path).stringByResolvingSymlinksInPath;
  NSString *macos = [app stringByAppendingPathComponent:@"Contents/MacOS"].stringByResolvingSymlinksInPath;
  return [exe.stringByDeletingLastPathComponent isEqualToString:macos];
}
