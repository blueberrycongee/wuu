#import "CUAVirtualDisplay.h"

// Private CoreGraphics interfaces. They are only messaged through classes
// resolved with NSClassFromString, so no private class symbol is linked.
@interface CGVirtualDisplayDescriptor : NSObject
@property (retain, nonatomic) dispatch_queue_t queue;
@property (retain, nonatomic) NSString *name;
@property (nonatomic) unsigned int maxPixelsHigh;
@property (nonatomic) unsigned int maxPixelsWide;
@property (nonatomic) CGSize sizeInMillimeters;
@property (nonatomic) unsigned int serialNum;
@property (nonatomic) unsigned int productID;
@property (nonatomic) unsigned int vendorID;
@property (copy, nonatomic) void (^terminationHandler)(id, id);
@end

@interface CGVirtualDisplayMode : NSObject
- (instancetype)initWithWidth:(unsigned int)width height:(unsigned int)height refreshRate:(double)refreshRate;
@end

@interface CGVirtualDisplaySettings : NSObject
@property (retain, nonatomic) NSArray *modes;
@property (nonatomic) unsigned int hiDPI;
@end

@interface CGVirtualDisplay : NSObject
@property (readonly, nonatomic) CGDirectDisplayID displayID;
- (instancetype)initWithDescriptor:(CGVirtualDisplayDescriptor *)descriptor;
- (BOOL)applySettings:(CGVirtualDisplaySettings *)settings;
@end

@implementation CUAVirtualDisplay {
    CGVirtualDisplay *_display;
}

+ (BOOL)isSupported {
    return NSClassFromString(@"CGVirtualDisplayDescriptor") != nil
        && NSClassFromString(@"CGVirtualDisplayMode") != nil
        && NSClassFromString(@"CGVirtualDisplaySettings") != nil
        && NSClassFromString(@"CGVirtualDisplay") != nil;
}

- (nullable instancetype)initWithName:(NSString *)name
                           pointWidth:(uint32_t)pointWidth
                          pointHeight:(uint32_t)pointHeight
                             vendorID:(uint32_t)vendorID
                            productID:(uint32_t)productID
                         serialNumber:(uint32_t)serialNumber
                   terminationHandler:(void (^)(void))terminationHandler {
    if (![CUAVirtualDisplay isSupported] || pointWidth == 0 || pointHeight == 0) return nil;
    self = [super init];
    if (!self) return nil;

    CGVirtualDisplayDescriptor *descriptor = [[NSClassFromString(@"CGVirtualDisplayDescriptor") alloc] init];
    descriptor.queue = dispatch_get_main_queue();
    descriptor.name = name;
    // HiDPI modes are expressed in points; the backing store is twice as dense.
    descriptor.maxPixelsWide = pointWidth * 2;
    descriptor.maxPixelsHigh = pointHeight * 2;
    // About 110 points per inch keeps the reported physical size plausible.
    descriptor.sizeInMillimeters = CGSizeMake(pointWidth * 25.4 / 110.0, pointHeight * 25.4 / 110.0);
    descriptor.vendorID = vendorID;
    descriptor.productID = productID;
    descriptor.serialNum = serialNumber;
    descriptor.terminationHandler = ^(id __unused display, id __unused reason) { terminationHandler(); };

    _display = [[NSClassFromString(@"CGVirtualDisplay") alloc] initWithDescriptor:descriptor];
    if (!_display) return nil;

    CGVirtualDisplaySettings *settings = [[NSClassFromString(@"CGVirtualDisplaySettings") alloc] init];
    settings.hiDPI = 1;
    settings.modes = @[[[NSClassFromString(@"CGVirtualDisplayMode") alloc] initWithWidth:pointWidth height:pointHeight refreshRate:60]];
    if (![_display applySettings:settings]) return nil;
    return self;
}

- (CGDirectDisplayID)displayID {
    return _display.displayID;
}

@end
