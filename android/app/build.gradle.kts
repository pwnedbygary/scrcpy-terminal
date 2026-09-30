plugins {
    alias(libs.plugins.android.application)
}

// Release builds get their version from the tag (-Pscterm.version=2.0.0) and
// their signing key from the environment (CI secrets, see build.yml); without
// SCTERM_KEYSTORE the release APK is left unsigned.
val releaseVersion: String? = providers.gradleProperty("scterm.version").orNull
val releaseKeystore: String? = providers.environmentVariable("SCTERM_KEYSTORE").orNull

// Where the app looks for newer releases; -Pscterm.updateUrl points a test
// build at a local server (devicetest/update_server.py).
val updateUrl: String = providers.gradleProperty("scterm.updateUrl")
    .getOrElse("https://api.github.com/repos/pwnedbygary/scrcpy-terminal/releases/latest")

/** "2.0.0" -> 20000: grows with every release, as Android requires of updates. */
fun versionCodeOf(version: String): Int {
    val parts = version.split('.').map { it.toIntOrNull() ?: -1 }
    require(parts.size == 3 && parts.all { it in 0..99 }) { "scterm.version must be MAJOR.MINOR.PATCH (each 0-99), got $version" }
    return parts[0] * 10_000 + parts[1] * 100 + parts[2]
}

android {
    namespace = "io.github.pwnedbygary.scterm"
    compileSdk = 37

    defaultConfig {
        applicationId = "io.github.pwnedbygary.scterm"
        minSdk = 27
        targetSdk = 37
        versionCode = releaseVersion?.let(::versionCodeOf) ?: 1
        versionName = releaseVersion ?: "dev"
        buildConfigField("String", "UPDATE_URL", "\"$updateUrl\"")
    }

    signingConfigs {
        if (releaseKeystore != null) {
            create("release") {
                storeFile = file(releaseKeystore)
                storePassword = providers.environmentVariable("SCTERM_KEYSTORE_PASSWORD").get()
                keyAlias = providers.environmentVariable("SCTERM_KEY_ALIAS").getOrElse("scterm")
                keyPassword = storePassword
            }
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.findByName("release")
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        buildConfig = true
    }

    lint {
        // Lint the pure-JVM :protocol/:peer code against this minSdk as well:
        // they run on Android 8.1, so JDK 9+ library APIs are off limits.
        checkDependencies = true
        abortOnError = true
        lintConfig = file("lint.xml")
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

dependencies {
    implementation(project(":peer"))
    implementation(project(":scrcpy-server"))
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.ktx)
    implementation(libs.androidx.annotation)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.shizuku.api)
    implementation(libs.shizuku.provider)
    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test.junit)
}
