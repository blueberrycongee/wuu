#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
platform="${1:-all}"
case "$platform" in ios|android|all) ;; *) echo "Usage: bash clients/native/verify.sh [ios|android|all]" >&2; exit 2 ;; esac

if [[ "${WUU_NATIVE_PRIVATE_DATABASE:-}" != 1 ]]; then
    export WUU_NATIVE_PRIVATE_DATABASE=1
    exec bash clients/native/with-postgres.sh bash clients/native/verify.sh "$platform"
fi

mkdir -p clients/native/.build
go build -o clients/native/.build/testhost ./clients/native/testhost
export WUU_NATIVE_TESTHOST="$PWD/clients/native/.build/testhost"
go build -o clients/native/.build/testaccount ./clients/native/testaccount
export WUU_NATIVE_TESTACCOUNT="$PWD/clients/native/.build/testaccount"
go test ./internal/remote/account ./internal/remote/conversations -count=1
go test ./internal/remote/host -run 'Test(FilterMobileChat|MobileChat|MobileActivity)' -count=1

if [[ "$platform" == ios || "$platform" == all ]]; then
    NSUnbufferedIO=YES swift test --package-path clients/native/ios
    xcodebuild -project clients/native/ios/Wuu.xcodeproj -scheme Wuu \
        -sdk iphonesimulator -derivedDataPath clients/native/ios/.build-xcode \
        CODE_SIGNING_ALLOWED=NO build
fi
if [[ "$platform" == android || "$platform" == all ]]; then
    # Always run the host integration test even if Gradle cached a prior skipped run.
    clients/native/android/gradlew -p clients/native/android \
        :app:assembleDebug :app:testDebugUnitTest --rerun-tasks
fi
