// This Mac triggers' looks at the Mac (macwatch_darwin.go): its mounted
// volumes, as JSON, and its power.

#import <Foundation/Foundation.h>
#import <DiskArbitration/DiskArbitration.h>
#import <IOKit/ps/IOPowerSources.h>
#import <IOKit/ps/IOPSKeys.h>
#include <stdlib.h>
#include <string.h>

// hmk_volumes is the mounted volumes the Finder could show, as a JSON
// array (malloc'd): each one's path, name, total bytes, bytes available
// (as the Finder counts them: purgeable space included), and what kind of
// disk it is.
char *hmk_volumes(void) {
	@autoreleasepool {
		NSArray<NSURLResourceKey> *keys = @[
			NSURLVolumeNameKey, NSURLVolumeTotalCapacityKey, NSURLVolumeAvailableCapacityForImportantUsageKey,
			NSURLVolumeAvailableCapacityKey, NSURLVolumeIsInternalKey, NSURLVolumeIsRemovableKey,
			NSURLVolumeIsEjectableKey, NSURLVolumeIsLocalKey, NSURLVolumeIsBrowsableKey, NSURLVolumeIsRootFileSystemKey,
		];
		NSArray<NSURL *> *urls = [[NSFileManager defaultManager] mountedVolumeURLsIncludingResourceValuesForKeys:keys options:0];
		DASessionRef session = DASessionCreate(kCFAllocatorDefault);
		NSMutableArray *out = [NSMutableArray array];
		for (NSURL *u in urls) {
			NSDictionary<NSURLResourceKey, id> *v = [u resourceValuesForKeys:keys error:nil];
			if (v == nil) continue;
			NSMutableDictionary *d = [NSMutableDictionary dictionary];
			d[@"path"] = u.path ?: @"";
			d[@"name"] = v[NSURLVolumeNameKey] ?: @"";
			d[@"total"] = v[NSURLVolumeTotalCapacityKey] ?: @0;
			id avail = v[NSURLVolumeAvailableCapacityForImportantUsageKey];
			if (avail == nil || [avail longLongValue] == 0) avail = v[NSURLVolumeAvailableCapacityKey];
			d[@"avail"] = avail ?: @0;
			d[@"internal"] = v[NSURLVolumeIsInternalKey] ?: @NO;
			d[@"removable"] = v[NSURLVolumeIsRemovableKey] ?: @NO;
			d[@"ejectable"] = v[NSURLVolumeIsEjectableKey] ?: @NO;
			d[@"local"] = v[NSURLVolumeIsLocalKey] ?: @NO;
			d[@"browsable"] = v[NSURLVolumeIsBrowsableKey] ?: @NO;
			d[@"root"] = v[NSURLVolumeIsRootFileSystemKey] ?: @NO;
			// A disk image is a "Disk Image" model (or protocol) to Disk Arbitration.
			if (session != NULL) {
				DADiskRef disk = DADiskCreateFromVolumePath(kCFAllocatorDefault, session, (__bridge CFURLRef)u);
				if (disk != NULL) {
					NSDictionary *desc = CFBridgingRelease(DADiskCopyDescription(disk));
					NSString *model = desc[(__bridge NSString *)kDADiskDescriptionDeviceModelKey];
					NSString *protocol = desc[(__bridge NSString *)kDADiskDescriptionDeviceProtocolKey];
					d[@"model"] = [model isKindOfClass:[NSString class]] ? [model stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceCharacterSet] : @"";
					d[@"protocol"] = [protocol isKindOfClass:[NSString class]] ? protocol : @"";
					CFRelease(disk);
				}
			}
			[out addObject:d];
		}
		if (session != NULL) CFRelease(session);
		NSData *json = [NSJSONSerialization dataWithJSONObject:out options:0 error:nil];
		if (json == nil) return strdup("[]");
		char *buf = malloc(json.length + 1);
		memcpy(buf, json.bytes, json.length);
		buf[json.length] = 0;
		return buf;
	}
}

// hmk_power says where the Mac's power comes from: *known 0 when it can't
// tell; *ac 1 on mains power; *battery 1 if it has a battery, and *percent
// how full it is.
void hmk_power(int *known, int *ac, int *battery, int *percent) {
	*known = 0; *ac = 0; *battery = 0; *percent = 0;
	@autoreleasepool {
		CFTypeRef info = IOPSCopyPowerSourcesInfo();
		if (info == NULL) return;
		CFStringRef type = IOPSGetProvidingPowerSourceType(info);
		if (type != NULL) {
			*known = 1;
			*ac = CFStringCompare(type, CFSTR(kIOPMACPowerKey), 0) == kCFCompareEqualTo;
		}
		CFArrayRef list = IOPSCopyPowerSourcesList(info);
		if (list != NULL) {
			for (CFIndex i = 0; i < CFArrayGetCount(list); i++) {
				CFDictionaryRef ps = IOPSGetPowerSourceDescription(info, CFArrayGetValueAtIndex(list, i));
				if (ps == NULL) continue;
				CFStringRef t = CFDictionaryGetValue(ps, CFSTR(kIOPSTypeKey));
				if (t == NULL || CFStringCompare(t, CFSTR(kIOPSInternalBatteryType), 0) != kCFCompareEqualTo) continue;
				CFNumberRef cur = CFDictionaryGetValue(ps, CFSTR(kIOPSCurrentCapacityKey));
				CFNumberRef max = CFDictionaryGetValue(ps, CFSTR(kIOPSMaxCapacityKey));
				int c = 0, m = 0;
				if (cur != NULL) CFNumberGetValue(cur, kCFNumberIntType, &c);
				if (max != NULL) CFNumberGetValue(max, kCFNumberIntType, &m);
				*battery = 1;
				*percent = m > 0 ? (c * 100 + m / 2) / m : c;
				break;
			}
			CFRelease(list);
		}
		CFRelease(info);
	}
}
