#!/usr/bin/env bash
#
# Builds the scrcpy server that scterm embeds and runs in adb mode,
# third_party/scrcpy-server.jar.d/scrcpy-server, from the vendored source in
# third_party/scrcpy-server-src, and writes its provenance next to it
# (scrcpy-server.provenance). It needs no network and no Gradle: only git, the
# JDK 17 javac and jar, and an Android SDK with platform android-37.0 and
# build-tools 36.0.0. Run it from anywhere in the checkout:
#
#     ANDROID_HOME=/path/to/sdk third_party/build-scrcpy-server.sh
#
# With --check, it builds in a temporary directory and compares the result with
# the embedded jar and its provenance, and changes nothing.
#
# The steps are those of third_party/scrcpy-server-src/build_without_gradle.sh
# (aidl, javac, d8), with these differences:
# - It compiles against android-37.0, as the Android app compiles the same
#   source for its helper. That script's default, android-36, is what the
#   Gradle build of scrcpy 4.1 uses; android-34 lacks the API level 35 constant
#   that AndroidVersions.java names.
# - javac compiles every source aidl generates, as Gradle does. That script
#   leaves out IOnPrimaryClipChangedListener, which no source imports.
# - d8 runs as in the Gradle release build: --release, --min-api 21 (the
#   minSdkVersion in build.gradle), and the platform as a library (--lib).
#   Given the platform with --classpath, as in that script, d8 adds methods to
#   the server's lambdas that forward the default methods of java.util
#   interfaces (Comparator, Predicate) to companion classes that no device has.
# - The output is reproducible: inputs in a fixed order, and a jar that stores
#   classes.dex uncompressed with a fixed date, so that its bytes depend only on
#   the dex and not on the zlib inside the JDK. zipalign then puts the dex on a
#   4-byte boundary: ART maps a stored dex in place only when it is aligned,
#   and copies it out at every start otherwise.

set -euo pipefail
export LC_ALL=C

# Options.parse() refuses to start when the client passes another version:
# keep it equal to serverVersion() in adb.go.
VERSION_NAME=4.1
MIN_API=21
PLATFORM=${ANDROID_PLATFORM:-37.0}
BUILD_TOOLS=${ANDROID_BUILD_TOOLS:-36.0.0}

case "$*" in
    "") check=false ;;
    --check) check=true ;;
    *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC=third_party/scrcpy-server-src
OUT=$ROOT/third_party/scrcpy-server.jar.d
: "${ANDROID_HOME:?set ANDROID_HOME to the Android SDK directory}"
PLATFORM_DIR=$ANDROID_HOME/platforms/android-$PLATFORM
BUILD_TOOLS_DIR=$ANDROID_HOME/build-tools/$BUILD_TOOLS
ANDROID_JAR=$PLATFORM_DIR/android.jar
ANDROID_AIDL=$PLATFORM_DIR/framework.aidl
LAMBDA_JAR=$BUILD_TOOLS_DIR/core-lambda-stubs.jar
for f in "$ANDROID_JAR" "$ANDROID_AIDL" "$LAMBDA_JAR" "$BUILD_TOOLS_DIR/aidl" "$BUILD_TOOLS_DIR/d8" "$BUILD_TOOLS_DIR/zipalign"; do
    if [[ ! -e $f ]]; then
        echo "$f is missing: install platform android-$PLATFORM and build-tools $BUILD_TOOLS" >&2
        exit 1
    fi
done

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
GEN=$WORK/gen
CLASSES=$WORK/classes
DEX=$WORK/dex
mkdir -p "$GEN/com/genymobile/scrcpy" "$CLASSES" "$DEX"

echo "Platform: android-$PLATFORM"
echo "Build-tools: $BUILD_TOOLS"

<< EOF cat > "$GEN/com/genymobile/scrcpy/BuildConfig.java"
package com.genymobile.scrcpy;

public final class BuildConfig {
  public static final boolean DEBUG = false;
  public static final String VERSION_NAME = "$VERSION_NAME";
}
EOF

echo "Generating java from aidl..."
(
    cd "$ROOT/$SRC/src/main/aidl"
    "$BUILD_TOOLS_DIR/aidl" -o"$GEN" -I. \
        android/content/IOnPrimaryClipChangedListener.aidl
    "$BUILD_TOOLS_DIR/aidl" -o"$GEN" -I. -p "$ANDROID_AIDL" \
        android/view/IDisplayWindowListener.aidl
)

echo "Compiling java sources..."
mapfile -t sources < <(find "$ROOT/$SRC/src/main/java" "$GEN" -name '*.java' | sort)
javac -encoding UTF-8 -bootclasspath "$ANDROID_JAR" \
    -cp "$LAMBDA_JAR" \
    -d "$CLASSES" \
    -source 1.8 -target 1.8 \
    "${sources[@]}"

echo "Dexing..."
mapfile -t classes < <(cd "$CLASSES" && find . -name '*.class' | sort)
(
    cd "$CLASSES"
    "$BUILD_TOOLS_DIR/d8" --release --min-api "$MIN_API" --lib "$ANDROID_JAR" \
        --output "$DEX" "${classes[@]}"
)
# The jar holds classes.dex alone: stop if d8 ever writes more.
if [[ -n $(cd "$DEX" && find . -mindepth 1 ! -path ./classes.dex) ]]; then
    echo "d8 wrote more than classes.dex in $DEX" >&2
    exit 1
fi

echo "Archiving..."
jar --create --file "$WORK/unaligned" --no-manifest --no-compress \
    --date=1980-01-01T00:00:02Z -C "$DEX" classes.dex
"$BUILD_TOOLS_DIR/zipalign" -f 4 "$WORK/unaligned" "$WORK/scrcpy-server"

# The tree hash git gives the source as it is now, committed or not, computed
# with a temporary index and object directory so that the repository is left
# alone.
mkdir "$WORK/objects"
tree=$(
    export GIT_INDEX_FILE=$WORK/index GIT_OBJECT_DIRECTORY=$WORK/objects
    git -C "$ROOT" add --all -- "$SRC"
    git -C "$ROOT" write-tree --prefix="$SRC/"
)

sha256=$(sha256sum "$WORK/scrcpy-server" | cut -d' ' -f1)
{
    echo "# The scrcpy server that scterm embeds (scrcpy-server, next to this file),"
    echo "# built from $SRC by third_party/build-scrcpy-server.sh,"
    echo "# which wrote this file. To check that the jar is what the source builds to:"
    echo "#     ANDROID_HOME=/path/to/sdk third_party/build-scrcpy-server.sh --check"
    echo "sha256: $sha256"
    echo "source tree: $tree"
    echo "version name: $VERSION_NAME"
    echo "javac: $(javac -version 2>&1), -source 1.8 -target 1.8"
    echo "platform: android-$PLATFORM, $(grep '^Pkg.Revision=' "$PLATFORM_DIR/source.properties"), android.jar sha256 $(sha256sum "$ANDROID_JAR" | cut -d' ' -f1)"
    echo "build-tools: $BUILD_TOOLS, $("$BUILD_TOOLS_DIR/d8" --version | sed -n 1p), d8.jar sha256 $(sha256sum "$BUILD_TOOLS_DIR/lib/d8.jar" | cut -d' ' -f1)"
    echo "d8: --release --min-api $MIN_API --lib android.jar"
    echo "command: ANDROID_PLATFORM=$PLATFORM ANDROID_BUILD_TOOLS=$BUILD_TOOLS third_party/build-scrcpy-server.sh"
} > "$WORK/scrcpy-server.provenance"

if ! $check; then
    cp "$WORK/scrcpy-server" "$WORK/scrcpy-server.provenance" "$OUT/"
    echo "Server generated in $OUT/scrcpy-server (sha256 $sha256)"
    exit 0
fi

# The toolchain lines may differ from the recorded ones on another computer;
# only the jar, its hash, the source tree and the version name must match
# (server_jar_test.go reads the recorded version name as the jar's).
diff -u --label recorded --label rebuilt "$OUT/scrcpy-server.provenance" "$WORK/scrcpy-server.provenance" || true
recorded() { sed -n "s/^$1: //p" "$OUT/scrcpy-server.provenance"; }
differs=
cmp -s "$WORK/scrcpy-server" "$OUT/scrcpy-server" || differs+=", the embedded jar"
[[ $(recorded sha256) == "$sha256" ]] || differs+=", the recorded sha256"
[[ $(recorded 'source tree') == "$tree" ]] || differs+=", the recorded source tree"
[[ $(recorded 'version name') == "$VERSION_NAME" ]] || differs+=", the recorded version name"
if [[ -z $differs ]]; then
    echo "OK: the embedded server is what $SRC builds to (sha256 $sha256)"
else
    echo "FAILED: these differ from what $SRC builds to (sha256 $sha256): ${differs#, }. Run third_party/build-scrcpy-server.sh and commit the result." >&2
    exit 1
fi
