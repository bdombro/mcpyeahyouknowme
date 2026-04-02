#import <Foundation/Foundation.h>
#import <Vision/Vision.h>
#import <CoreGraphics/CoreGraphics.h>
#import <AppKit/AppKit.h>
#include <stdlib.h>

// analyze_image runs VNRecognizeTextRequest and VNClassifyImageRequest on the image at path.
// Returns a malloc'd JSON string {"ocr_text":"...","labels":["...",...]} or "error:<msg>".
// Caller must free the returned string.
char* analyze_image(const char* path) {
    NSString *nsPath = [NSString stringWithUTF8String:path];
    NSURL *url = [NSURL fileURLWithPath:nsPath];

    // Load image via CGImageSource for broad format support.
    CGImageSourceRef source = CGImageSourceCreateWithURL((__bridge CFURLRef)url, NULL);
    if (!source) {
        const char *err = strdup("error: could not load image");
        return (char *)err;
    }
    CGImageRef cgImage = CGImageSourceCreateImageAtIndex(source, 0, NULL);
    CFRelease(source);
    if (!cgImage) {
        const char *err = strdup("error: could not decode image");
        return (char *)err;
    }

    dispatch_semaphore_t sema = dispatch_semaphore_create(0);
    __block NSString *ocrText = @"";
    __block NSMutableArray<NSString *> *labelList = [NSMutableArray array];
    __block int pending = 2;

    void (^decrement)(void) = ^{
        if (--pending == 0) dispatch_semaphore_signal(sema);
    };

    // OCR request
    VNRecognizeTextRequest *ocrReq = [[VNRecognizeTextRequest alloc] initWithCompletionHandler:^(VNRequest *req, NSError *err) {
        if (!err) {
            NSMutableArray *lines = [NSMutableArray array];
            for (VNRecognizedTextObservation *obs in req.results) {
                VNRecognizedText *top = [obs topCandidates:1].firstObject;
                if (top) [lines addObject:top.string];
            }
            ocrText = [lines componentsJoinedByString:@"\n"];
        }
        decrement();
    }];
    ocrReq.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
    ocrReq.automaticallyDetectsLanguage = YES;
    ocrReq.usesLanguageCorrection = YES;

    // Classification request
    VNClassifyImageRequest *classReq = [[VNClassifyImageRequest alloc] initWithCompletionHandler:^(VNRequest *req, NSError *err) {
        if (!err) {
            for (VNClassificationObservation *obs in req.results) {
                if (obs.confidence >= 0.5f) {
                    [labelList addObject:obs.identifier];
                }
                if (labelList.count >= 10) break;
            }
        }
        decrement();
    }];

    VNImageRequestHandler *handler = [[VNImageRequestHandler alloc] initWithCGImage:cgImage options:@{}];
    NSError *handlerErr = nil;
    [handler performRequests:@[ocrReq, classReq] error:&handlerErr];

    if (handlerErr) {
        // Both completions won't fire; signal manually.
        dispatch_semaphore_signal(sema);
        dispatch_semaphore_signal(sema);
    }

    dispatch_semaphore_wait(sema, dispatch_time(DISPATCH_TIME_NOW, 15 * NSEC_PER_SEC));
    CGImageRelease(cgImage);

    // Build JSON result.
    NSError *jsonErr = nil;
    NSData *jsonData = [NSJSONSerialization dataWithJSONObject:@{
        @"ocr_text": ocrText,
        @"labels": labelList,
    } options:0 error:&jsonErr];

    if (jsonErr || !jsonData) {
        return strdup("{\"ocr_text\":\"\",\"labels\":[]}");
    }
    NSString *jsonStr = [[NSString alloc] initWithData:jsonData encoding:NSUTF8StringEncoding];
    return strdup([jsonStr UTF8String]);
}

// ocr_pdf_pages opens the PDF at path, renders each page to a CGImage via CoreGraphics,
// then runs VNRecognizeTextRequest on each page, concatenating the results.
// Returns a malloc'd string with all page text joined by newlines. Caller must free.
char* ocr_pdf_pages(const char* path) {
    NSString *nsPath = [NSString stringWithUTF8String:path];
    NSURL *url = [NSURL fileURLWithPath:nsPath];

    CGPDFDocumentRef doc = CGPDFDocumentCreateWithURL((__bridge CFURLRef)url);
    if (!doc) return strdup("");

    size_t pageCount = CGPDFDocumentGetNumberOfPages(doc);
    NSMutableArray<NSString *> *pageTexts = [NSMutableArray array];

    for (size_t i = 1; i <= pageCount; i++) {
        CGPDFPageRef page = CGPDFDocumentGetPage(doc, i);
        if (!page) continue;

        CGRect box = CGPDFPageGetBoxRect(page, kCGPDFMediaBox);
        // Render at 150 DPI (72pt * ~2x) for reasonable OCR quality.
        int width  = (int)(box.size.width  * 2.0);
        int height = (int)(box.size.height * 2.0);
        if (width <= 0 || height <= 0) continue;

        CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
        CGContextRef ctx = CGBitmapContextCreate(NULL, width, height, 8, 0, cs,
            kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
        CGColorSpaceRelease(cs);
        if (!ctx) continue;

        CGContextSetRGBFillColor(ctx, 1, 1, 1, 1);
        CGContextFillRect(ctx, CGRectMake(0, 0, width, height));
        CGContextScaleCTM(ctx, 2.0, 2.0);
        CGContextDrawPDFPage(ctx, page);

        CGImageRef cgImage = CGBitmapContextCreateImage(ctx);
        CGContextRelease(ctx);
        if (!cgImage) continue;

        dispatch_semaphore_t sema = dispatch_semaphore_create(0);
        __block NSString *pageText = @"";

        VNRecognizeTextRequest *req = [[VNRecognizeTextRequest alloc] initWithCompletionHandler:^(VNRequest *r, NSError *err) {
            if (!err) {
                NSMutableArray *lines = [NSMutableArray array];
                for (VNRecognizedTextObservation *obs in r.results) {
                    VNRecognizedText *top = [obs topCandidates:1].firstObject;
                    if (top) [lines addObject:top.string];
                }
                pageText = [lines componentsJoinedByString:@"\n"];
            }
            dispatch_semaphore_signal(sema);
        }];
        req.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
        req.automaticallyDetectsLanguage = YES;
        req.usesLanguageCorrection = YES;

        VNImageRequestHandler *handler = [[VNImageRequestHandler alloc] initWithCGImage:cgImage options:@{}];
        [handler performRequests:@[req] error:nil];
        dispatch_semaphore_wait(sema, dispatch_time(DISPATCH_TIME_NOW, 15 * NSEC_PER_SEC));
        CGImageRelease(cgImage);

        if (pageText.length > 0) {
            [pageTexts addObject:pageText];
        }
    }

    CGPDFDocumentRelease(doc);

    NSString *combined = [pageTexts componentsJoinedByString:@"\n\n"];
    return strdup([combined UTF8String]);
}
