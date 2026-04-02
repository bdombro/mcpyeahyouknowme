#import <Foundation/Foundation.h>
#import <Vision/Vision.h>
#import <CoreGraphics/CoreGraphics.h>
#import <AppKit/AppKit.h>
#import <ImageIO/ImageIO.h>
#include <stdlib.h>

// analyze_image runs VNRecognizeTextRequest and VNClassifyImageRequest on the image at path.
// Returns a malloc'd JSON string {"ocr_text":"...","labels":["...",...]} or "error:<msg>".
// Caller must free the returned string.
char* analyze_image(const char* path) {
    @autoreleasepool {
        if (path == NULL) {
            return strdup("error: empty image path");
        }

        NSString *nsPath = [NSString stringWithUTF8String:path];
        if (nsPath == nil) {
            return strdup("error: invalid image path");
        }
        NSURL *url = [NSURL fileURLWithPath:nsPath];

        // Load image via CGImageSource for broad format support.
        CGImageSourceRef source = CGImageSourceCreateWithURL((__bridge CFURLRef)url, NULL);
        if (!source) {
            return strdup("error: could not load image");
        }
        CGImageRef cgImage = CGImageSourceCreateImageAtIndex(source, 0, NULL);
        CFRelease(source);
        if (!cgImage) {
            return strdup("error: could not decode image");
        }

        VNRecognizeTextRequest *ocrReq = [[VNRecognizeTextRequest alloc] init];
        ocrReq.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
        ocrReq.automaticallyDetectsLanguage = YES;
        ocrReq.usesLanguageCorrection = YES;

        VNClassifyImageRequest *classReq = [[VNClassifyImageRequest alloc] init];

        VNImageRequestHandler *handler = [[VNImageRequestHandler alloc] initWithCGImage:cgImage options:@{}];
        NSError *handlerErr = nil;
        BOOL success = NO;
        @try {
            success = [handler performRequests:@[ocrReq, classReq] error:&handlerErr];
        } @catch (NSException *exception) {
            CGImageRelease(cgImage);
            NSString *message = [NSString stringWithFormat:@"error: vision exception: %@", exception.reason ?: @"unknown"];
            return strdup(message.UTF8String);
        }
        if (!success || handlerErr != nil) {
            CGImageRelease(cgImage);
            NSString *message = handlerErr.localizedDescription ?: @"vision request failed";
            NSString *err = [NSString stringWithFormat:@"error: %@", message];
            return strdup(err.UTF8String);
        }

        NSMutableArray<NSString *> *lines = [NSMutableArray array];
        for (VNRecognizedTextObservation *obs in ocrReq.results) {
            VNRecognizedText *top = [obs topCandidates:1].firstObject;
            if (top) {
                [lines addObject:top.string];
            }
        }
        NSString *ocrText = [lines componentsJoinedByString:@"\n"];

        NSMutableArray<NSString *> *labelList = [NSMutableArray array];
        for (VNClassificationObservation *obs in classReq.results) {
            if (obs.confidence >= 0.5f) {
                [labelList addObject:obs.identifier];
            }
            if (labelList.count >= 10) {
                break;
            }
        }

        CGImageRelease(cgImage);

        NSError *jsonErr = nil;
        NSData *jsonData = [NSJSONSerialization dataWithJSONObject:@{
            @"ocr_text": ocrText ?: @"",
            @"labels": labelList,
        } options:0 error:&jsonErr];
        if (jsonErr || !jsonData) {
            return strdup("{\"ocr_text\":\"\",\"labels\":[]}");
        }

        NSString *jsonStr = [[NSString alloc] initWithData:jsonData encoding:NSUTF8StringEncoding];
        const char *fallback = "{\"ocr_text\":\"\",\"labels\":[]}";
        return strdup(jsonStr.UTF8String ?: fallback);
    }
}

// ocr_pdf_pages opens the PDF at path, renders each page to a CGImage via CoreGraphics,
// then runs VNRecognizeTextRequest on each page, concatenating the results.
// Returns a malloc'd string with all page text joined by newlines. Caller must free.
char* ocr_pdf_pages(const char* path) {
    @autoreleasepool {
        if (path == NULL) {
            return strdup("");
        }

        NSString *nsPath = [NSString stringWithUTF8String:path];
        if (nsPath == nil) {
            return strdup("");
        }
        NSURL *url = [NSURL fileURLWithPath:nsPath];

        CGPDFDocumentRef doc = CGPDFDocumentCreateWithURL((__bridge CFURLRef)url);
        if (!doc) {
            return strdup("");
        }

        size_t pageCount = CGPDFDocumentGetNumberOfPages(doc);
        NSMutableArray<NSString *> *pageTexts = [NSMutableArray array];

        for (size_t i = 1; i <= pageCount; i++) {
            @autoreleasepool {
                CGPDFPageRef page = CGPDFDocumentGetPage(doc, i);
                if (!page) {
                    continue;
                }

                CGRect box = CGPDFPageGetBoxRect(page, kCGPDFMediaBox);
                int width = (int)(box.size.width * 2.0);
                int height = (int)(box.size.height * 2.0);
                if (width <= 0 || height <= 0) {
                    continue;
                }

                CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
                CGContextRef ctx = CGBitmapContextCreate(NULL, width, height, 8, 0, cs,
                    kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
                CGColorSpaceRelease(cs);
                if (!ctx) {
                    continue;
                }

                CGContextSetRGBFillColor(ctx, 1, 1, 1, 1);
                CGContextFillRect(ctx, CGRectMake(0, 0, width, height));
                CGContextScaleCTM(ctx, 2.0, 2.0);
                CGContextDrawPDFPage(ctx, page);

                CGImageRef cgImage = CGBitmapContextCreateImage(ctx);
                CGContextRelease(ctx);
                if (!cgImage) {
                    continue;
                }

                VNRecognizeTextRequest *req = [[VNRecognizeTextRequest alloc] init];
                req.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
                req.automaticallyDetectsLanguage = YES;
                req.usesLanguageCorrection = YES;

                VNImageRequestHandler *handler = [[VNImageRequestHandler alloc] initWithCGImage:cgImage options:@{}];
                NSError *handlerErr = nil;
                BOOL success = NO;
                @try {
                    success = [handler performRequests:@[req] error:&handlerErr];
                } @catch (NSException *exception) {
                    CGImageRelease(cgImage);
                    continue;
                }
                if (!success || handlerErr != nil) {
                    CGImageRelease(cgImage);
                    continue;
                }

                NSMutableArray<NSString *> *lines = [NSMutableArray array];
                for (VNRecognizedTextObservation *obs in req.results) {
                    VNRecognizedText *top = [obs topCandidates:1].firstObject;
                    if (top) {
                        [lines addObject:top.string];
                    }
                }
                NSString *pageText = [lines componentsJoinedByString:@"\n"];
                CGImageRelease(cgImage);

                if (pageText.length > 0) {
                    [pageTexts addObject:pageText];
                }
            }
        }

        CGPDFDocumentRelease(doc);

        NSString *combined = [pageTexts componentsJoinedByString:@"\n\n"];
        return strdup(combined.UTF8String ?: "");
    }
}
