plugins {
    alias(libs.plugins.android.library)
}

// The vendored scrcpy v4.1 server, compiled from third_party/ without edits so
// the APK carries a helper that matches the reviewed source. It only runs
// under `app_process` with shell identity (see HelperActivation in :app);
// inside the app process nothing references it.
val vendored = layout.projectDirectory.dir("../../third_party/scrcpy-server-src/src/main")

android {
    namespace = "com.genymobile.scrcpy"
    compileSdk = 37
    enableKotlin = false

    defaultConfig {
        minSdk = 27
        // Options.parse() rejects a launcher whose version argument differs.
        buildConfigField("String", "VERSION_NAME", "\"4.1\"")
        consumerProguardFiles("consumer-rules.pro")
    }

    buildFeatures {
        buildConfig = true
        aidl = true
    }

    sourceSets {
        getByName("main") {
            java.directories += vendored.dir("java").asFile.path
            aidl.directories += vendored.dir("aidl").asFile.path
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    lint {
        // Upstream code: hidden-API reflection is intentional and reviewed there.
        abortOnError = false
        checkReleaseBuilds = false
    }
}
