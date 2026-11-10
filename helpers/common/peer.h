// Who may talk to a helper. A helper's socket sits in the state dir
// (0700), so only you reach it; but anything of yours would then drive
// its simulators or pages, or borrow its grants (Screen Recording,
// Accessibility), past hmkd's checks (an agent in a pane running nc). Nor
// can a helper go by what its command line says: anything of yours can run
// the helper itself, with a socket and an "owner" of its own choosing. So a
// helper goes by what the process at the other end is, from the socket's
// audit token (LOCAL_PEERTOKEN), and takes a connection only from those of
// Hammock's programs it names (HammockPeerKinds):
//   - hmkd (any of them, so a restarted daemon takes over its helpers), and
//   - the Hammock app (it connects to the browser and simulator hosts for
//     live layers and input; never to the screen host),
// signed by this helper's own team, with the hardened runtime (nothing
// injected into it, no debugger attached).
//
// What that proves, and what it doesn't. A unix socket's audit token isn't
// fixed as the peer connects: macOS works it out when asked, from the pid
// that last used the peer's end, as that pid is now. So it rules out a
// pid reused by an unrelated process, or one whose program changed after
// the check, but not a process that connects, writes, hands the socket to
// a forked child and then replaces itself with a suspended (never running)
// copy of hmkd or the app (POSIX_SPAWN_SETEXEC|POSIX_SPAWN_START_SUSPENDED),
// with the check timed after that (it can stop and continue the helper:
// the same user). That passes, and the child goes on talking. Checked on
// macOS 26 against the screen host: it then served the impostor's child.
// So this check keeps out a program of yours that merely connects (an
// agent running nc), and no more. Where that isn't enough, a helper also
// needs a proof the socket can't forge: the screen host, holding Screen
// Recording and Accessibility, serves only a client that answers its
// challenge under a secret it got over Mach from the daemon that started it
// (helpers/common/rendezvous.h, internal/daemon/screenauth.go), and keeps
// this check as well. The browser and simulator hosts hold no privacy
// grants, and the app also connects to them, so they go by this check
// alone: a program of yours that goes to that length can drive their pages
// and simulators, which it could also do as you in other ways.
//
// A helper with no team (an ad-hoc development build) has no signature to
// check against, so there it goes by where the peer's program is: hmkd, or
// the app's own program (its Info.plist's CFBundleExecutable), in the .app
// this helper is inside (Contents/MacOS), with the hardened runtime if this
// helper has it; or, for a helper not in an app (bin/, `just dev-daemon`),
// the hmkd beside it. That bundle or folder is yours to write, so the
// check is weaker there: release and TestFlight builds go by team.
//
// Header only: include it in one file of a helper, and link Security.
#pragma once

#import <Foundation/Foundation.h>
#import <Security/Security.h>
#include <libproc.h>
#include <sys/socket.h>
#include <sys/un.h>

// The Hammock programs a helper takes connections from.
typedef enum {
  HammockPeerDaemon = 1 << 0,  // hmkd
  HammockPeerApp = 1 << 1,     // Hammock.app (or Hammock β)
} HammockPeerKind;
typedef unsigned HammockPeerKinds;

// The connection's other end: its pid (for the log) and audit token (as
// macOS works it out now, from the last process to use the other end: see
// the top of this file).
static BOOL HammockPeerToken(int fd, pid_t *pid, audit_token_t *token) {
  socklen_t n = sizeof *pid;
  if (getsockopt(fd, SOL_LOCAL, LOCAL_PEERPID, pid, &n) != 0) return NO;
  n = sizeof *token;
  return getsockopt(fd, SOL_LOCAL, LOCAL_PEERTOKEN, token, &n) == 0 && n == sizeof *token;
}

// This process's team (nil ad hoc), and whether it runs with the hardened
// runtime.
static NSString *HammockOwnTeam(BOOL *runtime) {
  static NSString *team;
  static BOOL hardened;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    SecCodeRef me = NULL;
    SecStaticCodeRef st = NULL;
    CFDictionaryRef info = NULL;
    if (SecCodeCopySelf(kSecCSDefaultFlags, &me) == errSecSuccess &&
        SecCodeCopyStaticCode(me, kSecCSDefaultFlags, &st) == errSecSuccess &&
        SecCodeCopySigningInformation(st, kSecCSSigningInformation | kSecCSDynamicInformation, &info) == errSecSuccess) {
      NSDictionary *d = (__bridge NSDictionary *)info;
      NSString *t = d[(__bridge NSString *)kSecCodeInfoTeamIdentifier];
      if (t.length) team = [t copy];
      hardened = ([d[(__bridge NSString *)kSecCodeInfoFlags] unsignedIntValue] & kSecCodeSignatureRuntime) != 0;
    }
    if (info) CFRelease(info);
    if (st) CFRelease(st);
    if (me) CFRelease(me);
  });
  if (runtime) *runtime = hardened;
  return team;
}

// The .app this helper's bundle is inside (Hammock.app/Contents/Helpers/X.app), or nil.
static NSString *HammockEnclosingApp(void) {
  NSString *p = NSBundle.mainBundle.bundlePath.stringByDeletingLastPathComponent;  // …/Contents/Helpers
  if (![p.lastPathComponent isEqualToString:@"Helpers"]) return nil;
  p = p.stringByDeletingLastPathComponent.stringByDeletingLastPathComponent;  // …/Hammock.app
  return [p.pathExtension isEqualToString:@"app"] ? p : nil;
}

// The running code the token names, validly signed and meeting req (nil:
// any valid signature), with the hardened runtime if runtime is set. By
// token, so a token whose process has exec'd since it was taken (a new pid
// version) isn't found: what makes a Mach message's audit trailer a proof
// of who sent it. A socket's token is taken when asked, so for it this
// only says what that process runs now (see the top of this file).
static BOOL HammockTokenMeets(audit_token_t token, NSString *req, BOOL runtime) {
  NSData *audit = [NSData dataWithBytes:&token length:sizeof token];
  SecCodeRef code = NULL;
  if (SecCodeCopyGuestWithAttributes(NULL, (__bridge CFDictionaryRef) @{(__bridge NSString *)kSecGuestAttributeAudit : audit},
                                     kSecCSDefaultFlags, &code) != errSecSuccess)
    return NO;
  SecRequirementRef r = NULL;
  BOOL ok = !req || SecRequirementCreateWithString((__bridge CFStringRef)req, kSecCSDefaultFlags, &r) == errSecSuccess;
  ok = ok && SecCodeCheckValidity(code, kSecCSDefaultFlags, r) == errSecSuccess;
  if (ok && runtime) {
    // The hardened runtime: no library injected into it, no debugger attached.
    SecStaticCodeRef st = NULL;
    CFDictionaryRef info = NULL;
    ok = SecCodeCopyStaticCode(code, kSecCSDefaultFlags, &st) == errSecSuccess &&
         SecCodeCopySigningInformation(st, kSecCSDynamicInformation, &info) == errSecSuccess &&
         ([((__bridge NSDictionary *)info)[(__bridge NSString *)kSecCodeInfoFlags] unsignedIntValue] & kSecCodeSignatureRuntime);
    if (info) CFRelease(info);
    if (st) CFRelease(st);
  }
  if (r) CFRelease(r);
  CFRelease(code);
  return ok;
}

// Whether the token's process is one of kinds, signed by team with the
// hardened runtime.
static BOOL HammockSignedAsHammock(audit_token_t token, NSString *team, HammockPeerKinds kinds) {
  NSMutableArray *ids = [NSMutableArray array];
  if (kinds & HammockPeerDaemon) [ids addObject:@"identifier \"hmkd\""];
  if (kinds & HammockPeerApp) {
    [ids addObject:@"identifier \"com.homegrownsw.hammock\""];
    [ids addObject:@"identifier \"com.homegrownsw.hammock.beta\""];
  }
  if (!ids.count || !team.length) return NO;
  NSString *req = [NSString stringWithFormat:@"anchor apple generic and certificate leaf[subject.OU] = \"%@\" and (%@)", team,
                                             [ids componentsJoinedByString:@" or "]];
  return HammockTokenMeets(token, req, YES);
}

// Ad hoc: whether the token's program is one of kinds, by where it is (see
// the top of this file).
static BOOL HammockOwnProgram(audit_token_t token, HammockPeerKinds kinds) {
  char path[PROC_PIDPATHINFO_MAXSIZE];
  if (proc_pidpath_audittoken(&token, path, sizeof path) <= 0) return NO;
  NSString *exe = @(path).stringByResolvingSymlinksInPath;
  NSMutableArray<NSString *> *allowed = [NSMutableArray array];
  NSString *app = HammockEnclosingApp();
  if (app) {
    NSString *macos = [app stringByAppendingPathComponent:@"Contents/MacOS"];
    if (kinds & HammockPeerDaemon) [allowed addObject:[macos stringByAppendingPathComponent:@"hmkd"]];
    if (kinds & HammockPeerApp) {
      NSString *name = [NSBundle bundleWithPath:app].infoDictionary[@"CFBundleExecutable"];
      if (![name isKindOfClass:NSString.class] || !name.length || [name containsString:@"/"]) name = @"Hammock";
      [allowed addObject:[macos stringByAppendingPathComponent:name]];
    }
  } else if (kinds & HammockPeerDaemon) {
    // A helper built into bin/ (helpers/*/build.sh), run by the hmkd there.
    NSString *dir = NSBundle.mainBundle.bundlePath.stringByDeletingLastPathComponent;
    [allowed addObject:[dir stringByAppendingPathComponent:@"hmkd"]];
  }
  BOOL found = NO;
  for (NSString *p in allowed) found = found || [exe isEqualToString:p.stringByResolvingSymlinksInPath];
  if (!found) return NO;
  BOOL runtime = NO;
  HammockOwnTeam(&runtime);
  return !runtime || HammockTokenMeets(token, nil, YES);
}

// Whether the process at the other end of fd is one of kinds, so may talk
// to this helper; why (for the log) in *why.
static BOOL HammockPeerAllowed(int fd, HammockPeerKinds kinds, pid_t *peerOut, const char **why) {
  pid_t pid = 0;
  audit_token_t token;
  if (why) *why = "no peer";
  if (!HammockPeerToken(fd, &pid, &token)) return NO;
  if (peerOut) *peerOut = pid;
  NSString *team = HammockOwnTeam(NULL);
  if (team) {
    if (why) *why = "signed as Hammock";
    return HammockSignedAsHammock(token, team, kinds);
  }
  if (why) *why = "this build's own program (ad hoc)";
  return HammockOwnProgram(token, kinds);
}
