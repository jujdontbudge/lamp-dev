#import <Foundation/Foundation.h>
#import <Metal/Metal.h>
#include <fcntl.h>
#include <unistd.h>

int main(int argc, char **argv) {
    @autoreleasepool {
        if (argc != 2) return 2;
        int fd = open(argv[1], O_RDWR);
        unsigned char expected[4096], actual[4096];
        for (int i = 0; i < 4096; i++) expected[i] = (unsigned char)(i * 7 + 11);
        if (fd < 0 || pwrite(fd, expected, sizeof(expected), 0) != sizeof(expected)
            || fsync(fd) || pread(fd, actual, sizeof(actual), 0) != sizeof(actual)
            || memcmp(actual, expected, sizeof(actual))) return 3;
        close(fd);
        id<MTLDevice> device = MTLCreateSystemDefaultDevice();
        if (!device) return 4;
        NSError *error = nil;
        NSString *source = @"#include <metal_stdlib>\nusing namespace metal;\nkernel void fill(device uint *out [[buffer(0)]], uint i [[thread_position_in_grid]]) { out[i] = i * 3 + 7; }";
        id<MTLLibrary> library = [device newLibraryWithSource:source options:nil error:&error];
        if (!library) { NSLog(@"%@", error); return 5; }
        id<MTLComputePipelineState> pipeline = [device newComputePipelineStateWithFunction:[library newFunctionWithName:@"fill"] error:&error];
        if (!pipeline) return 6;
        id<MTLBuffer> buffer = [device newBufferWithLength:64 * sizeof(uint32_t) options:MTLResourceStorageModeShared];
        id<MTLCommandQueue> queue = [device newCommandQueue];
        id<MTLCommandBuffer> command = [queue commandBuffer];
        id<MTLComputeCommandEncoder> encoder = [command computeCommandEncoder];
        [encoder setComputePipelineState:pipeline];
        [encoder setBuffer:buffer offset:0 atIndex:0];
        [encoder dispatchThreads:MTLSizeMake(64, 1, 1) threadsPerThreadgroup:MTLSizeMake(64, 1, 1)];
        [encoder endEncoding];
        [command commit];
        [command waitUntilCompleted];
        if (command.status != MTLCommandBufferStatusCompleted) return 7;
        uint32_t *values = buffer.contents;
        for (int i = 0; i < 64; i++) if (values[i] != i * 3 + 7) return 8;
        NSDictionary *result = @{
            @"extra_disk_roundtrip_bytes": @4096,
            @"metal_device": device.name,
            @"metal_verified_values": @64,
            @"metal_last_value": @(values[63])
        };
        NSData *json = [NSJSONSerialization dataWithJSONObject:result options:0 error:&error];
        puts([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding].UTF8String);
    }
}
