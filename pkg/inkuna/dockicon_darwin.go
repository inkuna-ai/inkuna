//go:build darwin

package inkuna

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void inkunaSetDockIcon(const void *bytes, int length) {
	NSData *data = [NSData dataWithBytes:bytes length:(NSUInteger)length];
	NSImage *image = [[NSImage alloc] initWithData:data];
	if (image == nil) {
		return;
	}
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp setApplicationIconImage:image];
	});
}
*/
import "C"

import "unsafe"

// setDockIcon shows icon in the Dock even when the binary runs outside a
// packaged .app bundle, as `wails dev` does. Packaged builds get the same
// image from the bundle's icon file, which Wails derives from
// build/appicon.png.
func setDockIcon(icon []byte) {
	if len(icon) == 0 {
		return
	}
	C.inkunaSetDockIcon(unsafe.Pointer(&icon[0]), C.int(len(icon)))
}
