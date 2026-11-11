#import <AppKit/AppKit.h>
#include <spawn.h>
#include <fcntl.h>
#include <crt_externs.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <sys/stat.h>

@interface LampInputWindow : NSWindow
@end
@implementation LampInputWindow
- (BOOL)canBecomeKeyWindow { return YES; }
@end

@interface LampInputView : NSView
@property(strong) NSMutableArray *events;
@property(strong) NSMutableString *text;
@property(strong) NSString *directory;
@property(strong) NSMutableArray *pending;
@end

@implementation LampInputView
- (BOOL)acceptsFirstResponder { return YES; }
- (void)drawRect:(NSRect)dirtyRect {
    [[NSColor colorWithRed:0.08 green:0.2 blue:0.45 alpha:1] setFill];
    NSRectFill(self.bounds);
    NSDictionary *style = @{NSForegroundColorAttributeName:NSColor.whiteColor,
                             NSFontAttributeName:[NSFont systemFontOfSize:24]};
    [@"Lamp private macOS guest" drawAtPoint:NSMakePoint(24, 390) withAttributes:style];
    [[@"Typed: " stringByAppendingString:self.text] drawAtPoint:NSMakePoint(24, 330) withAttributes:style];
    [[NSString stringWithFormat:@"Real input events: %lu", self.events.count] drawAtPoint:NSMakePoint(24, 280) withAttributes:style];
}
- (void)save {
    [self setNeedsDisplay:YES];
    [self displayIfNeeded];
    NSDictionary *state = @{@"backend":@"guest-AppKit-event-queue", @"events":self.events,
                            @"text":self.text, @"pid":@(getpid()), @"window_number":@(self.window.windowNumber),
                            @"display":@{@"id":@(CGMainDisplayID()),@"pixel_width":@(CGDisplayPixelsWide(CGMainDisplayID())),@"pixel_height":@(CGDisplayPixelsHigh(CGMainDisplayID()))}};
    NSData *json = [NSJSONSerialization dataWithJSONObject:state options:0 error:nil];
    [json writeToFile:[self.directory stringByAppendingPathComponent:@"events.json"] atomically:YES];
    NSBitmapImageRep *image = [self bitmapImageRepForCachingDisplayInRect:self.bounds];
    [self cacheDisplayInRect:self.bounds toBitmapImageRep:image];
    [[image representationUsingType:NSBitmapImageFileTypePNG properties:@{}]
      writeToFile:[self.directory stringByAppendingPathComponent:@"display.png"] atomically:YES];
    for(NSDictionary *request in self.pending.copy) {
        if(self.events.count<[request[@"until"] unsignedIntegerValue])continue;
        NSData *reply=[NSJSONSerialization dataWithJSONObject:@{@"queued":request[@"count"],@"observed":request[@"count"]} options:0 error:nil];
        int fd=[request[@"fd"] intValue];
        send(fd,reply.bytes,reply.length,0);
        close(fd);
        [self.pending removeObject:request];
    }
}
- (void)keyDown:(NSEvent *)event {
    [self.events addObject:@{@"kind":@"key", @"code":@(event.keyCode), @"down":@YES, @"timestamp":@(event.timestamp)}];
    [self.text appendString:event.characters ?: @""];
    [self save];
}
- (void)keyUp:(NSEvent *)event {
    [self.events addObject:@{@"kind":@"key", @"code":@(event.keyCode), @"down":@NO, @"timestamp":@(event.timestamp)}];
    [self save];
}
- (void)mouseMoved:(NSEvent *)event {
    [self.events addObject:@{@"kind":@"pointer", @"x":@(event.locationInWindow.x), @"y":@(event.locationInWindow.y),
                            @"timestamp":@(event.timestamp)}];
    [self save];
}
@end

int main(int argc, char **argv) {
    if (argc>=4 && strcmp(argv[1],"--detach")==0) {
        posix_spawnattr_t attr;
        posix_spawn_file_actions_t files;
        posix_spawnattr_init(&attr);
        posix_spawnattr_setflags(&attr,POSIX_SPAWN_SETPGROUP);
        posix_spawnattr_setpgroup(&attr,0);
        posix_spawn_file_actions_init(&files);
        posix_spawn_file_actions_addopen(&files,0,"/dev/null",O_RDONLY,0);
        posix_spawn_file_actions_addopen(&files,1,argv[2],O_WRONLY|O_CREAT|O_EXCL,0600);
        posix_spawn_file_actions_adddup2(&files,1,2);
        pid_t pid=0;
        int error=posix_spawnp(&pid,argv[3],&files,&attr,&argv[3],*_NSGetEnviron());
        posix_spawnattr_destroy(&attr);
        posix_spawn_file_actions_destroy(&files);
        if(error){fprintf(stderr,"detach failed: %s\n",strerror(error));return 6;}
        printf("%d\n",pid);
        return 0;
    }
    if (argc != 2) return 2;
    @autoreleasepool {
        if (strcmp(argv[1], "--kcpassword") == 0) {
            unsigned char bytes[128] = {0};
            size_t n = fread(bytes, 1, sizeof(bytes), stdin);
            if (n == 0 || n >= sizeof(bytes)) return 4;
            size_t length = ((n + 12) / 12) * 12;
            NSMutableData *encoded = [NSMutableData dataWithLength:length];
            unsigned char key[] = {0x7d,0x89,0x52,0x23,0xd2,0xbc,0xdd,0xea,0xa3,0xb9,0x1f};
            unsigned char *out = encoded.mutableBytes;
            for (size_t i=0; i<length; i++) out[i]=(i<n ? bytes[i] : 0)^key[i%sizeof(key)];
            return [encoded writeToFile:@"/etc/kcpassword" atomically:YES] ? 0 : 5;
        }
        [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyAccessory];
        NSString *directory = [NSString stringWithUTF8String:argv[1]];
        if (![NSFileManager.defaultManager createDirectoryAtPath:directory withIntermediateDirectories:NO
              attributes:@{NSFilePosixPermissions:@0700} error:nil]) return 3;
        NSWindow *window = [[LampInputWindow alloc] initWithContentRect:NSMakeRect(0, 0, 640, 480)
                             styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
        LampInputView *view = [[LampInputView alloc] initWithFrame:NSMakeRect(0, 0, 640, 480)];
        view.directory = directory;
        view.events = [NSMutableArray new];
        view.text = [NSMutableString new];
        view.pending=[NSMutableArray new];
        window.contentView = view;
        window.acceptsMouseMovedEvents = YES;
        [window makeFirstResponder:view];
        [NSApp activateIgnoringOtherApps:YES];
        [window makeKeyAndOrderFront:nil];
        NSString *socketPath=[directory stringByAppendingPathComponent:@"input.sock"];
        int listener=socket(AF_UNIX,SOCK_STREAM,0);
        struct sockaddr_un address={.sun_len=sizeof(address),.sun_family=AF_UNIX};
        if(listener<0||strlen(socketPath.fileSystemRepresentation)>=sizeof(address.sun_path))return 7;
        strcpy(address.sun_path,socketPath.fileSystemRepresentation);
        if(bind(listener,(struct sockaddr *)&address,sizeof(address))||chmod(address.sun_path,0600)||listen(listener,4))return 8;
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{
            for(;;) {
                int fd=accept(listener,NULL,NULL);
                if(fd<0)break;
                struct timeval timeout={.tv_sec=10};
                setsockopt(fd,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));
                setsockopt(fd,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout));
                int noSignal=1;
                setsockopt(fd,SOL_SOCKET,SO_NOSIGPIPE,&noSignal,sizeof(noSignal));
                unsigned char bytes[16385];
                size_t used=0;
                ssize_t n=0;
                while(used<sizeof(bytes)&&(n=read(fd,bytes+used,sizeof(bytes)-used))>0)used+=(size_t)n;
                NSDictionary *request=used<sizeof(bytes)?[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:bytes length:used] options:0 error:nil]:nil;
                NSArray *events=[request isKindOfClass:NSDictionary.class]?request[@"events"]:nil;
                if(![events isKindOfClass:NSArray.class]||events.count==0||events.count>32){close(fd);continue;}
                dispatch_async(dispatch_get_main_queue(),^{
                    [view.pending addObject:@{@"fd":@(fd),@"until":@(view.events.count+events.count),@"count":@(events.count)}];
                    for(NSDictionary *input in events) {
                        NSEvent *event=nil;
                        if([input[@"kind"] isEqual:@"key"]) {
                            unsigned short code=[input[@"code"] unsignedShortValue];
                            BOOL down=[input[@"down"] boolValue];
                            CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);
                            CGEventRef key=CGEventCreateKeyboardEvent(source,code,down);
                            CGEventSetFlags(key,0);
                            NSEvent *native=[NSEvent eventWithCGEvent:key];
                            event=[NSEvent keyEventWithType:down?NSEventTypeKeyDown:NSEventTypeKeyUp location:NSZeroPoint modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:window.windowNumber context:nil characters:native.characters?:@"" charactersIgnoringModifiers:native.charactersIgnoringModifiers?:@"" isARepeat:NO keyCode:code];
                            CFRelease(key);CFRelease(source);
                        }else {
                            NSPoint point=NSMakePoint([input[@"x"] doubleValue],[input[@"y"] doubleValue]);
                            event=[NSEvent mouseEventWithType:NSEventTypeMouseMoved location:point modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:window.windowNumber context:nil eventNumber:0 clickCount:0 pressure:0];
                        }
                        [NSApp postEvent:event atStart:NO];
                    }
                });
            }
        });
        [view save];
        [NSApp run];
    }
    return 0;
}
