//go:build darwin && cgo

// The Dock half of the app: just enough Cocoa for Derby and Ales to behave like
// a Mac application. Everything else — the server, the race, the pages — is Go.
//
// Three things are handled here and nothing more:
//   - Quit (from the Dock, the menu bar or ⌘Q) stops the server cleanly before
//     the process ends, so the database is closed and the displays are told.
//   - Clicking the Dock icon brings the coordinator page back up in the browser,
//     because there is no window of our own to show.
//   - The Dock menu offers the same, and the devices page.

#import <Cocoa/Cocoa.h>
#include "_cgo_export.h"

@interface DNAAppDelegate : NSObject <NSApplicationDelegate>
@property BOOL serverStopped;
@property BOOL quitPending;
@end

@implementation DNAAppDelegate

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    // The server has already stopped (Quit on the Status page): nothing to wait for.
    if (self.serverStopped) {
        return NSTerminateNow;
    }
    // Ask Go to shut the server down, and finish quitting when it says it has.
    if (!self.quitPending) {
        self.quitPending = YES;
        dnaQuitRequested();
    }
    return NSTerminateLater;
}

- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)flag {
    dnaOpenCoordinator();
    return NO;
}

- (NSMenu *)applicationDockMenu:(NSApplication *)sender {
    NSMenu *menu = [[NSMenu alloc] init];
    [menu addItemWithTitle:@"Open Derby and Ales" action:@selector(openCoordinator:) keyEquivalent:@""];
    [menu addItemWithTitle:@"Open the devices page" action:@selector(openDevices:) keyEquivalent:@""];
    for (NSMenuItem *item in menu.itemArray) {
        item.target = self;
    }
    return menu;
}

- (void)openCoordinator:(id)sender { dnaOpenCoordinator(); }
- (void)openDevices:(id)sender { dnaOpenDevices(); }

@end

static DNAAppDelegate *dnaDelegate;

// dnaRunApp takes over the main thread with the Cocoa event loop. It returns
// only when the process exits.
void dnaRunApp(void) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];

        dnaDelegate = [[DNAAppDelegate alloc] init];
        app.delegate = dnaDelegate;

        // A menu bar with the usual Quit, so ⌘Q works like any other app.
        NSMenu *bar = [[NSMenu alloc] init];
        NSMenuItem *appItem = [[NSMenuItem alloc] init];
        [bar addItem:appItem];
        NSMenu *appMenu = [[NSMenu alloc] init];
        NSMenuItem *open = [appMenu addItemWithTitle:@"Open Derby and Ales"
                                               action:@selector(openCoordinator:)
                                        keyEquivalent:@"o"];
        open.target = dnaDelegate;
        NSMenuItem *devices = [appMenu addItemWithTitle:@"Open the Devices Page"
                                                  action:@selector(openDevices:)
                                           keyEquivalent:@""];
        devices.target = dnaDelegate;
        [appMenu addItem:[NSMenuItem separatorItem]];
        [appMenu addItemWithTitle:@"Quit Derby and Ales"
                           action:@selector(terminate:)
                    keyEquivalent:@"q"];
        appItem.submenu = appMenu;
        app.mainMenu = bar;

        [app run];
    }
}

// dnaServerStopped is called by Go once the server has shut down, for whatever
// reason. If a Quit is waiting on it, it goes ahead; otherwise the app quits,
// because an app whose server has stopped has nothing left to do.
void dnaServerStopped(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        dnaDelegate.serverStopped = YES;
        if (dnaDelegate.quitPending) {
            [NSApp replyToApplicationShouldTerminate:YES];
        } else {
            [NSApp terminate:nil];
        }
    });
}
