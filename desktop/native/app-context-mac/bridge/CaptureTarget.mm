#include <node_api.h>
#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#include <cstring>
#include <unistd.h>

// Metadata only. Synchronous shortcut pinning and permission-guide positioning
// must not wait for a capture helper process or require screen/AX access.
static NSString *LaunchIdentity(NSRunningApplication *application) {
  NSDate *date = application.launchDate;
  if (!date) return nil;
  double time = date.timeIntervalSinceReferenceDate;
  uint64_t bits;
  std::memcpy(&bits, &time, sizeof(bits));
  return [NSString stringWithFormat:@"%016llx", (unsigned long long)bits];
}

static napi_value Fail(napi_env env, const char *message) {
  napi_throw_error(env, nullptr, message);
  return nullptr;
}

static napi_value PinTarget(napi_env env, napi_callback_info) {
  @autoreleasepool {
    if (![NSThread isMainThread]) return Fail(env, "App snapshots must start on the main thread.");
    NSRunningApplication *application = NSWorkspace.sharedWorkspace.frontmostApplication;
    NSString *launch = LaunchIdentity(application);
    if (!application || application.terminated || !launch || application.processIdentifier == getpid()
        || [application.bundleIdentifier isEqualToString:@"com.blueberrycongee.wuu"]
        || [application.bundleIdentifier isEqualToString:@"com.blueberrycongee.wuu.app-context"]) {
      return Fail(env, "Switch to the app you want to capture, then press the snapshot shortcut.");
    }
    pid_t pid = application.processIdentifier;
    NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
    for (NSDictionary *window in windows) {
      if ([window[(id)kCGWindowOwnerPID] intValue] != pid || [window[(id)kCGWindowLayer] intValue] != 0) continue;
      CGRect frame;
      NSDictionary *bounds = window[(id)kCGWindowBounds];
      NSNumber *number = window[(id)kCGWindowNumber];
      if (!bounds || !number || !CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)bounds, &frame)
          || frame.size.width <= 1 || frame.size.height <= 1
          || (window[(id)kCGWindowAlpha] && [window[(id)kCGWindowAlpha] doubleValue] <= 0)) continue;
      NSRunningApplication *current = NSWorkspace.sharedWorkspace.frontmostApplication;
      if (current.processIdentifier != pid || current.terminated || ![LaunchIdentity(current) isEqualToString:launch]) {
        return Fail(env, "The frontmost app changed while identifying the capture target. Try again.");
      }
      // No title, text, pixels, input events, or background observations.
      NSDictionary *target = @{ @"pid": @(pid), @"launch_identity": launch, @"window_id": number,
        @"frame": @[@(frame.origin.x), @(frame.origin.y), @(frame.size.width), @(frame.size.height)] };
      NSData *json = [NSJSONSerialization dataWithJSONObject:target options:0 error:nullptr];
      if (!json) return Fail(env, "The app capture target could not be identified.");
      napi_value result;
      if (napi_create_string_utf8(env, (const char *)json.bytes, json.length, &result) != napi_ok) return nullptr;
      return result;
    }
    return Fail(env, "The frontmost app has no visible window to capture.");
  }
}

static napi_value PermissionGuideWindow(napi_env env, napi_callback_info) {
  @autoreleasepool {
    if (![NSThread isMainThread]) return Fail(env, "Permission guide positioning requires the main thread.");
    napi_value result;
    if (napi_get_null(env, &result) != napi_ok) return nullptr;
    NSRunningApplication *settings = [NSRunningApplication runningApplicationsWithBundleIdentifier:@"com.apple.systempreferences"].firstObject;
    pid_t foreground = NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier;
    if (!settings || (foreground != settings.processIdentifier && foreground != getpid())) return result;
    NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
    NSDictionary *selected = nil;
    CGRect frame = CGRectZero;
    for (NSDictionary *window in windows) {
      if ([window[(id)kCGWindowOwnerPID] intValue] != settings.processIdentifier
          || (window[(id)kCGWindowAlpha] && [window[(id)kCGWindowAlpha] doubleValue] <= 0)) continue;
      CGRect candidate;
      NSDictionary *bounds = window[(id)kCGWindowBounds];
      // macOS also exposes tiny title-bar controls as separate layer-0 windows
      // (for example, the 66x20 traffic-light group). They cannot host a dialog.
      if (!bounds || !CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)bounds, &candidate)
          || candidate.size.width < 100 || candidate.size.height < 100) continue;
      // Settings normally has one content window. Authentication dialogs and file
      // pickers can share its PID and layer 0; never anchor to one of those.
      // Count other layers too, so a floating sheet also hides the guide.
      if (selected) return result;
      selected = window;
      frame = candidate;
    }
    if (!selected || [selected[(id)kCGWindowLayer] intValue] != 0) return result;
    if (napi_create_object(env, &result) != napi_ok) return nullptr;
    const char *keys[] = { "x", "y", "width", "height" };
    double values[] = { frame.origin.x, frame.origin.y, frame.size.width, frame.size.height };
    for (size_t i = 0; i < 4; ++i) {
      napi_value value;
      if (napi_create_double(env, values[i], &value) != napi_ok
          || napi_set_named_property(env, result, keys[i], value) != napi_ok) return nullptr;
    }
    return result;
  }
}

static napi_value Initialize(napi_env env, napi_value exports) {
  napi_value pin, guide;
  if (napi_create_function(env, "pinTarget", NAPI_AUTO_LENGTH, PinTarget, nullptr, &pin) != napi_ok
      || napi_set_named_property(env, exports, "pinTarget", pin) != napi_ok
      || napi_create_function(env, "permissionGuideWindow", NAPI_AUTO_LENGTH, PermissionGuideWindow, nullptr, &guide) != napi_ok
      || napi_set_named_property(env, exports, "permissionGuideWindow", guide) != napi_ok) return nullptr;
  return exports;
}
NAPI_MODULE(wuu_capture_target, Initialize)
