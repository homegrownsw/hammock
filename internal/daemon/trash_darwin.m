// Moving to the Trash for files panes (trash_darwin.go): the Finder's own
// way, so Put Back works.

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// hmk_trash moves path to the Trash: where it went there (malloc'd), or NULL
// and *err (malloc'd).
char *hmk_trash(const char *path, char **err) {
	@autoreleasepool {
		NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
		NSURL *out = nil;
		NSError *e = nil;
		if (![[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:&out error:&e]) {
			const char *why = e.localizedDescription.UTF8String;
			*err = strdup(why ? why : "it couldn't be moved");
			return NULL;
		}
		return strdup(out.path.UTF8String ? out.path.UTF8String : "");
	}
}
