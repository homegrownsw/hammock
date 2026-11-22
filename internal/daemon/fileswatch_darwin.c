// FSEvents for files panes (fileswatch_darwin.go): a stream over a pane's
// folder (and its repository's .git), reporting folders whose entries
// changed. Here rather than in the Go file's preamble, which can't define
// functions alongside an //export.

#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include <stdint.h>

extern void hmkFilesEvent(uintptr_t handle, char *path, unsigned int flags);

static void hmk_files_cb(ConstFSEventStreamRef stream, void *info, size_t n, void *paths,
                         const FSEventStreamEventFlags flags[], const FSEventStreamEventId ids[]) {
	char **p = (char **)paths;
	for (size_t i = 0; i < n; i++) {
		hmkFilesEvent((uintptr_t)info, p[i], (unsigned int)flags[i]);
	}
}

void *hmk_files_start(uintptr_t handle, const char **paths, int n) {
	CFMutableArrayRef arr = CFArrayCreateMutable(NULL, n, &kCFTypeArrayCallBacks);
	for (int i = 0; i < n; i++) {
		CFStringRef s = CFStringCreateWithCString(NULL, paths[i], kCFStringEncodingUTF8);
		if (s) {
			CFArrayAppendValue(arr, s);
			CFRelease(s);
		}
	}
	FSEventStreamContext ctx = {0, (void *)handle, NULL, NULL, NULL};
	FSEventStreamRef st = FSEventStreamCreate(NULL, hmk_files_cb, &ctx, arr, kFSEventStreamEventIdSinceNow, 0.05,
	                                          kFSEventStreamCreateFlagNoDefer);
	CFRelease(arr);
	if (!st) {
		return NULL;
	}
	dispatch_queue_t q = dispatch_queue_create("hammock.files", DISPATCH_QUEUE_SERIAL);
	FSEventStreamSetDispatchQueue(st, q);
	dispatch_release(q); // the stream keeps it
	if (!FSEventStreamStart(st)) {
		FSEventStreamInvalidate(st);
		FSEventStreamRelease(st);
		return NULL;
	}
	return (void *)st;
}

void hmk_files_stop(void *stream) {
	FSEventStreamRef st = (FSEventStreamRef)stream;
	FSEventStreamStop(st);
	FSEventStreamInvalidate(st);
	FSEventStreamRelease(st);
}
