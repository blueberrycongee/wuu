#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// Owns one CoreGraphics virtual display for the lifetime of the object.
///
/// CGVirtualDisplay is private CoreGraphics API. Classes are resolved at run
/// time so a macOS release without it disables the agent display instead of
/// preventing the helper from launching.
@interface CUAVirtualDisplay : NSObject

@property (nonatomic, readonly) CGDirectDisplayID displayID;

+ (BOOL)isSupported;

- (nullable instancetype)initWithName:(NSString *)name
                           pointWidth:(uint32_t)pointWidth
                          pointHeight:(uint32_t)pointHeight
                             vendorID:(uint32_t)vendorID
                            productID:(uint32_t)productID
                         serialNumber:(uint32_t)serialNumber
                   terminationHandler:(void (^)(void))terminationHandler NS_DESIGNATED_INITIALIZER;

- (instancetype)init NS_UNAVAILABLE;

@end

NS_ASSUME_NONNULL_END
